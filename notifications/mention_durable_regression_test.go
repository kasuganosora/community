package notifications

import (
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProcessMentionNotificationQueuesDurableFeedEvent(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	require.NoError(t, gdb.AutoMigrate(&models.FeedEvent{}, &models.FeedBatch{}))

	author := createLocalAccount(t, gdb, "mention_author")
	recipient := createLocalAccount(t, gdb, "mention_target")
	now := time.Now().UTC().Truncate(time.Millisecond)

	status := &models.Status{
		AccountId:  author.Id,
		Visibility: models.StatusVisibilityPublic,
		CreatedAt:  &now,
		UpdatedAt:  &now,
		Content:    models.StatusContent{Content: "@mention_target hello"},
	}
	require.NoError(t, gdb.Session(&gorm.Session{SkipHooks: true}).Create(status).Error)
	require.NoError(t, gdb.Create(&models.StatusToAccount{StatusId: status.Id, AccountId: recipient.Id}).Error)

	// Reload exact UpdatedAt and Content stored in DB.
	var stored models.Status
	require.NoError(t, gdb.Preload("Content").First(&stored, status.Id).Error)
	ver := mentionStatusVersion{
		updatedAt: *stored.UpdatedAt,
		content:   stored.Content,
	}

	require.NoError(t, processMentionNotification(gdb, stored.Id, false, ver))

	var events []models.FeedEvent
	require.NoError(t, gdb.Where("kind = ?", models.FeedNotification).Find(&events).Error)
	require.Len(t, events, 1, "mention notification must queue a durable FeedNotification event even with SkipHooks")
	require.Equal(t, []uint64{recipient.Id}, events[0].Metadata.RecipientIDs)
}
