package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminAuditDurableSafeOutcomeAndStorageFailure(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(&models.AdminAuditLog{}))
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous); raw, _ := conn.DB(); _ = raw.Close() })
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("admin_authorized", true)
		c.Set("user", &models.User{Id: 10})
		c.Set("account", &models.Account{Id: 20})
	})
	r.Use(AdminAuditMiddleware())
	executed := 0
	r.POST("/api/v1/admin/accounts/:id/action", func(c *gin.Context) {
		executed++
		if c.Param("id") == "2" {
			c.JSON(422, gin.H{"error": "invalid"})
		} else {
			c.JSON(200, gin.H{})
		}
	})
	call := func(id string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/v1/admin/accounts/"+id+"/action?token=secret", strings.NewReader(`{"password":"secret"}`))
		r.ServeHTTP(w, req)
		return w.Code
	}
	require.Equal(t, 200, call("1"))
	require.Equal(t, 422, call("2"))
	var rows []models.AdminAuditLog
	require.NoError(t, conn.Order("id").Find(&rows).Error)
	require.Len(t, rows, 2)
	require.Equal(t, "success", rows[0].Outcome)
	require.Equal(t, "failure", rows[1].Outcome)
	require.Equal(t, "POST /api/v1/admin/accounts/:id/action", rows[0].Action)
	require.Equal(t, "1", rows[0].TargetID)
	require.EqualValues(t, 10, rows[0].ActorID)
	require.NoError(t, conn.Migrator().DropTable(&models.AdminAuditLog{}))
	require.Equal(t, 503, call("3"))
	require.Equal(t, 2, executed)
}

func TestAdminAuditRequiresAuthorizationAndSkipsNonAdminRoutes(t *testing.T) {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user", &models.User{Id: 10, IsAdmin: true})
		c.Set("account", &models.Account{Id: 20})
	})
	r.Use(AdminAuditMiddleware())
	executed := 0
	handler := func(c *gin.Context) { executed++; c.Status(204) }
	r.POST("/api/v1/admin/accounts/:id/action", handler)
	r.POST("/api/v1/statuses", handler)
	request := func(path string) int {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		return w.Code
	}
	require.Equal(t, 401, request("/api/v1/admin/accounts/1/action"))
	require.Zero(t, executed)
	require.Equal(t, 204, request("/api/v1/statuses"))
	require.Equal(t, 1, executed)
}
