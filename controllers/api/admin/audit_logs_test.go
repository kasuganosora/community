package admin

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuditLogFiltersAndCursor(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.Exec(`CREATE TABLE admin_audit_logs(id INTEGER PRIMARY KEY AUTOINCREMENT,actor_id INTEGER,account_id INTEGER,action TEXT,method TEXT,route TEXT,target_id TEXT,outcome TEXT,status INTEGER,duration_ms INTEGER,created_at DATETIME,completed_at DATETIME)`).Error)
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous); raw, _ := conn.DB(); _ = raw.Close() })
	for i := range 3 {
		require.NoError(t, conn.Create(&models.AdminAuditLog{ActorID: 1, AccountID: 1, Action: "DELETE /api/v1/admin/announcements/:id", Outcome: "success", CreatedAt: time.Date(2026, 10, 7, 12, i, 0, 0, time.UTC)}).Error)
	}
	require.NoError(t, conn.Create(&models.AdminAuditLog{ActorID: 2, AccountID: 2, Action: "POST /api/v1/admin/announcements", Outcome: "failure", CreatedAt: time.Now()}).Error)
	r := gin.New()
	r.GET("/logs", ListAuditLogs)
	call := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/logs"+query, nil))
		return w
	}
	response := call("?actor_id=1&outcome=success&limit=2&from=2026-10-07T12:00:00Z&to=2026-10-07T12:10:00Z")
	require.Equal(t, 200, response.Code)
	var rows []models.AdminAuditLog
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &rows))
	require.Len(t, rows, 2)
	require.EqualValues(t, 3, rows[0].Id)
	require.Contains(t, response.Header().Get("Link"), "max_id=2")
	response = call("?actor_id=1&outcome=success&limit=2&max_id=2")
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &rows))
	require.Len(t, rows, 1)
	require.EqualValues(t, 1, rows[0].Id)
	for _, query := range []string{"?actor_id=invalid", "?outcome=unknown", "?from=invalid", "?limit=0"} {
		require.Equal(t, 422, call(query).Code)
	}
}
