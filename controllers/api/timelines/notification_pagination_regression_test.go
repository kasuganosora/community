package timelines

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNotificationFilteredWindowRemainsReachable(t *testing.T) {
	conn := setupTestDB(t)
	setGlobalTestDB(t, conn)
	previous := maxNotificationScanRows
	maxNotificationScanRows = 20
	t.Cleanup(func() { maxNotificationScanRows = previous })

	viewer := makeAccount(1)
	sender := makeAccount(2)
	require.NoError(t, conn.AutoMigrate(&models.StatusContent{}))
	require.NoError(t, conn.Create([]*models.Account{viewer, sender}).Error)
	hidden := &models.Status{
		Id: 9, AccountId: sender.Id, Visibility: models.StatusVisibilityDirect,
		Content: models.StatusContent{Content: "hidden-direct-body", SpoilerText: "hidden-direct-cw"},
	}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(hidden).Error)
	require.NoError(t, conn.Create(&models.Notification{Id: 19, Type: models.NotificationTypeFollow, AccountId: viewer.Id, FromAccountId: sender.Id}).Error)
	for id := uint64(20); id <= 39; id++ {
		require.NoError(t, conn.Create(&models.Notification{
			Id: id, Type: models.NotificationTypeMention, AccountId: viewer.Id, FromAccountId: sender.Id, StatusId: hidden.Id,
		}).Error)
	}
	require.NoError(t, conn.Create(&models.Notification{Id: 40, Type: models.NotificationTypeFollow, AccountId: viewer.Id, FromAccountId: sender.Id}).Error)

	var pages []string
	seen := map[string]int{}
	query := "limit=20"
	for hop := 0; hop < 6; hop++ {
		body, link := notificationPage(t, viewer, query)
		require.NotContains(t, body, "hidden-direct-body")
		require.NotContains(t, body, "hidden-direct-cw")
		for _, id := range notificationIDs(t, body) {
			seen[id]++
			require.Equal(t, 1, seen[id], "duplicate notification %s", id)
		}
		pages = append(pages, body)
		next := linkRel(link, "next")
		if next == "" {
			break
		}
		parsed, err := url.Parse(next)
		require.NoError(t, err)
		query = parsed.RawQuery
	}
	require.NotContains(t, pages[0], `"id":"19"`, "one response must not scan past the hidden window")
	require.Contains(t, strings.Join(pages, "\n"), `"id":"40"`)
	require.Contains(t, strings.Join(pages, "\n"), `"id":"19"`)
}

func TestNotificationMinIdContinuesPastFilteredWindow(t *testing.T) {
	conn := setupTestDB(t)
	setGlobalTestDB(t, conn)
	previous := maxNotificationScanRows
	maxNotificationScanRows = 20
	t.Cleanup(func() { maxNotificationScanRows = previous })

	viewer := makeAccount(1)
	sender := makeAccount(2)
	require.NoError(t, conn.AutoMigrate(&models.StatusContent{}))
	require.NoError(t, conn.Create([]*models.Account{viewer, sender}).Error)
	hidden := &models.Status{Id: 9, AccountId: sender.Id, Visibility: models.StatusVisibilityDirect}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(hidden).Error)
	require.NoError(t, conn.Create(&models.Notification{Id: 10, Type: models.NotificationTypeFollow, AccountId: viewer.Id, FromAccountId: sender.Id}).Error)
	for id := uint64(11); id <= 30; id++ {
		require.NoError(t, conn.Create(&models.Notification{
			Id: id, Type: models.NotificationTypeMention, AccountId: viewer.Id, FromAccountId: sender.Id, StatusId: hidden.Id,
		}).Error)
	}
	require.NoError(t, conn.Create(&models.Notification{Id: 31, Type: models.NotificationTypeFollow, AccountId: viewer.Id, FromAccountId: sender.Id}).Error)

	query := "limit=1&min_id=10"
	var joined string
	for hop := 0; hop < 6; hop++ {
		body, link := notificationPage(t, viewer, query)
		require.NotContains(t, body, "hidden-direct")
		joined += body
		next := linkRel(link, "next")
		if next == "" {
			break
		}
		parsed, err := url.Parse(next)
		require.NoError(t, err)
		query = parsed.RawQuery
		require.Contains(t, query, "min_id=")
	}
	require.Contains(t, joined, `"id":"31"`)
}

func notificationPage(t *testing.T, viewer *models.Account, rawQuery string) (string, string) {
	t.Helper()
	c, w := newTestContext(t, http.MethodGet, "/api/v1/notifications", rawQuery)
	setAccount(c, viewer)
	safeHandler(t, func() { NotificationTimeline(c) })
	require.Equal(t, http.StatusOK, w.Code)
	return w.Body.String(), w.Header().Get("Link")
}

func notificationIDs(t *testing.T, body string) []string {
	t.Helper()
	var rows []struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &rows))
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func linkRel(header, rel string) string {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="`+rel+`"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start >= 0 && end > start {
			return part[start+1 : end]
		}
	}
	return ""
}
