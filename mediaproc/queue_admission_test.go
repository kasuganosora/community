package mediaproc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestConcurrentVideoEnqueueRespectsCaps(t *testing.T) {
	admitAcrossConnections(t, openAdmissionDB(t, sqlite.Open(filepath.Join(t.TempDir(), "admit.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate")))
}

func TestConcurrentVideoEnqueueRespectsCapsOnPostgres(t *testing.T) {
	if !pgtest.Enabled() {
		t.Skip("TEST_DATABASE_URL is not set; PostgreSQL admission was not run")
	}
	conn := pgtest.Open(t, &gorm.Config{Logger: logger.Discard})
	require.NoError(t, conn.AutoMigrate(&models.MediaAttachment{}, &models.MediaProcessTask{}, &models.MediaQueueGate{}))
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	admitAcrossConnections(t, conn)
}

func openAdmissionDB(t *testing.T, dialector gorm.Dialector) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	require.NoError(t, conn.AutoMigrate(&models.MediaAttachment{}, &models.MediaProcessTask{}, &models.MediaQueueGate{}))
	t.Cleanup(func() { _ = sqlDB.Close() })
	return conn
}

func admitAcrossConnections(t *testing.T, conn *gorm.DB) {
	t.Helper()
	ok, full, other := raceEnqueue(t, conn, 10, func(i int) (uint64, uint64) {
		return uint64(3000 + i), 7
	})
	require.Zero(t, other, "admission returned a database error instead of a full queue")
	require.Equal(t, maxVideoPerUser, ok)
	require.Equal(t, 10-maxVideoPerUser, full)
	require.EqualValues(t, maxVideoPerUser, liveVideos(t, conn))

	var cancelRow models.MediaProcessTask
	require.NoError(t, conn.Where("account_id = ? AND state = ?", 7, models.ProcessQueued).First(&cancelRow).Error)
	require.NoError(t, conn.Model(&cancelRow).Update("state", models.ProcessCancelled).Error)
	err := Enqueue(context.Background(), conn, 4000, 7, "video", "video/mp4", "staged", "out-after-cancel", 1)
	require.NoError(t, err)
	var failRow models.MediaProcessTask
	require.NoError(t, conn.Where("account_id = ? AND state = ?", 7, models.ProcessQueued).First(&failRow).Error)
	require.NoError(t, conn.Model(&failRow).Update("state", models.ProcessFailed).Error)
	err = Enqueue(context.Background(), conn, 4001, 7, "video", "video/mp4", "staged", "out-after-fail", 1)
	require.NoError(t, err)
	require.EqualValues(t, maxVideoPerUser, liveVideos(t, conn))
	err = Enqueue(context.Background(), conn, 4002, 7, "video", "video/mp4", "staged-rejected", "out-user-rejected", 1)
	require.ErrorIs(t, err, errQueue)
	var rejected int64
	require.NoError(t, conn.Model(&models.MediaProcessTask{}).Where("output_key = ?", "out-user-rejected").Count(&rejected).Error)
	require.Zero(t, rejected)

	ok, full, other = raceEnqueue(t, conn, 20, func(i int) (uint64, uint64) {
		return uint64(1000 + i), uint64(2000 + i)
	})
	require.Zero(t, other, "admission returned a database error instead of a full queue")
	require.Equal(t, maxVideoGlobal-maxVideoPerUser, ok)
	require.Equal(t, 20-(maxVideoGlobal-maxVideoPerUser), full)
	require.EqualValues(t, maxVideoGlobal, liveVideos(t, conn))
	err = Enqueue(context.Background(), conn, 4003, 8, "video", "video/mp4", "staged-rejected", "out-rejected", 1)
	require.ErrorIs(t, err, errQueue)
	require.EqualValues(t, maxVideoGlobal, liveVideos(t, conn))
	require.NoError(t, conn.Model(&models.MediaProcessTask{}).Where("output_key = ?", "out-rejected").Count(&rejected).Error)
	require.Zero(t, rejected)
}

func raceEnqueue(t *testing.T, conn *gorm.DB, n int, id func(int) (uint64, uint64)) (ok, full, other int) {
	t.Helper()
	var wg sync.WaitGroup
	var mu sync.Mutex
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		mediaID, accountID := id(i)
		go func(mediaID, accountID uint64, i int) {
			defer wg.Done()
			<-start
			err := Enqueue(context.Background(), conn, mediaID, accountID, "video", "video/mp4", "staged", fmt.Sprintf("out-%d-%d", accountID, i), 1)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, errQueue):
				full++
			default:
				other++
				t.Logf("enqueue %d: %v", i, err)
			}
		}(mediaID, accountID, i)
	}
	close(start)
	wg.Wait()
	return ok, full, other
}

func liveVideos(t *testing.T, conn *gorm.DB) int64 {
	t.Helper()
	var n int64
	err := conn.Model(&models.MediaProcessTask{}).Where("kind = ? AND state IN ?", "video", []string{models.ProcessQueued, models.ProcessProcessing, models.ProcessSaving}).Count(&n).Error
	require.NoError(t, err)
	return n
}
