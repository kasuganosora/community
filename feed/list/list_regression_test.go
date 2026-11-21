package list

import (
	"testing"

	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestListReadUpdatesLastViewedWithoutSelfInvalidatingOutbox(t *testing.T) {
	gdb, _ := setupFull(t)
	require.NoError(t, gdb.AutoMigrate(&models.FeedEvent{}, &models.FeedBatch{}))

	owner := createAccount(t, gdb, "owner")
	l := &models.List{Id: 10, AccountId: owner.Id, Title: "Tech", Visibility: models.ListVisibilityPrivate}
	require.NoError(t, gdb.Create(l).Error)

	var beforeCount int64
	require.NoError(t, gdb.Model(&models.FeedEvent{}).Where("kind = ?", models.FeedInvalidate).Count(&beforeCount).Error)

	_, err := GetTimelineContent(l, &feed.ListParam{Viewer: owner, Limit: 10, Other: map[string]any{"database_only": true}})
	require.NoError(t, err)
	var reloaded models.List
	require.NoError(t, gdb.First(&reloaded, l.Id).Error)
	require.NotNil(t, reloaded.LastViewedAt)

	var afterCount int64
	require.NoError(t, gdb.Model(&models.FeedEvent{}).Where("kind = ?", models.FeedInvalidate).Count(&afterCount).Error)
	require.Equal(t, beforeCount, afterCount, "reading a list timeline must not fire List.AfterUpdate and queue FeedInvalidate")

	// Adding and deleting a ListHashtag must queue FeedInvalidate events.
	lh := &models.ListHashtag{ListId: l.Id, TagId: 77}
	require.NoError(t, gdb.Create(lh).Error)
	var afterAdd int64
	require.NoError(t, gdb.Model(&models.FeedEvent{}).Where("kind = ?", models.FeedInvalidate).Count(&afterAdd).Error)
	require.Equal(t, beforeCount+1, afterAdd)

	require.NoError(t, gdb.Where("list_id = ? AND tag_id = ?", l.Id, 77).Delete(&models.ListHashtag{}).Error)
	var afterRemove int64
	require.NoError(t, gdb.Model(&models.FeedEvent{}).Where("kind = ?", models.FeedInvalidate).Count(&afterRemove).Error)
	require.Equal(t, beforeCount+2, afterRemove)
}
