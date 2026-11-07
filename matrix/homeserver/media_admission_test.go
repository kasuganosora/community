package homeserver

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/mediaproc"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLocalMatrixVideoUsesTheSharedAdmission(t *testing.T) {
	conn := openMediaAdmissionDB(t)
	alice := models.Account{Id: 41, Username: "alice", UserId: 1}
	bob := models.Account{Id: 42, Username: "bob", UserId: 2}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&alice).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&bob).Error)
	var found models.Account
	require.NoError(t, database.GetDB().Where("username = ?", "alice").First(&found).Error)
	require.Equal(t, alice.Id, found.Id)
	require.NotZero(t, found.Id)
	prev := processLocal
	t.Cleanup(func() { processLocal = prev })
	var encodes int
	processLocal = func(_ context.Context, path, _ string, detected string, _ systemsettings.MediaProcessing, _ mediaproc.Limits) (mediaproc.Output, error) {
		encodes++
		return mediaproc.Output{Path: path, MIME: detected, OutputBytes: 4}, nil
	}

	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(clip, mp4Magic(), 0o600))
	_, err := gateLocalUpload(context.Background(), clip, "video/mp4", "")
	require.Error(t, err)
	require.Zero(t, encodes)
	_, err = gateLocalUpload(context.Background(), clip, "video/mp4", "@_ap_bridge:test.local")
	require.Error(t, err)
	require.Zero(t, encodes)

	out, err := gateLocalUpload(context.Background(), clip, "video/mp4", "@alice:test.local")
	require.NoError(t, err)
	require.Equal(t, clip, out.Path)
	require.Equal(t, 1, encodes)
	require.Zero(t, liveOwners(t, conn, alice.Id))

	hold, err := mediaproc.HoldVideo(context.Background(), conn, alice.Id, clip, 4)
	require.NoError(t, err)
	second, err := mediaproc.HoldVideo(context.Background(), conn, alice.Id, clip, 4)
	require.NoError(t, err)
	_, err = gateLocalUpload(context.Background(), clip, "video/mp4", "@alice:test.local")
	require.ErrorIs(t, err, mediaproc.ErrQueue)
	require.Equal(t, 1, encodes, "a full per-owner queue must not start ffmpeg")
	require.NoError(t, os.RemoveAll(dir))
	_, statErr := os.Stat(clip)
	require.True(t, os.IsNotExist(statErr))
	require.NoError(t, hold.Release(context.Background(), conn, models.ProcessFailed))
	require.NoError(t, second.Release(context.Background(), conn, models.ProcessCancelled))
	require.Zero(t, liveOwners(t, conn, alice.Id))

	started := make(chan struct{})
	finish := make(chan struct{})
	processLocal = func(ctx context.Context, path, _ string, detected string, _ systemsettings.MediaProcessing, _ mediaproc.Limits) (mediaproc.Output, error) {
		encodes++
		close(started)
		select {
		case <-finish:
			return mediaproc.Output{Path: path, MIME: detected, OutputBytes: 4}, nil
		case <-ctx.Done():
			return mediaproc.Output{}, ctx.Err()
		}
	}
	clipDir := t.TempDir()
	clip = filepath.Join(clipDir, "clip.mp4")
	require.NoError(t, os.WriteFile(clip, mp4Magic(), 0o600))
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err = gateLocalUpload(context.Background(), clip, "video/mp4", "@alice:test.local")
	}()
	<-started
	require.NoError(t, mediaproc.Enqueue(context.Background(), conn, 900, bob.Id, "video", "video/mp4", "api", "api-bob", 1))
	close(finish)
	wg.Wait()
	require.NoError(t, err)
	require.Equal(t, 2, encodes)
}

func openMediaAdmissionDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(dialect.OpenSQLite(filepath.Join(t.TempDir(), "matrix-admit.db")+"?_pragma=busy_timeout(5000)&_txlock=immediate"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	require.NoError(t, conn.AutoMigrate(&models.Account{}, &models.MediaProcessTask{}, &models.MediaQueueGate{}))
	database.SetTestDB(conn)
	t.Cleanup(func() {
		database.ResetTestDB()
		_ = sqlDB.Close()
	})
	return conn
}

func liveOwners(t *testing.T, conn *gorm.DB, accountID uint64) int64 {
	t.Helper()
	var n int64
	err := conn.Model(&models.MediaProcessTask{}).Where("account_id = ? AND kind = ? AND state IN ?", accountID, "video", []string{models.ProcessQueued, models.ProcessProcessing, models.ProcessSaving}).Count(&n).Error
	require.NoError(t, err)
	return n
}

func mp4Magic() []byte {
	buf := make([]byte, 16)
	copy(buf[4:], []byte("ftypisom"))
	return buf
}
