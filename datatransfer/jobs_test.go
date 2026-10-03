package datatransfer

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func jobFixture(t *testing.T) (*Job, func() *Job) {
	t.Helper()
	db, b, o := personalFixture(t)
	require.NoError(t, db.AutoMigrate(&Job{}))
	t.Setenv("DATA_TRANSFER_DIR", t.TempDir())
	b.Accounts = b.Accounts[:1]
	payload, e := json.Marshal(b)
	require.NoError(t, e)
	j, e := StageJob(context.Background(), db, bytes.NewReader(payload), "bundle.json", "community", "archive", "personal", o.OwnerUserID, o.OwnerAccountID, o.TargetBaseURL, o.StorageDir)
	require.NoError(t, e)
	runJob(db, j.ID)
	reload := func() *Job { var v Job; require.NoError(t, db.First(&v, "id = ?", j.ID).Error); return &v }
	return reload(), reload
}
func TestJobPrivateStagingPreparationAndRevision(t *testing.T) {
	j, reload := jobFixture(t)
	require.Equal(t, "ready", j.State)
	for _, p := range []string{j.Directory, filepath.Join(j.Directory, "upload"), filepath.Join(j.Directory, "prepared.json")} {
		s, e := os.Stat(p)
		require.NoError(t, e)
		if s.IsDir() {
			require.Equal(t, os.FileMode(0700), s.Mode().Perm())
		} else {
			require.Equal(t, os.FileMode(0600), s.Mode().Perm())
		}
	}
	encoded, e := json.Marshal(j)
	require.NoError(t, e)
	require.NotContains(t, string(encoded), j.Directory)
	require.NotContains(t, string(encoded), j.UploadSHA256)
	p, e := loadPrepared(j)
	require.NoError(t, e)
	p.Bundle.Accounts[0].DisplayName = "tampered"
	require.NoError(t, privateJSON(filepath.Join(j.Directory, "prepared.json"), p))
	_, e = loadPrepared(reload())
	require.ErrorContains(t, e, "revision")
}
func TestJobRejectsPersonalSiteAndSecrets(t *testing.T) {
	for _, kind := range []string{"site", "credential", "key"} {
		t.Run(kind, func(t *testing.T) {
			db, b, o := personalFixture(t)
			require.NoError(t, db.AutoMigrate(&Job{}))
			t.Setenv("DATA_TRANSFER_DIR", t.TempDir())
			b.Accounts = b.Accounts[:1]
			switch kind {
			case "site":
				b.Scope = "site"
			case "credential":
				b.Credentials = []Credential{{AccountID: b.OwnerID}}
			case "key":
				b.Keys = []FederationKey{{AccountID: b.OwnerID}}
			}
			data, e := json.Marshal(b)
			require.NoError(t, e)
			j, e := StageJob(context.Background(), db, bytes.NewReader(data), "bundle.json", "community", "archive", "personal", o.OwnerUserID, o.OwnerAccountID, o.TargetBaseURL, o.StorageDir)
			require.NoError(t, e)
			_, _, e = Prepare(context.Background(), db, j)
			require.Error(t, e)
		})
	}
}
func TestJobConcurrentExecuteRevisionLeaseAndRestart(t *testing.T) {
	db, b, o := personalFixture(t)
	require.NoError(t, db.AutoMigrate(&Job{}))
	t.Setenv("DATA_TRANSFER_DIR", t.TempDir())
	b.Accounts = b.Accounts[:1]
	payload, e := json.Marshal(b)
	require.NoError(t, e)
	j, e := StageJob(context.Background(), db, bytes.NewReader(payload), "bundle.json", "community", "archive", "personal", o.OwnerUserID, o.OwnerAccountID, o.TargetBaseURL, o.StorageDir)
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(filepath.Join(j.Directory, ".prepared-crash"), []byte("partial"), 0600))
	runJob(db, j.ID)
	require.NoError(t, db.First(j, "id = ?", j.ID).Error)
	require.Equal(t, "ready", j.State)
	require.Error(t, ExecuteJob(context.Background(), db, j, "wrong"))
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- ExecuteJob(context.Background(), db, j, j.Revision) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	future := time.Now().Add(time.Hour)
	require.NoError(t, db.Model(&Job{}).Where("id = ?", j.ID).Updates(map[string]any{"lease_token": "other-process", "lease_until": future}).Error)
	runJob(db, j.ID)
	var held Job
	require.NoError(t, db.First(&held, "id = ?", j.ID).Error)
	require.Equal(t, "other-process", held.LeaseToken)
	past := time.Now().Add(-time.Minute)
	require.NoError(t, db.Model(&Job{}).Where("id = ?", j.ID).Update("lease_until", past).Error)
	runJob(db, j.ID)
	require.NoError(t, db.First(&held, "id = ?", j.ID).Error)
	require.Equal(t, "completed", held.State)
	runJob(db, j.ID)
	require.NoError(t, db.First(&held, "id = ?", j.ID).Error)
	require.Equal(t, "completed", held.State)
}

func TestJobPreparedMediaSameSizeTamperingRejected(t *testing.T) {
	db, b, o := personalFixture(t)
	require.NoError(t, db.AutoMigrate(&Job{}))
	t.Setenv("DATA_TRANSFER_DIR", t.TempDir())
	b.Accounts = b.Accounts[:1]
	content := []byte("original")
	sum := sha256.Sum256(content)
	b.Media = []Media{{ID: "m", OwnerID: b.OwnerID, Path: "media/file", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content)), ContentType: "application/octet-stream", Name: "original.bin"}}
	manifest, e := json.Marshal(b)
	require.NoError(t, e)
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, value := range map[string][]byte{"bundle.json": manifest, "media/file": content} {
		w, e := z.Create(name)
		require.NoError(t, e)
		_, e = w.Write(value)
		require.NoError(t, e)
	}
	require.NoError(t, z.Close())
	j, e := StageJob(context.Background(), db, &buf, "backup.zip", "community", "archive", "personal", o.OwnerUserID, o.OwnerAccountID, o.TargetBaseURL, o.StorageDir)
	require.NoError(t, e)
	runJob(db, j.ID)
	require.NoError(t, db.First(j, "id = ?", j.ID).Error)
	require.Equal(t, "ready", j.State)
	p, e := loadPrepared(j)
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(p.Files["media/file"].Path, []byte("tampered"), 0600))
	require.NoError(t, ExecuteJob(context.Background(), db, j, j.Revision))
	runJob(db, j.ID)
	require.NoError(t, db.First(j, "id = ?", j.ID).Error)
	require.Equal(t, "failed", j.State)
	var count int64
	require.NoError(t, db.Model(&models.MediaAttachment{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestJobRevokedAdminCannotResumeSiteImport(t *testing.T) {
	db, _, o := personalFixture(t)
	require.NoError(t, db.AutoMigrate(&Job{}))
	j := Job{ID: "revoked-admin", OwnerUserID: o.OwnerUserID, OwnerAccountID: o.OwnerAccountID, Scope: "site", State: "running", Directory: t.TempDir()}
	require.NoError(t, db.Create(&j).Error)
	runJob(db, j.ID)
	require.NoError(t, db.First(&j, "id = ?", j.ID).Error)
	require.Equal(t, "failed", j.State)
	require.Contains(t, j.Error, "permission")
	require.Empty(t, j.LeaseToken)
}
func TestJobUploadChecksumAndCancellationCleanup(t *testing.T) {
	db, b, o := personalFixture(t)
	require.NoError(t, db.AutoMigrate(&Job{}))
	root := t.TempDir()
	t.Setenv("DATA_TRANSFER_DIR", root)
	payload, e := json.Marshal(b)
	require.NoError(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = StageJob(ctx, db, bytes.NewReader(payload), "bundle.json", "community", "archive", "personal", o.OwnerUserID, o.OwnerAccountID, o.TargetBaseURL, o.StorageDir)
	require.Error(t, e)
	entries, e := os.ReadDir(root)
	require.NoError(t, e)
	require.Empty(t, entries)
	j, e := StageJob(context.Background(), db, bytes.NewReader(payload), "bundle.json", "community", "archive", "personal", o.OwnerUserID, o.OwnerAccountID, o.TargetBaseURL, o.StorageDir)
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(filepath.Join(j.Directory, "upload"), []byte("modified"), 0600))
	_, _, e = Prepare(context.Background(), db, j)
	require.ErrorContains(t, e, "checksum")
}

func TestWorkerServiceCancellationDuringPreparationResumes(t *testing.T) {
	db, b, o := personalFixture(t)
	require.NoError(t, db.AutoMigrate(&Job{}))
	t.Setenv("DATA_TRANSFER_DIR", t.TempDir())
	b.Accounts = b.Accounts[:1]
	payload, e := json.Marshal(b)
	require.NoError(t, e)
	j, e := StageJob(context.Background(), db, bytes.NewReader(payload), "bundle.json", "community", "archive", "personal", o.OwnerUserID, o.OwnerAccountID, o.TargetBaseURL, o.StorageDir)
	require.NoError(t, e)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	var fired atomic.Bool
	// Cancel after lease acquisition and owner lookup, before Prepare enters its
	// archive work: this exercises an actual in-flight worker shutdown.
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:cancel_worker", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" && !fired.Swap(true) {
			cancel()
		}
	}))
	runJobContext(parent, db, j.ID)
	require.True(t, fired.Load())
	require.NoError(t, db.Callback().Query().Remove("test:cancel_worker"))
	require.NoError(t, db.First(j, "id = ?", j.ID).Error)
	require.Equal(t, "validating", j.State)
	require.Empty(t, j.LeaseToken)
	require.Nil(t, j.LeaseUntil)
	runJobContext(context.Background(), db, j.ID)
	require.NoError(t, db.First(j, "id = ?", j.ID).Error)
	require.Equal(t, "ready", j.State)
}
func TestStartJobInheritsCancelledServiceContext(t *testing.T) {
	db, b, o := personalFixture(t)
	require.NoError(t, db.AutoMigrate(&Job{}))
	t.Setenv("DATA_TRANSFER_DIR", t.TempDir())
	payload, e := json.Marshal(b)
	require.NoError(t, e)
	j, e := StageJob(context.Background(), db, bytes.NewReader(payload), "bundle.json", "community", "archive", "personal", o.OwnerUserID, o.OwnerAccountID, o.TargetBaseURL, o.StorageDir)
	require.NoError(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	serviceContext.Lock()
	old := serviceContext.ctx
	serviceContext.ctx = ctx
	serviceContext.Unlock()
	defer func() { serviceContext.Lock(); serviceContext.ctx = old; serviceContext.Unlock() }()
	StartJob(db, j.ID)
	require.Eventually(t, func() bool { _, active := activeTransfers.Load(j.ID); return !active }, time.Second, time.Millisecond)
	require.NoError(t, db.First(j, "id = ?", j.ID).Error)
	require.Equal(t, "validating", j.State)
	require.Empty(t, j.LeaseToken)
}

func TestWorkerServiceCancellationBeforeImportAvoidsWritesAndResumes(t *testing.T) {
	db, b, o := personalFixture(t)
	require.NoError(t, db.AutoMigrate(&Job{}))
	t.Setenv("DATA_TRANSFER_DIR", t.TempDir())
	b.Accounts = b.Accounts[:1]
	b.Posts = []Post{{ID: "cancel-post", AuthorID: b.OwnerID, Text: "one post only", Visibility: "public", CreatedAt: time.Now()}}
	payload, e := json.Marshal(b)
	require.NoError(t, e)
	j, e := StageJob(context.Background(), db, bytes.NewReader(payload), "bundle.json", "community", "archive", "personal", o.OwnerUserID, o.OwnerAccountID, o.TargetBaseURL, o.StorageDir)
	require.NoError(t, e)
	runJob(db, j.ID)
	require.NoError(t, db.First(j, "id = ?", j.ID).Error)
	require.Equal(t, "ready", j.State)
	require.NoError(t, ExecuteJob(context.Background(), db, j, j.Revision))
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	var fired atomic.Bool
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:cancel_import", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" && !fired.Swap(true) {
			cancel()
		}
	}))
	runJobContext(parent, db, j.ID)
	require.True(t, fired.Load())
	require.NoError(t, db.Callback().Query().Remove("test:cancel_import"))
	require.NoError(t, db.First(j, "id = ?", j.ID).Error)
	require.Equal(t, "running", j.State)
	require.Empty(t, j.LeaseToken)
	var count int64
	require.NoError(t, db.Model(&models.Status{}).Count(&count).Error)
	require.Zero(t, count)
	runJobContext(context.Background(), db, j.ID)
	require.NoError(t, db.First(j, "id = ?", j.ID).Error)
	require.Equal(t, "completed", j.State)
	require.NoError(t, db.Model(&models.Status{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	runJob(db, j.ID)
	require.NoError(t, db.Model(&models.Status{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
