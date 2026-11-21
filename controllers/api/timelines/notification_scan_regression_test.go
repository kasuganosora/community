package timelines

import (
	"net/http"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestNotificationScanStopsBeforeAHiddenPrefixHidesOlderRows(t *testing.T) {
	conn := setupTestDB(t)
	setGlobalTestDB(t, conn)
	previous := maxNotificationScanRows
	maxNotificationScanRows = 20
	t.Cleanup(func() { maxNotificationScanRows = previous })

	viewer := makeAccount(1)
	sender := makeAccount(2)
	require.NoError(t, conn.Create(viewer).Error)
	require.NoError(t, conn.Create(sender).Error)
	visible := &models.Notification{Id: 1, Type: models.NotificationTypeFollow, AccountId: viewer.Id, FromAccountId: sender.Id}
	require.NoError(t, conn.Create(visible).Error)
	for id := uint64(2); id <= 21; id++ {
		require.NoError(t, conn.Create(&models.Notification{
			Id: id, Type: models.NotificationTypeFollow, AccountId: viewer.Id,
		}).Error)
	}

	c, w := newTestContext(t, http.MethodGet, "/api/v1/notifications", "limit=1")
	setAccount(c, viewer)
	safeHandler(t, func() { NotificationTimeline(c) })
	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, w.Body.String(), `"id":"1"`, "scan bound must stop before the older visible row")
	require.Equal(t, "[]", w.Body.String())
}
