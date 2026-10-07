package admin

import (
	"bytes"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/controllers/api/account"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http/httptest"
	"testing"
	"time"
)

func announcementTestRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	raw, err := conn.DB()
	require.NoError(t, err)
	raw.SetMaxOpenConns(1)
	require.NoError(t, conn.Exec(`CREATE TABLE announcements(id INTEGER PRIMARY KEY AUTOINCREMENT,text TEXT NOT NULL,published BOOLEAN DEFAULT false,all_day BOOLEAN DEFAULT false,scheduled_at DATETIME,starts_at DATETIME,ends_at DATETIME,created_at DATETIME,updated_at DATETIME,published_at DATETIME,status_ids TEXT,mention_account_ids TEXT,tags TEXT,emojis_id TEXT)`).Error)
	require.NoError(t, conn.AutoMigrate(&models.AnnouncementDismissal{}))
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous); _ = raw.Close() })
	r := gin.New()
	r.Use(func(c *gin.Context) {
		id := uint64(1)
		if c.GetHeader("X-Test-Account") == "2" {
			id = 2
		}
		c.Set("account", &models.Account{Id: id})
		c.Next()
	})
	r.POST("/admin", SaveAdminAnnouncement)
	r.PUT("/admin/:id", SaveAdminAnnouncement)
	r.POST("/admin/:id/publish", PublishAdminAnnouncement)
	r.POST("/admin/:id/unpublish", UnpublishAdminAnnouncement)
	r.DELETE("/admin/:id", DeleteAdminAnnouncement)
	r.GET("/announcements", account.ListAnnouncements)
	r.POST("/announcements/:id/dismiss", account.DismissAnnouncement)
	return r, conn
}
func announcementRequest(r *gin.Engine, method, path, body, identity string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Account", identity)
	r.ServeHTTP(w, req)
	return w
}
func TestAnnouncementsPublishWindowsDismissAndEdit(t *testing.T) {
	r, conn := announcementTestRouter(t)
	require.Equal(t, 200, announcementRequest(r, "POST", "/admin", `{"content":"<script>secret</script>"}`, "").Code)
	require.JSONEq(t, `[]`, announcementRequest(r, "GET", "/announcements", "", "").Body.String())
	require.Equal(t, 200, announcementRequest(r, "POST", "/admin/1/publish", "", "").Code)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(announcementRequest(r, "GET", "/announcements", "", "").Body.Bytes(), &rows))
	require.Len(t, rows, 1)
	require.Equal(t, "&lt;script&gt;secret&lt;/script&gt;", rows[0]["content"])
	for range 2 {
		require.Equal(t, 200, announcementRequest(r, "POST", "/announcements/1/dismiss", "", "").Code)
	}
	var count int64
	require.NoError(t, conn.Model(&models.AnnouncementDismissal{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.JSONEq(t, `[]`, announcementRequest(r, "GET", "/announcements?with_dismissed=false", "", "").Body.String())
	require.NoError(t, json.Unmarshal(announcementRequest(r, "GET", "/announcements?with_dismissed=false", "", "2").Body.Bytes(), &rows))
	require.Len(t, rows, 1)
	require.NoError(t, conn.Model(&models.AnnouncementDismissal{}).Where("account_id = 1").Update("read_at", time.Now().Add(-time.Hour)).Error)
	require.Equal(t, 200, announcementRequest(r, "PUT", "/admin/1", `{"content":"Revised"}`, "").Code)
	require.NoError(t, json.Unmarshal(announcementRequest(r, "GET", "/announcements?with_dismissed=false", "", "").Body.Bytes(), &rows))
	require.Len(t, rows, 1)
	for _, field := range []string{"scheduled_at", "starts_at"} {
		require.NoError(t, conn.Model(&models.Announcement{}).Where("id = 1").Update(field, time.Now().Add(time.Hour)).Error)
		require.JSONEq(t, `[]`, announcementRequest(r, "GET", "/announcements", "", "").Body.String())
		require.NoError(t, conn.Model(&models.Announcement{}).Where("id = 1").Update(field, nil).Error)
	}
	require.Equal(t, 200, announcementRequest(r, "POST", "/admin/1/unpublish", "", "").Code)
	require.JSONEq(t, `[]`, announcementRequest(r, "GET", "/announcements", "", "").Body.String())
	require.Equal(t, 200, announcementRequest(r, "POST", "/admin/1/publish", "", "").Code)
	require.NoError(t, conn.Model(&models.Announcement{}).Where("id = 1").Update("ends_at", time.Now().Add(-time.Hour)).Error)
	require.JSONEq(t, `[]`, announcementRequest(r, "GET", "/announcements", "", "").Body.String())
	require.Equal(t, 404, announcementRequest(r, "POST", "/announcements/1/dismiss", "", "").Code)
	require.Equal(t, 200, announcementRequest(r, "DELETE", "/admin/1", "", "").Code)
	require.NoError(t, conn.Model(&models.AnnouncementDismissal{}).Count(&count).Error)
	require.Zero(t, count)
}
func TestAnnouncementsInvalidWindowAndUnavailableStorage(t *testing.T) {
	r, conn := announcementTestRouter(t)
	require.Equal(t, 422, announcementRequest(r, "POST", "/admin", `{"content":"ok","starts_at":"2026-10-07T12:00:00Z","ends_at":"2026-10-07T11:00:00Z"}`, "").Code)
	require.NoError(t, conn.Migrator().DropTable(&models.Announcement{}))
	require.Equal(t, 500, announcementRequest(r, "GET", "/announcements", "", "").Code)
}
