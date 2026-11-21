package timelines

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/kasuganosora/community/controllers/api/conversation"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGroupedNotificationsUseMastodonEnvelope(t *testing.T) {
	conn := setupTestDB(t)
	setGlobalTestDB(t, conn)
	viewer, sender := makeAccount(1), makeAccount(2)
	for _, a := range []*models.Account{viewer, sender} {
		require.NoError(t, conn.Create(a).Error)
	}
	request := func(query string) *httptest.ResponseRecorder {
		c, w := newTestContext(t, http.MethodGet, "/api/v2/notifications"+query, "")
		setAccount(c, viewer)
		GroupedNotificationTimeline(c)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		return w
	}
	require.JSONEq(t, `{"accounts":[],"statuses":[],"notification_groups":[]}`, request("").Body.String())
	require.JSONEq(t, `{"accounts":[],"partial_accounts":[],"statuses":[],"notification_groups":[]}`, request("?expand_accounts=partial_avatars").Body.String())
	for range 2 {
		require.NoError(t, conn.Create(&models.Notification{AccountId: viewer.Id, FromAccountId: sender.Id, Type: models.NotificationTypeFollow}).Error)
	}
	w := request("")
	var page serializer.NotificationGroups
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	require.Len(t, page.Accounts, 1)
	require.Equal(t, "2", page.Accounts[0].Id)
	require.Equal(t, []*serializer.Status{}, page.Statuses)
	require.Len(t, page.NotificationGroups, 2)
	group := page.NotificationGroups[0]
	require.Equal(t, "follow", group.Type)
	require.Equal(t, "ungrouped-"+strconv.FormatUint(group.MostRecentNotificationId, 10), group.GroupKey)
	require.Equal(t, []string{"2"}, group.SampleAccountIds)
	require.Equal(t, 1, group.NotificationsCount)
	require.NotContains(t, w.Body.String(), `"status_id"`)
	require.Contains(t, w.Header().Get("Link"), "max_id=")
}

func TestNotificationsHideResolvedFollowRequests(t *testing.T) {
	conn := setupTestDB(t)
	setGlobalTestDB(t, conn)
	viewer, sender := makeAccount(1), makeAccount(2)
	for _, actor := range []*models.Account{viewer, sender} {
		require.NoError(t, conn.Create(actor).Error)
	}
	writer := conn.Session(&gorm.Session{SkipHooks: true})
	follow := &models.Follow{AccountId: sender.Id, FollowId: viewer.Id, Status: models.FollowStatusRequested}
	require.NoError(t, writer.Create(follow).Error)
	row := &models.Notification{AccountId: viewer.Id, FromAccountId: sender.Id, Type: models.NotificationTypeFollowRequest}
	require.NoError(t, writer.Create(row).Error)

	request := func() string {
		c, w := newTestContext(t, http.MethodGet, "/api/v1/notifications", "")
		setAccount(c, viewer)
		NotificationTimeline(c)
		require.Equal(t, http.StatusOK, w.Code)
		return w.Body.String()
	}
	require.Contains(t, request(), `"type":"follow_request"`)
	require.NoError(t, writer.Model(&models.Follow{}).Where("account_id = ? AND follow_id = ?", sender.Id, viewer.Id).
		UpdateColumn("status", models.FollowStatusFollowed).Error)
	require.JSONEq(t, `[]`, request())
	item, err := serializer.ToNotification(row, viewer)
	require.NoError(t, err)
	require.Nil(t, item)

	// A new request must not revive the notification from the first request,
	// including when paging strictly below the new notification ID.
	require.NoError(t, writer.Where("account_id = ? AND follow_id = ?", sender.Id, viewer.Id).Delete(&models.Follow{}).Error)
	newRequest := &models.Follow{AccountId: sender.Id, FollowId: viewer.Id, Status: models.FollowStatusRequested}
	require.NoError(t, writer.Create(newRequest).Error)
	next := &models.Notification{AccountId: viewer.Id, FromAccountId: sender.Id, Type: models.NotificationTypeFollowRequest}
	require.NoError(t, writer.Create(next).Error)
	var results []serializer.Notification
	require.NoError(t, json.Unmarshal([]byte(request()), &results))
	require.Len(t, results, 1)
	require.Equal(t, strconv.FormatUint(next.Id, 10), results[0].Id)
	old, err := serializer.ToNotifications(models.Notifications{row}, viewer)
	require.NoError(t, err)
	require.Empty(t, old)
}

func TestNotificationMutesAndUnreadTypeFilters(t *testing.T) {
	conn := setupTestDB(t)
	setGlobalTestDB(t, conn)
	require.NoError(t, conn.AutoMigrate(&models.Marker{}))
	viewer, sender := makeAccount(1), makeAccount(2)
	writer := conn.Session(&gorm.Session{SkipHooks: true})
	require.NoError(t, writer.Create(viewer).Error)
	require.NoError(t, writer.Create(sender).Error)
	row := &models.Notification{AccountId: viewer.Id, FromAccountId: sender.Id, Type: models.NotificationTypeFollow}
	require.NoError(t, writer.Create(row).Error)
	unread := func(query string, expected string) {
		t.Helper()
		c, w := newTestContext(t, http.MethodGet, "/api/v2/notifications/unread_count", query)
		setAccount(c, viewer)
		conversation.UnreadNotificationCount(c)
		require.Empty(t, c.Errors)
		require.Equal(t, http.StatusOK, w.Code)
		require.JSONEq(t, expected, w.Body.String())
	}
	mute := &models.MuteAccount{AccountId: viewer.Id, TargetId: sender.Id, Notifications: true}
	require.NoError(t, writer.Create(mute).Error)
	unread("", `{"count":0}`)
	item, err := serializer.ToNotification(row, viewer)
	require.NoError(t, err)
	require.Nil(t, item)
	past := time.Now().Add(-time.Hour)
	require.NoError(t, writer.Model(mute).Update("expired_at", past).Error)
	unread("types[]=follow", `{"count":1}`)
	unread("types[]=mention", `{"count":0}`)
	unread("exclude_types[]=follow", `{"count":0}`)
	unread("limit=0", `{"count":0}`)
	require.NoError(t, writer.Model(mute).Updates(map[string]any{"expired_at": nil, "notifications": false}).Error)
	unread("", `{"count":1}`)
	item, err = serializer.ToNotification(row, viewer)
	require.NoError(t, err)
	require.NotNil(t, item)
}

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
