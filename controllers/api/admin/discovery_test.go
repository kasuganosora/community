package admin

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func discoveryAdminDB(t *testing.T) *gorm.DB {
	t.Helper()
	var driver gorm.Dialector = sqlite.Open(fmt.Sprintf("file:discovery_admin_%d?mode=memory&cache=shared", time.Now().UnixNano()))
	if dsn := os.Getenv("DISCOVERY_TEST_MYSQL_DSN"); dsn != "" {
		driver = mysql.Open(dsn)
	} else if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		driver = dialect.OpenPostgres(dsn)
	}
	conn, err := gorm.Open(driver, &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	for _, model := range []any{&models.RecommendationFeedback{}, &models.AdminAuditLog{}} {
		require.NoError(t, conn.AutoMigrate(model))
		require.NoError(t, conn.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(model).Error)
	}
	database.SetTestDB(conn)
	t.Cleanup(func() { database.ResetTestDB(); sqlDB, _ := conn.DB(); _ = sqlDB.Close() })
	return conn
}

func TestDiscoveryGovernanceAuditAndOwnership(t *testing.T) {
	env := &adminTestEnv{gdb: discoveryAdminDB(t)}
	audit := models.AdminAuditLog{ActorID: 1, AccountID: 1, Action: "POST discovery", Method: "POST", Route: "/api/v1/admin/discovery/feedback", Outcome: "pending", CreatedAt: time.Now()}
	require.NoError(t, env.gdb.Create(&audit).Error)
	input := map[string]any{"target_kind": "tag", "target_key": "#Example", "reason": "coordinated spam", "scope": "user", "owner_id": 99}
	c, w := newJSONContext(t, http.MethodPost, nil, input)
	c.Set("admin_audit_id", audit.Id)
	CreateDiscoveryFeedback(c)
	require.Equal(t, 200, w.Code, "%s: %v", w.Body.String(), c.Errors)
	var rule models.RecommendationFeedback
	require.NoError(t, env.gdb.First(&rule).Error)
	require.Equal(t, "site", rule.Scope)
	require.Zero(t, rule.OwnerID)
	require.Equal(t, "example", rule.TargetKey)
	require.NoError(t, env.gdb.First(&audit, audit.Id).Error)
	require.Equal(t, "coordinated spam", audit.Details["reason"])
	audit.Id = 0
	audit.Details = nil
	audit.Method = "DELETE"
	require.NoError(t, env.gdb.Create(&audit).Error)
	c, w = newJSONContext(t, http.MethodDelete, gin.Params{{Key: "id", Value: strconv.FormatUint(rule.ID, 10)}}, nil)
	c.Set("admin_audit_id", audit.Id)
	DeleteDiscoveryFeedback(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	var count int64
	require.NoError(t, env.gdb.Model(&models.RecommendationFeedback{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, env.gdb.First(&audit, audit.Id).Error)
	require.Equal(t, "example", audit.Details["target_key"], "revoked rule retains auditable target and reason")
	c, w = newJSONContext(t, http.MethodPost, nil, input)
	CreateDiscoveryFeedback(c)
	require.Equal(t, 503, w.Code, w.Body.String())
	require.NoError(t, env.gdb.Model(&models.RecommendationFeedback{}).Count(&count).Error)
	require.Zero(t, count, "failure to persist audit detail must roll back governance mutation")
}

func TestDiscoveryGovernanceCannotDeletePersonalFeedback(t *testing.T) {
	env := &adminTestEnv{gdb: discoveryAdminDB(t)}
	row := models.RecommendationFeedback{Scope: "user", OwnerID: 42, Action: "hide_tag", TargetKind: "tag", TargetKey: "example"}
	require.NoError(t, env.gdb.Create(&row).Error)
	c, w := newJSONContext(t, http.MethodDelete, gin.Params{{Key: "id", Value: strconv.FormatUint(row.ID, 10)}}, nil)
	DeleteDiscoveryFeedback(c)
	require.Equal(t, 404, w.Code, w.Body.String())
	var count int64
	require.NoError(t, env.gdb.Model(&row).Where("id = ?", row.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
