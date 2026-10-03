package timelines

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNotificationPagesScanPastHiddenBatches(t *testing.T) {
	conn := setupTestDB(t)
	setGlobalTestDB(t, conn)
	writer := conn.Session(&gorm.Session{SkipHooks: true})
	author, viewer, blocked := makeAccount(1), makeAccount(2), makeAccount(3)
	for _, account := range []*models.Account{author, viewer, blocked} {
		require.NoError(t, writer.Create(account).Error)
	}
	visibleIDs := []string{}
	for i := 0; i < 5; i++ {
		n := &models.Notification{AccountId: viewer.Id, FromAccountId: author.Id, Type: models.NotificationTypeFollow}
		require.NoError(t, writer.Create(n).Error)
		visibleIDs = append(visibleIDs, strconv.FormatUint(n.Id, 10))
		// Interleave hidden notifications as well as a completely hidden first batch.
		require.NoError(t, writer.Create(&models.Notification{AccountId: viewer.Id, FromAccountId: blocked.Id, Type: models.NotificationTypeFollow}).Error)
	}
	for i := 0; i < 25; i++ {
		require.NoError(t, writer.Create(&models.Notification{AccountId: viewer.Id, FromAccountId: blocked.Id, Type: models.NotificationTypeFollow}).Error)
	}
	require.NoError(t, writer.Create(&models.Block{AccountId: viewer.Id, TargetAccountId: blocked.Id, Type: models.BlockTypeAccount}).Error)
	var seen []string
	cursor := ""
	for page := 0; page < 4; page++ {
		query := "limit=2"
		if cursor != "" {
			query += "&max_id=" + cursor
		}
		c, w := newTestContext(t, "GET", "/api/v1/notifications", query)
		setAccount(c, viewer)
		NotificationTimeline(c)
		require.Equal(t, 200, w.Code, w.Body.String())
		var rows []*serializer.Notification
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
		if page < 2 {
			require.Len(t, rows, 2)
		} else if page == 2 {
			require.Len(t, rows, 1)
		} else {
			require.Empty(t, rows)
		}
		for _, n := range rows {
			seen = append(seen, n.Id)
			cursor = n.Id
		}
	}
	require.Equal(t, []string{visibleIDs[4], visibleIDs[3], visibleIDs[2], visibleIDs[1], visibleIDs[0]}, seen)
}
