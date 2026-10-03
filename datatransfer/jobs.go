package datatransfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const MaxUploadBytes int64 = 256 << 20

// Job never serializes archive locations, authentication secrets or raw data.
// A lease survives process restarts; entity receipts make resumed work idempotent.
type Job struct {
	ID             string     `gorm:"primaryKey;size:36" json:"id"`
	OwnerUserID    uint64     `gorm:"index" json:"-"`
	OwnerAccountID uint64     `gorm:"index" json:"-"`
	Scope          string     `gorm:"size:16;index" json:"scope"`
	Source         string     `gorm:"size:32" json:"source"`
	Kind           string     `gorm:"size:32" json:"-"`
	Filename       string     `gorm:"size:255" json:"filename"`
	State          string     `gorm:"size:24;index" json:"state"`
	Revision       string     `gorm:"size:64" json:"revision"`
	Report         *Report    `gorm:"type:json;serializer:json" json:"report"`
	Progress       Progress   `gorm:"type:json;serializer:json" json:"progress"`
	Error          string     `gorm:"type:text" json:"error,omitempty"`
	Directory      string     `gorm:"type:text" json:"-"`
	UploadSHA256   string     `gorm:"size:64" json:"-"`
	TargetBaseURL  string     `gorm:"type:text" json:"-"`
	StorageDir     string     `gorm:"type:text" json:"-"`
	LeaseToken     string     `gorm:"size:36" json:"-"`
	LeaseUntil     *time.Time `gorm:"index" json:"-"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (Job) TableName() string { return "data_transfer_jobs" }

func TransferRoot() (string, error) {
	dir := os.Getenv("DATA_TRANSFER_DIR")
	if dir == "" {
		dir = ".data-transfers"
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("invalid private transfer directory")
	}
	if err = os.Chmod(abs, 0700); err != nil {
		return "", err
	}
	return abs, nil
}

// StageJob copies the upload to a private file before the HTTP request returns.
// The expensive inspection and import run outside the request's timeout.
func StageJob(ctx context.Context, conn *gorm.DB, input io.Reader, filename, source, kind, scope string, ownerUserID, ownerAccountID uint64, baseURL, storageDir string) (*Job, error) {
	if scope != "personal" && scope != "site" {
		return nil, errors.New("invalid transfer scope")
	}
	if ownerUserID == 0 || ownerAccountID == 0 {
		return nil, errors.New("authenticated owner required")
	}
	if source == "" {
		source = "auto"
	}
	if kind == "" {
		kind = "archive"
	}
	if source != "auto" && source != "community" && source != "misskey" && source != "mastodon" {
		return nil, errors.New("unsupported source")
	}
	switch kind {
	case "archive", "notes", "following", "blocking", "muting", "domain_blocks", "bookmarks", "lists", "filters":
	default:
		return nil, errors.New("unsupported backup kind")
	}
	root, err := TransferRoot()
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	dir := filepath.Join(root, id)
	if err = os.Mkdir(dir, 0700); err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(dir)
		}
	}()
	f, err := os.OpenFile(filepath.Join(dir, "upload"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(&jobContextReader{ctx: ctx, reader: input}, MaxUploadBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if n == 0 || n > MaxUploadBytes {
		return nil, errors.New("backup must contain between 1 byte and 256 MiB")
	}
	filename = filepath.Base(strings.ReplaceAll(filename, "\\", "/"))
	if len(filename) > 255 {
		return nil, errors.New("backup filename is too long")
	}
	j := &Job{ID: id, OwnerUserID: ownerUserID, OwnerAccountID: ownerAccountID, Scope: scope, Source: source, Kind: kind, Filename: filename, State: "validating", Directory: dir, UploadSHA256: hex.EncodeToString(h.Sum(nil)), TargetBaseURL: baseURL, StorageDir: storageDir, Progress: Progress{Phase: "validating"}}
	if err = conn.WithContext(ctx).Create(j).Error; err != nil {
		return nil, errors.New("could not create import job")
	}
	ok = true
	return j, nil
}

type jobContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *jobContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type preparedJob struct {
	Bundle             Bundle                  `json:"bundle"`
	ArchiveRoot        string                  `json:"archive_root"`
	Files              map[string]preparedFile `json:"files"`
	VerifiedReferences map[string]bool         `json:"verified_references,omitempty"`
}
type preparedFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func privateJSON(path string, value any) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".prepared-*")
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	err = json.NewEncoder(f).Encode(value)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// ValidatePreparedMedia prevents a staged manifest from naming files outside
// its private extraction directory, including symlinks introduced later.
func ValidatePreparedMedia(root string, files map[string]ArchiveFile) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	for _, f := range files {
		p, e := filepath.Abs(f.Path)
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(abs, p)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("media outside private archive")
		}
		resolved, e := filepath.EvalSymlinks(p)
		if e != nil || resolved != p {
			return errors.New("invalid archived media path")
		}
		info, e := os.Lstat(p)
		if e != nil || !info.Mode().IsRegular() || info.Size() != f.Size {
			return errors.New("archived media changed")
		}
	}
	return nil
}

func jobOptions(j *Job, prepared *preparedJob) (Options, error) {
	files := make(map[string]ArchiveFile, len(prepared.Files))
	for name, f := range prepared.Files {
		files[name] = ArchiveFile{Name: name, Path: f.Path, Size: f.Size, SHA256: f.SHA256}
	}
	if err := ValidatePreparedMedia(prepared.ArchiveRoot, files); err != nil {
		return Options{}, err
	}
	return Options{Scope: j.Scope, OwnerUserID: j.OwnerUserID, OwnerAccountID: j.OwnerAccountID, TargetBaseURL: j.TargetBaseURL, ArchiveDir: prepared.ArchiveRoot, Files: files, VerifiedReferences: prepared.VerifiedReferences, StorageDir: j.StorageDir, JobID: j.ID}, nil
}

func Prepare(ctx context.Context, conn *gorm.DB, j *Job) (*Report, string, error) {
	f, err := os.Open(filepath.Join(j.Directory, "upload"))
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return nil, "", err
	}
	if hex.EncodeToString(h.Sum(nil)) != j.UploadSHA256 {
		return nil, "", errors.New("upload checksum changed")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	limits := DefaultLimits()
	limits.TempDir = j.Directory
	a, err := OpenArchive(ctx, j.Filename, f, limits)
	if err != nil {
		return nil, "", err
	}
	keep := false
	defer func() {
		if !keep {
			_ = a.Close()
		}
	}()
	bundle, err := ParseArchive(ctx, a, ParseOptions{Platform: j.Source, Kind: j.Kind})
	if err != nil {
		return nil, "", err
	}
	if bundle.Scope != j.Scope {
		return nil, "", errors.New("backup scope does not match this import endpoint")
	}
	if j.Scope == "personal" && (len(bundle.Credentials) > 0 || len(bundle.Keys) > 0) {
		return nil, "", errors.New("personal backups must not contain authentication or federation keys")
	}
	verified := map[string]bool{}
	if j.Scope == "personal" {
		if err = ResolvePersonalReferences(ctx, conn, bundle); err != nil {
			return nil, "", err
		}
		for _, account := range bundle.Accounts {
			if account.ID != bundle.OwnerID {
				if value, ok := account.Settings["personal_reference_verified"].(bool); ok && value {
					verified[account.ID] = true
				}
			}
		}
	}
	p := &preparedJob{Bundle: *bundle, ArchiveRoot: a.Root(), Files: make(map[string]preparedFile, len(a.Files))}
	p.VerifiedReferences = verified
	for name, f := range a.Files {
		p.Files[name] = preparedFile{Path: f.Path, Size: f.Size, SHA256: f.SHA256}
	}
	opts, err := jobOptions(j, p)
	if err != nil {
		return nil, "", err
	}
	report, err := Preview(ctx, conn, bundle, opts)
	if err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(append(encoded, []byte(fmt.Sprintf("\n%s:%d:%s", j.Scope, j.OwnerAccountID, j.UploadSHA256))...))
	if err = privateJSON(filepath.Join(j.Directory, "prepared.json"), p); err != nil {
		return nil, "", err
	}
	keep = true
	return report, hex.EncodeToString(digest[:]), nil
}

func loadPrepared(j *Job) (*preparedJob, error) {
	f, err := os.Open(filepath.Join(j.Directory, "prepared.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var p preparedJob
	decoder := json.NewDecoder(io.LimitReader(f, 128<<20))
	if err = decoder.Decode(&p); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(&p.Bundle)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(append(encoded, []byte(fmt.Sprintf("\n%s:%d:%s", j.Scope, j.OwnerAccountID, j.UploadSHA256))...))
	if hex.EncodeToString(digest[:]) != j.Revision {
		return nil, errors.New("prepared backup differs from approved revision")
	}
	return &p, nil
}

// ExecuteJob binds execution to the inspected revision. Only the owning
// authenticated principal may call it; the API rechecks roles separately.
func ExecuteJob(ctx context.Context, conn *gorm.DB, j *Job, revision string) error {
	if revision == "" || revision != j.Revision {
		return errors.New("preview revision changed")
	}
	if j.State == "running" || j.State == "completed" {
		return nil
	}
	if j.State != "ready" && j.State != "failed" {
		return errors.New("job is not ready")
	}
	if j.Report == nil || !j.Report.CanExecute {
		return errors.New("resolve preflight errors first")
	}
	r := conn.WithContext(ctx).Model(&Job{}).Where("id = ? AND state IN ? AND revision = ?", j.ID, []string{"ready", "failed"}, revision).Updates(map[string]any{"state": "running", "error": "", "lease_token": "", "lease_until": nil})
	if r.Error != nil {
		return errors.New("could not start import")
	}
	if r.RowsAffected == 0 {
		var current Job
		if conn.WithContext(ctx).Where("id = ? AND owner_user_id = ? AND owner_account_id = ? AND scope = ? AND revision = ?", j.ID, j.OwnerUserID, j.OwnerAccountID, j.Scope, revision).First(&current).Error == nil && (current.State == "running" || current.State == "completed") {
			return nil
		}
		return errors.New("job changed; reload its state")
	}
	return nil
}

var transferWorkers = make(chan struct{}, 2)
var activeTransfers sync.Map
var serviceContext = struct {
	sync.RWMutex
	ctx context.Context
}{ctx: context.Background()}

func StartJob(conn *gorm.DB, id string) {
	if _, loaded := activeTransfers.LoadOrStore(id, true); loaded {
		return
	}
	select {
	case transferWorkers <- struct{}{}:
	default:
		activeTransfers.Delete(id)
		return
	}
	serviceContext.RLock()
	parent := serviceContext.ctx
	serviceContext.RUnlock()
	go func() {
		defer func() { <-transferWorkers; activeTransfers.Delete(id) }()
		runJobContext(parent, conn, id)
	}()
}

func runJob(conn *gorm.DB, id string) {
	runJobContext(context.Background(), conn, id)
}

func runJobContext(parent context.Context, conn *gorm.DB, id string) {
	if parent.Err() != nil {
		return
	}
	conn = conn.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	var j Job
	if conn.First(&j, "id = ?", id).Error != nil {
		return
	}
	if j.State != "validating" && j.State != "running" {
		return
	}
	token := uuid.NewString()
	now := time.Now()
	until := now.Add(90 * time.Second)
	r := conn.Model(&Job{}).Where("id = ? AND state = ? AND (lease_until IS NULL OR lease_until < ?)", id, j.State, now).Updates(map[string]any{"lease_token": token, "lease_until": until})
	if r.Error != nil || r.RowsAffected != 1 {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Minute)
	defer cancel()
	var transactionFenced atomic.Bool
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if transactionFenced.Load() {
					continue
				}
				u := time.Now().Add(90 * time.Second)
				r := conn.WithContext(ctx).Model(&Job{}).Where("id = ? AND lease_token = ?", id, token).Update("lease_until", u)
				if r.Error != nil || r.RowsAffected != 1 {
					cancel()
					return
				}
			}
		}
	}()
	finish := func(state string, report *Report, revision, message string) {
		updates := map[string]any{"state": state, "error": message, "lease_token": "", "lease_until": nil}
		if report != nil {
			b, _ := json.Marshal(report)
			updates["report"] = string(b)
		}
		if revision != "" {
			updates["revision"] = revision
		}
		_ = conn.Model(&Job{}).Where("id = ? AND lease_token = ?", id, token).Updates(updates).Error
	}
	// Service shutdown is resumable, not a malformed backup. A fresh service
	// can reclaim the original state immediately; only this lease may release
	// it. Deadline failures while the service is alive still require review.
	resumeAfterShutdown := func() bool {
		if parent.Err() == nil {
			return false
		}
		_ = conn.Model(&Job{}).Where("id = ? AND lease_token = ?", id, token).
			Updates(map[string]any{"lease_token": "", "lease_until": nil}).Error
		return true
	}
	var user models.User
	if conn.First(&user, j.OwnerUserID).Error != nil || user.IsBan || user.Status != models.UserStatusNormal || (j.Scope == "site" && !user.IsAdmin) {
		finish("failed", nil, "", "The importing account no longer has permission.")
		return
	}
	if j.State == "validating" {
		report, revision, err := Prepare(ctx, conn, &j)
		if err != nil {
			if resumeAfterShutdown() {
				return
			}
			finish("blocked", &Report{CanExecute: false, Counts: map[string]int{}, Created: map[string]int{}, Skipped: map[string]int{}, Archived: map[string]int{}, Issues: []Issue{{Severity: "error", Code: "invalid_backup", Message: "Backup inspection failed. Check the selected format, archive limits and required files."}}}, "", "")
			return
		}
		state := "ready"
		if !report.CanExecute {
			state = "blocked"
		}
		finish(state, report, revision, "")
		return
	}
	p, err := loadPrepared(&j)
	if err != nil {
		finish("failed", nil, "", "The private staged backup is unavailable.")
		return
	}
	opts, err := jobOptions(&j, p)
	if err != nil {
		finish("failed", nil, "", "The staged media no longer matches the inspected backup.")
		return
	}
	opts.LeaseCheck = func(tx *gorm.DB) error {
		firstCheck := !transactionFenced.Swap(true)
		var lock Job
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "lease_token", "lease_until", "state").Where("id = ?", id).Take(&lock).Error; err != nil {
			return err
		}
		if lock.State != "running" || lock.LeaseToken != token || lock.LeaseUntil == nil || (firstCheck && !lock.LeaseUntil.After(time.Now())) {
			return errors.New("import worker lease expired")
		}
		return tx.Model(&Job{}).Where("id = ? AND lease_token = ?", id, token).Update("lease_until", time.Now().Add(90*time.Second)).Error
	}
	report, err := Import(ctx, conn, &p.Bundle, opts, func(progress Progress) {
		b, _ := json.Marshal(progress)
		_ = conn.Model(&Job{}).Where("id = ? AND lease_token = ?", id, token).Update("progress", string(b)).Error
	})
	if err != nil {
		if resumeAfterShutdown() {
			return
		}
		finish("failed", report, "", "Import stopped. No history was broadcast; inspect the report and retry after fixing the problem.")
		return
	}
	finish("completed", report, "", "")
}

// ResumeJobs also picks up interrupted imports after a lease expires. It must
// start after schema migration, and stops cleanly with the application context.
func ResumeJobs(ctx context.Context, conn *gorm.DB) {
	serviceContext.Lock()
	serviceContext.ctx = ctx
	serviceContext.Unlock()
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			var jobs []Job
			if conn.WithContext(ctx).Select("id").Where("state IN ? AND (lease_until IS NULL OR lease_until < ?)", []string{"validating", "running"}, time.Now()).Order("created_at ASC").Limit(10).Find(&jobs).Error == nil {
				for _, j := range jobs {
					StartJob(conn, j.ID)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
