package setup

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kasuganosora/community/cmd/tootctl/commands"
	"github.com/kasuganosora/community/controllers/api/passkey"
	"github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func misskeyStages() map[string]stageFunc {
	s := commonStages()
	s["misskey_meta"] = stageMisskeyMeta
	s["misskey_media"] = stageMisskeyMedia
	s["misskey_export"] = stageMisskeyExport
	s["misskey_preflight"] = stageMisskeyPreflight
	s["misskey_import"] = stageMisskeyImport
	s["passkeys"] = stageMisskeyPasskeys
	return s
}

// workDir holds the export bundle, the bucket mirror and reports. It
// contains private source data (password hashes, keys); it is created 0700.
func workDir() string {
	if d := strings.TrimSpace(os.Getenv("SETUP_WORK_DIR")); d != "" {
		return d
	}
	return filepath.Join(ConfigDir(), "setup-work")
}

func (m *MisskeyParams) normalize() *Error {
	m.Host = strings.TrimSpace(m.Host)
	m.Name = strings.TrimSpace(m.Name)
	m.User = strings.TrimSpace(m.User)
	if m.Host == "" || m.Name == "" || m.User == "" {
		return fail("misskey_fields_required")
	}
	if strings.ContainsAny(m.Host, "/@?# \t\r\n") {
		return fail("invalid_database_host")
	}
	if m.Port == 0 {
		m.Port = 5432
	}
	if m.Port < 1 || m.Port > 65535 {
		return fail("invalid_database_port")
	}
	switch m.SSLMode {
	case "":
		m.SSLMode = "disable"
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		return fail("invalid_database_sslmode")
	}
	switch m.IDMethod {
	case "", "aid", "aidx", "meid", "meidg", "objectid", "ulid":
	default:
		return fail("invalid_id_method")
	}
	switch m.MediaSource {
	case "local":
		m.FilesPath = strings.TrimSpace(m.FilesPath)
		if m.FilesPath == "" {
			return fail("misskey_files_required")
		}
	case "s3":
		o := &m.ObjectStorage
		o.Endpoint, o.Bucket, o.PublicURL = strings.TrimSpace(o.Endpoint), strings.TrimSpace(o.Bucket), strings.TrimRight(strings.TrimSpace(o.PublicURL), "/")
		if o.PublicURL == "" {
			return fail("object_storage_fields_required")
		}
		if u, err := url.Parse(o.PublicURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return fail("object_storage_fields_required")
		}
		switch m.MediaMode {
		case "reference":
		case "copy":
			if o.Endpoint == "" || o.Bucket == "" || o.AccessKey == "" {
				return fail("object_storage_fields_required")
			}
		default:
			return fail("object_storage_fields_required")
		}
	default:
		return fail("misskey_files_required")
	}
	return nil
}

func openMisskey(m MisskeyParams, password string) (*gorm.DB, error) {
	dsn := postgresKeywordDSN(m.Host, m.Port, m.User, password, m.Name, m.SSLMode)
	conn, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return nil, fail("misskey_connect_failed", safeConnectHint(err))
	}
	sqlDB, err := conn.DB()
	if err != nil {
		return nil, fail("misskey_connect_failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err = sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fail("misskey_connect_failed", safeConnectHint(err))
	}
	limitAuxPool(conn, "source database (setup wizard)", 2)
	return conn, nil
}

// MisskeyInspection summarizes a source database for the wizard.
type MisskeyInspection struct {
	Counts map[string]int64 `json:"counts"`
	// IDMethod is guessed from the user ID format (Misskey's config.id).
	IDMethod string `json:"id_method"`
	// DetectedBaseURL is the source site origin found in local file and
	// emoji URLs. Misskey keeps its URL in default.yml, not in the
	// database, so it may be empty; the wizard then asks for it.
	DetectedBaseURL   string   `json:"detected_base_url"`
	BaseURLCandidates []string `json:"base_url_candidates,omitempty"`
	// ObjectStorage is prefilled from Misskey's meta (secret omitted).
	UsesObjectStorage bool                 `json:"uses_object_storage"`
	ObjectStorage     *ObjectStorageParams `json:"object_storage,omitempty"`
	HasObjectSecret   bool                 `json:"has_object_secret"`
}

var (
	reAid   = regexp.MustCompile(`^[0-9a-z]{10}$`)
	reAidx  = regexp.MustCompile(`^[0-9a-z]{16}$`)
	reHex24 = regexp.MustCompile(`^[0-9a-f]{24}$`)
	reMeidg = regexp.MustCompile(`^g[0-9a-f]{23}$`)
	reUlid  = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
)

// guessIDMethod infers Misskey's config.id from sample IDs.
func guessIDMethod(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	all := func(re *regexp.Regexp) bool {
		for _, id := range ids {
			if !re.MatchString(id) {
				return false
			}
		}
		return true
	}
	switch {
	case all(reAidx):
		return "aidx"
	case all(reAid):
		return "aid"
	case all(reMeidg):
		return "meidg"
	case all(reUlid):
		return "ulid"
	case all(reHex24):
		// meid: first 12 hex digits are ms + 0x800000000000; objectid:
		// first 8 are seconds. Pick the one that decodes to a plausible date.
		plausible := func(ms int64) bool {
			t := time.UnixMilli(ms)
			return t.After(time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC)) && t.Before(time.Now().Add(48*time.Hour))
		}
		meid, objectid := true, true
		for _, id := range ids {
			a, _ := new(big.Int).SetString(id[:12], 16)
			b, _ := new(big.Int).SetString(id[:8], 16)
			if !plausible(a.Int64() - 0x800000000000) {
				meid = false
			}
			if !plausible(b.Int64() * 1000) {
				objectid = false
			}
		}
		if meid && !objectid {
			return "meid"
		}
		if objectid {
			return "objectid"
		}
	}
	return ""
}

func originOf(raw, requiredPrefix string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || !strings.HasPrefix(u.Path, requiredPrefix) {
		return ""
	}
	return u.Scheme + "://" + strings.ToLower(u.Host)
}

func inspectMisskey(conn *gorm.DB) (*MisskeyInspection, *Error) {
	if !conn.Migrator().HasTable("user") || !conn.Migrator().HasTable("note") || !conn.Migrator().HasTable("user_profile") {
		return nil, fail("misskey_not_misskey")
	}
	res := &MisskeyInspection{Counts: map[string]int64{}}
	count := func(key, sql string) {
		var n int64
		if conn.Raw(sql).Scan(&n).Error == nil {
			res.Counts[key] = n
		}
	}
	count("local_users", `SELECT count(*) FROM "user" WHERE host IS NULL`)
	count("remote_users", `SELECT count(*) FROM "user" WHERE host IS NOT NULL`)
	count("notes", `SELECT count(*) FROM note`)
	count("files", `SELECT count(*) FROM drive_file`)
	count("emojis", `SELECT count(*) FROM emoji WHERE host IS NULL`)
	count("follows", `SELECT count(*) FROM following`)
	if conn.Migrator().HasTable("user_security_key") {
		count("passkeys", `SELECT count(*) FROM user_security_key`)
	}
	var ids []string
	conn.Raw(`SELECT id FROM "user" ORDER BY id LIMIT 50`).Scan(&ids)
	res.IDMethod = guessIDMethod(ids)

	// Origins of locally stored files (<url>/files/<key>) and local emoji.
	var urls []string
	conn.Raw(`SELECT url FROM drive_file WHERE "storedInternal" = true AND "userHost" IS NULL AND url <> '' LIMIT 200`).Scan(&urls)
	var emojiURLs []string
	conn.Raw(`SELECT "originalUrl" FROM emoji WHERE host IS NULL AND "originalUrl" <> '' LIMIT 200`).Scan(&emojiURLs)
	votes := map[string]int{}
	for _, u := range append(urls, emojiURLs...) {
		if o := originOf(u, "/files/"); o != "" {
			votes[o]++
		}
	}
	for o := range votes {
		res.BaseURLCandidates = append(res.BaseURLCandidates, o)
	}
	sort.Slice(res.BaseURLCandidates, func(i, j int) bool { return votes[res.BaseURLCandidates[i]] > votes[res.BaseURLCandidates[j]] })
	if len(res.BaseURLCandidates) == 1 {
		res.DetectedBaseURL = res.BaseURLCandidates[0]
	}

	var metaJSON string
	if conn.Raw(`SELECT row_to_json(m)::text FROM meta m LIMIT 1`).Scan(&metaJSON).Error == nil && metaJSON != "" {
		var meta map[string]any
		if json.Unmarshal([]byte(metaJSON), &meta) == nil {
			str := func(k string) string { s, _ := meta[k].(string); return s }
			res.UsesObjectStorage, _ = meta["useObjectStorage"].(bool)
			if str("objectStorageBucket") != "" || str("objectStorageBaseUrl") != "" {
				endpoint := str("objectStorageEndpoint")
				if endpoint != "" && !strings.Contains(endpoint, "://") {
					scheme := "https://"
					if ssl, ok := meta["objectStorageUseSSL"].(bool); ok && !ssl {
						scheme = "http://"
					}
					endpoint = scheme + endpoint
					if p, ok := meta["objectStoragePort"].(float64); ok && p > 0 {
						endpoint += ":" + strconv.Itoa(int(p))
					}
				}
				pathStyle, _ := meta["objectStorageS3ForcePathStyle"].(bool)
				res.ObjectStorage = &ObjectStorageParams{Endpoint: endpoint, Bucket: str("objectStorageBucket"), Region: str("objectStorageRegion"), AccessKey: str("objectStorageAccessKey"), PublicURL: strings.TrimRight(str("objectStorageBaseUrl"), "/"), PathStyle: pathStyle}
				res.HasObjectSecret = str("objectStorageSecretKey") != ""
			}
		}
	}
	return res, nil
}

// misskeyObjectSecret returns the object-storage secret from Misskey's meta,
// used when the operator leaves the secret field empty.
func misskeyObjectSecret(conn *gorm.DB) string {
	var s string
	conn.Raw(`SELECT COALESCE("objectStorageSecretKey", '') FROM meta LIMIT 1`).Scan(&s)
	return s
}

func (r *runner) misskey() (*MisskeyParams, *gorm.DB, error) {
	job := r.t.snapshot()
	if job.Misskey == nil {
		return nil, nil, fail("stage_failed", "no Misskey source")
	}
	conn, err := openMisskey(*job.Misskey, r.secrets.MisskeyPassword)
	if err != nil {
		return nil, nil, err
	}
	return job.Misskey, conn, nil
}

// lineLogger turns writer output into log lines.
type lineLogger struct {
	r     *runner
	lines int64
	stage string // when set, exporter "progress ..." lines also update this stage's counts
}

var progressLine = regexp.MustCompile(`^progress stage=(\S+) done=(\d+)(?:/(\d+))?`)

func (l *lineLogger) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if m := progressLine.FindStringSubmatch(line); m != nil && l.stage != "" {
			done, _ := strconv.ParseInt(m[2], 10, 64)
			total, _ := strconv.ParseInt(m[3], 10, 64)
			l.r.t.setCounts(l.stage, map[string]int64{"progress_done": done, "progress_total": total})
			l.r.t.setProgress(l.stage, m[1], done, total)
			continue // progress lines update the bar, not the log
		}
		if strings.TrimSpace(line) != "" {
			l.lines++
			l.r.t.logf("info", "%s", line)
		}
	}
	return len(p), nil
}

// stageMisskeyMeta imports instance settings with the same code as
// `tootctl admin import-misskey-meta`.
func stageMisskeyMeta(ctx context.Context, r *runner) error {
	_, src, err := r.misskey()
	if err != nil {
		return err
	}
	defer closeDB(src)
	dst, err := requireDB()
	if err != nil {
		return err
	}
	out := &lineLogger{r: r}
	if err = commands.ImportMisskeyMeta(src.WithContext(ctx), dst.WithContext(ctx), out, commands.MisskeyMetaOptions{}); err != nil {
		r.t.logf("error", "instance settings import failed: %s", sanitize(err.Error()))
		return fail("meta_import_failed")
	}
	r.t.setCount("misskey_meta", "settings", out.lines)
	return nil
}

func safeKey(key string) (string, bool) {
	if key == "" || strings.ContainsAny(key, "\\\x00") || strings.HasPrefix(key, "/") {
		return "", false
	}
	clean := path.Clean(key)
	if clean != key || clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", false
	}
	return clean, true
}

type mirrorObject struct {
	key  string
	size int64 // 0 = unknown
}

// stageMisskeyMedia mirrors object-storage files to the work directory in
// copy mode. Files already present with the expected size are skipped, so
// a retry resumes where it stopped.
func stageMisskeyMedia(ctx context.Context, r *runner) error {
	m, src, err := r.misskey()
	if err != nil {
		return err
	}
	defer closeDB(src)
	if m.MediaSource != "s3" || m.MediaMode != "copy" {
		return errSkipStage
	}
	o := m.ObjectStorage
	secret := r.secrets.S3SecretKey
	if secret == "" {
		secret = misskeyObjectSecret(src)
	}
	client, err := objectClient(o, secret)
	if err != nil {
		return err
	}
	var objects []mirrorObject
	rows, err := src.Raw(`SELECT "accessKey", size FROM drive_file WHERE "storedInternal" = false AND "isLink" = false AND "accessKey" IS NOT NULL ORDER BY id`).Rows()
	if err != nil {
		return fail("misskey_connect_failed")
	}
	for rows.Next() {
		var obj mirrorObject
		if rows.Scan(&obj.key, &obj.size) == nil {
			objects = append(objects, obj)
		}
	}
	_ = rows.Close()
	var emojiURLs []string
	src.Raw(`SELECT COALESCE(NULLIF("originalUrl", ''), "publicUrl") FROM emoji WHERE host IS NULL`).Scan(&emojiURLs)
	for _, u := range emojiURLs {
		if strings.HasPrefix(u, o.PublicURL+"/") {
			if key, err := url.PathUnescape(strings.TrimPrefix(u, o.PublicURL+"/")); err == nil {
				objects = append(objects, mirrorObject{key: key})
			}
		}
	}
	return mirrorObjects(ctx, r, "misskey_media", client, o.Bucket, objects)
}

// mirrorObjects downloads objects into workDir()/bucket with 4 workers.
// Files already present with the expected size are skipped, so a retry
// resumes where it stopped.
func mirrorObjects(ctx context.Context, r *runner, stage string, client *minio.Client, bucket string, objects []mirrorObject) error {
	root := filepath.Join(workDir(), "bucket")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fail("config_write_failed", "the work directory is not writable")
	}
	var totalBytes int64
	for _, obj := range objects {
		totalBytes += obj.size
	}
	r.t.setCounts(stage, map[string]int64{"files_total": int64(len(objects)), "bytes_total": totalBytes})
	var done, doneBytes, missing, skipped atomic.Int64
	jobs := make(chan mirrorObject)
	var wg sync.WaitGroup
	var firstErr atomic.Value
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for obj := range jobs {
				n, existed, err := mirrorOne(ctx, client, bucket, root, obj)
				switch {
				case err == nil && existed:
					skipped.Add(1)
				case err == nil:
				case errors.Is(err, errObjectMissing):
					missing.Add(1)
					r.t.logf("warn", "object missing in bucket: %s", obj.key)
				default:
					firstErr.CompareAndSwap(nil, err)
				}
				doneBytes.Add(n)
				done.Add(1)
			}
		}()
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	report := func() {
		r.t.setCounts(stage, map[string]int64{"files_done": done.Load(), "bytes_done": doneBytes.Load(), "files_missing": missing.Load(), "files_skipped": skipped.Load()})
		r.t.setProgress(stage, "media", done.Load(), int64(len(objects)))
	}
feed:
	for _, obj := range objects {
		select {
		case <-ctx.Done():
			break feed
		case jobs <- obj:
		case <-ticker.C:
			report()
			jobs <- obj
		}
		if firstErr.Load() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	report()
	if err := ctx.Err(); err != nil {
		return err
	}
	if v := firstErr.Load(); v != nil {
		r.t.logf("error", "object download failed: %s", sanitize(v.(error).Error()))
		return fail("object_storage_failed")
	}
	r.t.logf("info", "mirrored %d objects (%d already present, %d missing)", done.Load(), skipped.Load(), missing.Load())
	return nil
}

var errObjectMissing = errors.New("object missing")

func objectClient(o ObjectStorageParams, secret string) (*minio.Client, error) {
	endpoint := o.Endpoint
	secure := true
	switch {
	case strings.HasPrefix(endpoint, "https://"):
		endpoint = strings.TrimPrefix(endpoint, "https://")
	case strings.HasPrefix(endpoint, "http://"):
		endpoint, secure = strings.TrimPrefix(endpoint, "http://"), false
	}
	lookup := minio.BucketLookupAuto
	if o.PathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(strings.TrimRight(endpoint, "/"), &minio.Options{Creds: credentials.NewStaticV4(o.AccessKey, secret, ""), Secure: secure, Region: o.Region, BucketLookup: lookup})
	if err != nil {
		return nil, fail("object_storage_failed", "invalid endpoint")
	}
	return client, nil
}

// checkObjectStorage verifies bucket access (copy mode) without writing.
func checkObjectStorage(ctx context.Context, o ObjectStorageParams, secret string) *Error {
	client, err := objectClient(o, secret)
	if err != nil {
		return err.(*Error)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ok, err := client.BucketExists(ctx, o.Bucket)
	if err != nil || !ok {
		return fail("object_storage_failed")
	}
	return nil
}

func mirrorOne(ctx context.Context, client *minio.Client, bucket, root string, obj mirrorObject) (int64, bool, error) {
	key, ok := safeKey(obj.key)
	if !ok {
		return 0, false, errObjectMissing
	}
	target := filepath.Join(root, filepath.FromSlash(key))
	if info, err := os.Stat(target); err == nil && info.Mode().IsRegular() && (obj.size == 0 || info.Size() == obj.size) {
		return info.Size(), true, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return 0, false, err
	}
	reader, err := client.GetObject(ctx, bucket, obj.key, minio.GetObjectOptions{})
	if err != nil {
		return 0, false, err
	}
	defer reader.Close()
	tmp, err := os.CreateTemp(filepath.Dir(target), ".part-*")
	if err != nil {
		return 0, false, err
	}
	n, err := io.Copy(tmp, reader)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		var resp minio.ErrorResponse
		if errors.As(err, &resp) && (resp.Code == "NoSuchKey" || resp.StatusCode == 404) {
			return 0, false, errObjectMissing
		}
		return 0, false, err
	}
	if obj.size > 0 && n != obj.size {
		_ = os.Remove(tmp.Name())
		return n, false, fmt.Errorf("size mismatch for object")
	}
	if err = os.Rename(tmp.Name(), target); err != nil {
		_ = os.Remove(tmp.Name())
		return n, false, err
	}
	return n, false, nil
}

func exportDir() string { return filepath.Join(workDir(), "export") }

// exportStateDir keeps the exporter's resumable fetch state between attempts.
func exportStateDir() string { return filepath.Join(workDir(), "export.state") }

func pgpassEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `:`, `\:`).Replace(s)
}

// stageMisskeyExport runs scripts/migration/export_misskey.py against the
// source database (keyset-paged read-only transactions, local data plus the
// remote entities it references), producing a private community-transfer
// bundle. The state directory resumes an interrupted fetch; a finished
// bundle is a checkpoint.
func stageMisskeyExport(ctx context.Context, r *runner) error {
	m, src, err := r.misskey()
	if err != nil {
		return err
	}
	closeDB(src)
	job := r.t.snapshot()
	out := exportDir()
	if _, err := os.Stat(filepath.Join(out, "bundle.json")); err == nil {
		r.t.logf("info", "export already complete; reusing %s", out)
		return nil
	}
	script := utils.ResolveAppPath(filepath.Join("scripts", "migration", "export_misskey.py"))
	if !exporterAvailable(script) {
		return fail("export_tool_missing")
	}
	_ = os.RemoveAll(out)
	idMethod := m.IDMethod
	if idMethod == "" {
		return fail("invalid_id_method")
	}
	// --final-sync makes a resumed run pick up rows changed since the
	// previous attempt (new, updated and deleted) before building the bundle.
	args := []string{"--base-url", job.BaseURL, "--source-version", "unknown", "--id-method", idMethod, "--output", out,
		"--state-dir", exportStateDir(), "--final-sync", "--max-media-bytes", strconv.FormatInt(16<<40, 10)}
	switch m.MediaSource {
	case "local":
		args = append(args, "--source-dir", m.FilesPath)
	case "s3":
		args = append(args, "--object-storage-mode", m.MediaMode, "--object-base-url", m.ObjectStorage.PublicURL)
		if m.MediaMode == "copy" {
			args = append(args, "--object-storage-dir", filepath.Join(workDir(), "bucket"))
		}
		if m.FilesPath != "" {
			args = append(args, "--source-dir", m.FilesPath)
		}
	}
	r.t.logf("info", "reading the Misskey database (resumable, read-only)…")
	return runExporter(ctx, r, "misskey_export", script, pgSource{m.Host, m.Port, m.Name, m.User, m.SSLMode, r.secrets.MisskeyPassword}, args, nil)
}

func exporterAvailable(script string) bool {
	_, perr := exec.LookPath("python3")
	_, qerr := exec.LookPath("psql")
	return perr == nil && qerr == nil && exists(script)
}

// pgSource is a source PostgreSQL connection for an exporter script.
type pgSource struct {
	Host    string
	Port    int
	Name    string
	User    string
	SSLMode string
	Pass    string
}

// runExporter runs a Python exporter with libpq settings in the environment
// and the password in a private PGPASSFILE. Exit code 2 (blocking issues)
// is not an error: the preflight stage reports them. onLine, when set,
// receives every stdout line; otherwise the last JSON line is the summary.
func runExporter(ctx context.Context, r *runner, stage, script string, src pgSource, args []string, onLine func([]byte)) error {
	python, _ := exec.LookPath("python3")
	if err := os.MkdirAll(workDir(), 0o700); err != nil {
		return fail("config_write_failed", "the work directory is not writable")
	}
	pass, err := os.CreateTemp(workDir(), ".pgpass-*")
	if err != nil {
		return fail("config_write_failed", "the work directory is not writable")
	}
	defer func() { _ = os.Remove(pass.Name()) }()
	_ = pass.Chmod(0o600)
	_, _ = fmt.Fprintf(pass, "%s:%d:%s:%s:%s\n", pgpassEscape(src.Host), src.Port, pgpassEscape(src.Name), pgpassEscape(src.User), pgpassEscape(src.Pass))
	_ = pass.Close()

	cmd := exec.CommandContext(ctx, python, append([]string{script}, args...)...)
	env := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PG") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "PGHOST="+src.Host, "PGPORT="+strconv.Itoa(src.Port), "PGDATABASE="+src.Name, "PGUSER="+src.User, "PGSSLMODE="+src.SSLMode, "PGPASSFILE="+pass.Name(), "PGCONNECT_TIMEOUT=10")
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = &lineLogger{r: r, stage: stage}
	if err := cmd.Start(); err != nil {
		return fail("export_tool_missing")
	}
	var summary map[string]any
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		if onLine != nil {
			onLine(sc.Bytes())
			continue
		}
		_ = json.Unmarshal(sc.Bytes(), &summary)
	}
	err = cmd.Wait()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return fail("export_failed")
		}
		code = ee.ExitCode()
	}
	if code != 0 && code != 2 {
		return fail("export_failed")
	}
	for _, k := range []string{"source_records", "errors", "warnings"} {
		if v, ok := summary[k].(float64); ok {
			r.t.setCount(stage, k, int64(v))
		}
	}
	if code == 2 {
		r.t.logf("warn", "the export reported blocking issues; the preflight stage lists them")
	}
	return nil
}

func importOptions(archive *datatransfer.Archive) (datatransfer.Options, error) {
	storageDir, err := filepath.Abs(filepath.Join("uploads", "media"))
	if err != nil {
		return datatransfer.Options{}, err
	}
	return datatransfer.Options{Scope: "site", TargetBaseURL: systemsettings.GetBaseURL(), ArchiveDir: archive.Root(), Files: archive.Files, StorageDir: storageDir, VerifiedReferences: map[string]bool{}}, nil
}

func writeReport(name string, report *datatransfer.Report) {
	if report == nil {
		return
	}
	if data, err := json.MarshalIndent(report, "", "  "); err == nil {
		_ = writeFileAtomic(filepath.Join(workDir(), name), data, 0o600)
	}
}

func logIssues(r *runner, report *datatransfer.Report, severity string, limit int) int {
	n := 0
	for _, is := range report.Issues {
		if is.Severity != severity {
			continue
		}
		if n < limit {
			level := "warn"
			if severity == "error" {
				level = "error"
			}
			r.t.logf(level, "[%s] %s %s: %s", is.Code, is.Kind, is.SourceID, is.Message)
		}
		n++
	}
	return n
}

func toInt64(m map[string]int, prefix string) map[string]int64 {
	out := map[string]int64{}
	for k, v := range m {
		out[prefix+k] = int64(v)
	}
	return out
}

func stageMisskeyPreflight(ctx context.Context, r *runner) error {
	return preflight(ctx, r, "misskey_preflight", "misskey_export")
}

// preflight dry-runs the import of the export. On blocking issues the
// export is discarded and its stage reopened so a retry re-reads the source.
func preflight(ctx context.Context, r *runner, stage, exportStage string) error {
	conn, err := requireDB()
	if err != nil {
		return err
	}
	sp, archive, err := openSpooledExport(ctx, r, stage, true)
	if err != nil {
		return err
	}
	defer sp.Close()
	defer archive.Close()
	opts, err := importOptions(archive)
	if err != nil {
		return err
	}
	report, err := datatransfer.ImportSpool(ctx, conn, sp, opts, datatransfer.StreamOptions{ChunkSize: 1000, OnProgress: func(p datatransfer.StreamProgress) {
		r.t.setProgress(stage, "validate/"+p.Stage, int64(p.Done), int64(p.Total))
	}})
	writeReport("preflight-report.json", report)
	if err != nil {
		r.t.logf("error", "preflight could not finish: %s", sanitize(err.Error()))
		return fail("preflight_blocked")
	}
	r.t.setCounts(stage, toInt64(report.Counts, ""))
	warnings := logIssues(r, report, "warning", 20)
	r.t.setCount(stage, "warnings", int64(warnings))
	if !report.CanExecute {
		errs := logIssues(r, report, "error", 50)
		r.t.setCount(stage, "errors", int64(errs))
		// A retry must read the (fixed) source again, so the export is
		// discarded and its stage reopened.
		sp.Close()
		_ = os.RemoveAll(exportDir())
		_ = os.Remove(spoolPath())
		r.t.update(func(j *Job) {
			if st := r.t.stage(exportStage); st != nil {
				st.Status, st.Counts = StatusPending, nil
			}
		})
		return fail("preflight_blocked", fmt.Sprintf("%d blocking issues; see the log and %s", errs, filepath.Join(workDir(), "preflight-report.json")))
	}
	return nil
}

func spoolPath() string { return filepath.Join(workDir(), "spool.sqlite3") }

// openSpooledExport streams bundle.json into an on-disk spool (once; a retry
// reuses a complete spool) and indexes the export's media files. Memory stays
// flat however large the source instance is.
func openSpooledExport(ctx context.Context, r *runner, stage string, fresh bool) (*datatransfer.Spool, *datatransfer.Archive, error) {
	if fresh {
		_ = os.Remove(spoolPath())
	}
	sp, err := datatransfer.OpenSpool(spoolPath())
	if err != nil {
		return nil, nil, fail("preflight_blocked", "the spool cannot be created in the work directory")
	}
	if sp.Meta("complete") != "1" {
		f, err := os.Open(filepath.Join(exportDir(), "bundle.json"))
		if err != nil {
			sp.Close()
			return nil, nil, fail("preflight_blocked", "the export cannot be read")
		}
		var total int64
		if st, e := f.Stat(); e == nil {
			total = st.Size()
		}
		counter := &countingReader{r: f}
		_, err = sp.ReadBundleJSON(ctx, counter, func(int) { r.t.setProgress(stage, "spool", counter.n, total) })
		_ = f.Close()
		if err != nil {
			sp.Close()
			return nil, nil, fail("preflight_blocked", "the export is malformed")
		}
		r.t.setProgress(stage, "spool", total, total)
	}
	limits := datatransfer.Limits{MaxCompressedBytes: 1 << 50, MaxExpandedBytes: 1 << 50, MaxFileBytes: 64 << 30, MaxJSONBytes: 64 << 30, MaxFiles: 100000000, MaxRecords: 100000000,
		Skip: []string{"bundle.json", "manifest.json", ".spool"}, AllowEmpty: true}
	archive, err := datatransfer.OpenDirectory(ctx, exportDir(), limits)
	if err != nil {
		sp.Close()
		return nil, nil, fail("preflight_blocked", "the export cannot be read")
	}
	return sp, archive, nil
}

type countingReader struct {
	r interface{ Read([]byte) (int, error) }
	n int64
}

func (c *countingReader) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.n += int64(n)
	return n, err
}

// stageMisskeyImport runs the site import in one transaction. Import
// receipts make it idempotent: a retry after a failure (nothing committed)
// or after success (everything skipped) is safe.
func stageMisskeyImport(ctx context.Context, r *runner) error {
	return importBundle(ctx, r, "misskey_import")
}

func importBundle(ctx context.Context, r *runner, stage string) error {
	conn, err := requireDB()
	if err != nil {
		return err
	}
	sp, archive, err := openSpooledExport(ctx, r, stage, false)
	if err != nil {
		return err
	}
	defer sp.Close()
	defer archive.Close()
	opts, err := importOptions(archive)
	if err != nil {
		return err
	}
	r.t.logf("info", "importing %d accounts, %d posts, %d media, %d relations, %d emojis in chunks…", sp.Count("accounts"), sp.Count("posts"), sp.Count("media"), sp.Count("relations"), sp.Count("emojis"))
	report, err := datatransfer.ImportSpool(ctx, conn, sp, opts, datatransfer.StreamOptions{ChunkSize: 1000, Apply: true, OnProgress: func(p datatransfer.StreamProgress) {
		r.t.setProgress(stage, "import/"+p.Stage, int64(p.Done), int64(p.Total))
	}})
	writeReport("import-report.json", report)
	if err != nil {
		if report != nil {
			logIssues(r, report, "error", 50)
		}
		r.t.logf("error", "import stopped: %s (committed chunks are kept; retry resumes)", sanitize(err.Error()))
		return fail("import_failed")
	}
	r.t.setCounts(stage, toInt64(report.Created, ""))
	r.t.setCounts(stage, toInt64(report.Archived, "archived_"))
	return nil
}

// stageMisskeyPasskeys imports security keys with the code behind
// `tootctl passkeys import-misskey`.
func stageMisskeyPasskeys(_ context.Context, r *runner) error {
	_, src, err := r.misskey()
	if err != nil {
		return err
	}
	defer closeDB(src)
	if !src.Migrator().HasTable("user_security_key") {
		return errSkipStage
	}
	keys, err := passkey.LoadMisskeySecurityKeys(src)
	if err != nil {
		return fail("passkey_import_failed")
	}
	res, err := passkey.ImportMisskeySecurityKeys(db.GetDB(), keys, false)
	if err != nil {
		return fail("passkey_import_failed")
	}
	r.t.setCounts("passkeys", map[string]int64{"imported": int64(res.Imported), "skipped": int64(res.Skipped), "unmatched": int64(res.Unmatched), "invalid": int64(res.Invalid)})
	return nil
}
