package notifications

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMentionAccountIDsRejectBothBlockDirections(t *testing.T) {
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{SkipDefaultTransaction: true})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.Notification{}, &models.Status{}, &models.StatusToAccount{}, &models.Block{}, &models.Follow{}))
	conn = conn.Session(&gorm.Session{SkipHooks: true})
	for _, a := range []*models.Account{{Id: 1, Username: "author"}, {Id: 2, Username: "allowed"}, {Id: 3, Username: "blocked"}, {Id: 4, Username: "blocked-author"}} {
		require.NoError(t, conn.Omit("Extra").Create(a).Error)
	}
	require.NoError(t, conn.Omit("Content").Create(&models.Status{Id: 10, AccountId: 1, Visibility: models.StatusVisibilityDirect}).Error)
	for _, id := range []uint64{2, 3, 4} {
		require.NoError(t, conn.Create(&models.StatusToAccount{StatusId: 10, AccountId: id}).Error)
	}
	require.NoError(t, conn.Create(&models.Block{AccountId: 1, TargetAccountId: 3, Type: models.BlockTypeAccount}).Error)
	require.NoError(t, conn.Create(&models.Block{AccountId: 4, TargetAccountId: 1, Type: models.BlockTypeAccount}).Error)
	ids, err := mentionAccountIDs(conn, 10)
	require.NoError(t, err)
	require.Equal(t, []uint64{2}, ids)
	require.NoError(t, processMentionNotification(conn, 10, false, mentionStatusVersion{}))
	var notifications []models.Notification
	require.NoError(t, conn.Find(&notifications).Error)
	require.Len(t, notifications, 1)
	require.Equal(t, uint64(2), notifications[0].AccountId)
	require.Equal(t, uint64(10), notifications[0].StatusId)
}
