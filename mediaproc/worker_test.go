package mediaproc

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func workerDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "worker.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(&models.MediaAttachment{}, &models.MediaProcessTask{}))
	return conn
}

func seedQueued(t *testing.T, conn *gorm.DB) *models.MediaAttachment {
	t.Helper()
	row := &models.MediaAttachment{AccountId: 7, FileName: "processing", FilePath: "", ContentType: "video/mp4", Type: models.MediaTypeUnknown, ProcessState: models.ProcessQueued}
	require.NoError(t, conn.Create(row).Error)
	dir := t.TempDir()
	staging := filepath.Join(dir, "input")
	require.NoError(t, os.WriteFile(staging, []byte("input-bytes"), 0o600))
	systemsettings.Set(systemsettings.KeyMediaImageQuality, 70)
	t.Cleanup(func() { systemsettings.Unset(systemsettings.KeyMediaImageQuality) })
	require.NoError(t, Enqueue(context.Background(), conn, row.Id, row.AccountId, "video", "video/mp4", staging, "media/2026/11/07/out.webm", int64(len("input-bytes"))))
	systemsettings.Set(systemsettings.KeyMediaImageQuality, 40)
	return row
}

func TestClaimIsSingleAndRecoversExpiredLease(t *testing.T) {
	conn := workerDB(t)
	seedQueued(t, conn)
	var wg sync.WaitGroup
	okCount := 0
	var mu sync.Mutex
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, err := Claim(context.Background(), conn, time.Now())
			require.NoError(t, err)
			if ok {
				mu.Lock()
				okCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	require.Equal(t, 1, okCount)

	require.NoError(t, conn.Model(&models.MediaProcessTask{}).Where("media_id > 0").Updates(map[string]any{"state": models.ProcessProcessing, "claim_expires_at": time.Now().Add(-time.Minute)}).Error)
	_, ok, err := Claim(context.Background(), conn, time.Now())
	require.NoError(t, err)
	require.True(t, ok, "expired processing lease must be claimable")
	_, ok, err = Claim(context.Background(), conn, time.Now())
	require.NoError(t, err)
	require.False(t, ok, "a live lease must not be claimed twice")
}

func TestFinalizeDoesNotResurrectCancelledUpload(t *testing.T) {
	conn := workerDB(t)
	mem := &storagetest.Memory{}
	restore := storage.SetDefault(mem)
	t.Cleanup(restore)
	row := seedQueued(t, conn)
	task, ok, err := Claim(context.Background(), conn, time.Now())
	require.NoError(t, err)
	require.True(t, ok)
	cancelled, err := Cancel(context.Background(), conn, row.Id, row.AccountId)
	require.NoError(t, err)
	require.True(t, cancelled)
	out := filepath.Join(t.TempDir(), "out.webm")
	require.NoError(t, os.WriteFile(out, []byte("generated"), 0o600))
	prev := preview
	preview = func(context.Context, string, string, string) (string, string, int, int, error) {
		return "", "", 0, 0, nil
	}
	t.Cleanup(func() { preview = prev })
	require.NoError(t, publish(context.Background(), conn, task, Output{Path: out, MIME: "video/webm", OutputBytes: 9}))
	var again models.MediaAttachment
	require.NoError(t, conn.First(&again, row.Id).Error)
	require.Equal(t, models.ProcessCancelled, again.ProcessState)
	require.NotEqual(t, "out.webm", again.FileName)
	_, _, err = mem.Open(context.Background(), task.OutputKey)
	require.Error(t, err)

	require.NoError(t, publish(context.Background(), conn, task, Output{Path: out, MIME: "video/webm", OutputBytes: 9}))
	require.Equal(t, int64(1), countRows(t, conn))
}

func TestEnqueueFreezesConfig(t *testing.T) {
	conn := workerDB(t)
	seedQueued(t, conn)
	var task models.MediaProcessTask
	require.NoError(t, conn.First(&task).Error)
	require.Contains(t, task.ConfigJSON, `"ImageQuality":70`)
	require.NotContains(t, task.ConfigJSON, `"ImageQuality":40`)
}

func countRows(t *testing.T, conn *gorm.DB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, conn.Model(&models.MediaAttachment{}).Count(&n).Error)
	return n
}
