package newacctmod

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func openReviewDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "review.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(&ReviewJob{}, &models.Status{}, &models.StatusContent{}))
	return conn
}

func seedStatus(t *testing.T, conn *gorm.DB, id, accountID uint64) {
	t.Helper()
	now := time.Now()
	status := models.Status{Id: id, AccountId: accountID, CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&status).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.StatusContent{StatusId: id, Content: "hello"}).Error)
}

func TestProcessRequeuesWithoutReportWhenLLMMissing(t *testing.T) {
	conn := openReviewDB(t)
	seedStatus(t, conn, 9, 4)
	job := ReviewJob{StatusID: 9, AccountID: 4, State: "pending", NextAttemptAt: time.Now().Add(-time.Second)}
	require.NoError(t, conn.Create(&job).Error)

	require.NoError(t, Process(context.Background(), conn, 9))

	var saved ReviewJob
	require.NoError(t, conn.First(&saved, "status_id = ?", 9).Error)
	require.Equal(t, "pending", saved.State)
	require.Equal(t, 0, saved.Attempts)
	require.Empty(t, saved.LeaseToken)
	require.Equal(t, "llm not configured", saved.LastError)
	require.True(t, saved.NextAttemptAt.After(time.Now().Add(time.Minute)))
}

func TestProcessReclaimsExpiredLease(t *testing.T) {
	conn := openReviewDB(t)
	seedStatus(t, conn, 11, 4)
	job := ReviewJob{
		StatusID: 11, AccountID: 4, State: "processing", Attempts: 2,
		LeaseToken: "stale", NextAttemptAt: time.Now().Add(-time.Minute),
	}
	require.NoError(t, conn.Create(&job).Error)

	require.NoError(t, Process(context.Background(), conn, 11))

	var saved ReviewJob
	require.NoError(t, conn.First(&saved, "status_id = ?", 11).Error)
	require.Equal(t, "pending", saved.State)
	require.Equal(t, 2, saved.Attempts)
	require.Empty(t, saved.LeaseToken)
	require.Equal(t, "llm not configured", saved.LastError)
}

func TestProcessLeavesLiveLeaseAlone(t *testing.T) {
	conn := openReviewDB(t)
	seedStatus(t, conn, 12, 4)
	job := ReviewJob{
		StatusID: 12, AccountID: 4, State: "processing", Attempts: 1,
		LeaseToken: "live", NextAttemptAt: time.Now().Add(time.Minute),
	}
	require.NoError(t, conn.Create(&job).Error)

	require.NoError(t, Process(context.Background(), conn, 12))

	var saved ReviewJob
	require.NoError(t, conn.First(&saved, "status_id = ?", 12).Error)
	require.Equal(t, "processing", saved.State)
	require.Equal(t, 1, saved.Attempts)
	require.Equal(t, "live", saved.LeaseToken)
}
