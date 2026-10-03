package serializer

import (
	"testing"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHistoricalNotificationVisibilityRevocation(t *testing.T) {
	withTestDBMigrated(t, &models.Status{}, &models.StatusToAccount{}, &models.Block{}, &models.Follow{})
	conn := db.GetDB().Session(&gorm.Session{SkipHooks: true})
	author, viewer := &models.Account{Id: 1, Username: "author"}, &models.Account{Id: 2, Username: "viewer"}
	require.NoError(t, conn.Omit("Extra").Create(author).Error)
	require.NoError(t, conn.Omit("Extra").Create(viewer).Error)
	status := &models.Status{Id: 10, AccountId: 1, Visibility: models.StatusVisibilityDirect}
	require.NoError(t, conn.Omit("Content").Create(status).Error)
	require.NoError(t, conn.Create(&models.StatusToAccount{StatusId: 10, AccountId: 2}).Error)
	n := &models.Notification{Id: 50, AccountId: 2, FromAccountId: 1, StatusId: 10, Type: models.NotificationTypeMention}
	visible, err := NotificationVisible(n, viewer)
	require.NoError(t, err)
	require.True(t, visible)
	for _, reverse := range []bool{false, true} {
		blocker, target := uint64(2), uint64(1)
		if reverse {
			blocker, target = target, blocker
		}
		block := &models.Block{AccountId: blocker, TargetAccountId: target, Type: models.BlockTypeAccount}
		require.NoError(t, conn.Create(block).Error)
		single, err := ToNotification(n, viewer)
		require.NoError(t, err)
		require.Nil(t, single)
		batch, err := ToNotifications([]*models.Notification{n}, viewer)
		require.NoError(t, err)
		require.Empty(t, batch)
		follow := &models.Notification{Id: 51, AccountId: 2, FromAccountId: 1, Type: models.NotificationTypeFollow}
		single, err = ToNotification(follow, viewer)
		require.NoError(t, err)
		require.Nil(t, single)
		require.NoError(t, conn.Delete(block).Error)
	}
	require.NoError(t, conn.Where("status_id = ?", 10).Delete(&models.StatusToAccount{}).Error)
	visible, err = NotificationVisible(n, viewer)
	require.NoError(t, err)
	require.False(t, visible, "old mention no longer grants access")
	require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", 10).UpdateColumn("visibility", models.StatusVisibilityPrivate).Error)
	follow := &models.Follow{AccountId: 2, FollowId: 1, Status: models.FollowStatusFollowed}
	require.NoError(t, conn.Create(follow).Error)
	visible, err = NotificationVisible(n, viewer)
	require.NoError(t, err)
	require.True(t, visible)
	require.NoError(t, conn.Where("account_id = ? AND follow_id = ?", 2, 1).Delete(&models.Follow{}).Error)
	visible, err = NotificationVisible(n, viewer)
	require.NoError(t, err)
	require.False(t, visible, "unfollow revokes historical private notification")
}
