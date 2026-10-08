package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSettingsWhitelistAndCommittedRuntime(t *testing.T) {
	env := newAdminTestEnv(t)
	original := systemsettings.Get(systemsettings.KeyTitle, "Community")
	t.Cleanup(func() { systemsettings.Set(systemsettings.KeyTitle, original) })
	c, w := newJSONContext(t, http.MethodPut, gin.Params{{Key: "section", Value: "general"}}, map[string]any{"values": map[string]any{systemsettings.KeyTitle: "Updated Admin Title"}})
	UpdateSettings(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "Updated Admin Title", systemsettings.GetString(systemsettings.KeyTitle, ""))
	var row models.SystemSetting
	require.NoError(t, env.gdb.Where(map[string]any{"key": systemsettings.KeyTitle}).First(&row).Error)
	require.Equal(t, "Updated Admin Title", row.Val)
	c, w = newJSONContext(t, http.MethodPut, gin.Params{{Key: "section", Value: "general"}}, map[string]any{"values": map[string]any{"DB_PASSWORD": "must-not-write"}})
	UpdateSettings(c)
	require.Equal(t, 422, w.Code)
}
func TestSettingsSecretReadRetainClearAndEnvironment(t *testing.T) {
	newAdminTestEnv(t)
	const key = "config.email.password"
	original := systemsettings.Get(key, "")
	t.Cleanup(func() { systemsettings.Set(key, original) })
	systemsettings.Set(key, "existing-test-secret")
	c, w := newJSONContext(t, http.MethodGet, gin.Params{{Key: "section", Value: "email"}}, nil)
	GetSettings(c)
	require.NotContains(t, w.Body.String(), "existing-test-secret")
	var payload SettingsSection
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	var found bool
	for _, field := range payload.Fields {
		if field.Key == key {
			found = true
			require.True(t, field.SecretSet)
			require.Nil(t, field.Value)
		}
	}
	require.True(t, found)
	c, w = newJSONContext(t, http.MethodPut, gin.Params{{Key: "section", Value: "email"}}, map[string]any{"values": map[string]any{"config.email.server": "smtp.example.org"}})
	UpdateSettings(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "existing-test-secret", systemsettings.GetString(key, ""))
	c, w = newJSONContext(t, http.MethodPut, gin.Params{{Key: "section", Value: "email"}}, map[string]any{"values": map[string]any{key: ""}})
	UpdateSettings(c)
	require.Equal(t, 422, w.Code)
	c, w = newJSONContext(t, http.MethodPut, gin.Params{{Key: "section", Value: "email"}}, map[string]any{"clear_secrets": []string{key}})
	UpdateSettings(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Empty(t, systemsettings.GetString(key, ""))
	old, had := config.C.Other[systemsettings.KeyTitle]
	config.C.Other[systemsettings.KeyTitle] = "deployed-title"
	t.Cleanup(func() {
		if had {
			config.C.Other[systemsettings.KeyTitle] = old
		} else {
			delete(config.C.Other, systemsettings.KeyTitle)
		}
	})
	c, w = newJSONContext(t, http.MethodPut, gin.Params{{Key: "section", Value: "general"}}, map[string]any{"values": map[string]any{systemsettings.KeyTitle: "blocked-change"}})
	UpdateSettings(c)
	require.Equal(t, 409, w.Code)
}
func TestSettingsRollbackDoesNotPublishAndDiagnosticsCannotWrite(t *testing.T) {
	env := newAdminTestEnv(t)
	original := systemsettings.Get(systemsettings.KeyTitle, "Community")
	t.Cleanup(func() { systemsettings.Set(systemsettings.KeyTitle, original) })
	require.NoError(t, env.gdb.Callback().Create().Before("gorm:create").Register("qa_settings_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "system_settings" {
			tx.AddError(gorm.ErrInvalidTransaction)
		}
	}))
	t.Cleanup(func() { _ = env.gdb.Callback().Create().Remove("qa_settings_failure") })
	c, w := newJSONContext(t, http.MethodPut, gin.Params{{Key: "section", Value: "general"}}, map[string]any{"values": map[string]any{systemsettings.KeyTitle: "uncommitted-title"}})
	UpdateSettings(c)
	require.Equal(t, 500, w.Code)
	require.Equal(t, original, systemsettings.Get(systemsettings.KeyTitle, "Community"))
	c, w = newJSONContext(t, http.MethodPut, gin.Params{{Key: "section", Value: "database"}}, map[string]any{"values": map[string]any{"database.driver": "sqlite"}})
	UpdateSettings(c)
	require.Equal(t, 422, w.Code)
	c, w = newJSONContext(t, http.MethodPut, gin.Params{{Key: "section", Value: "performance"}}, map[string]any{"values": map[string]any{systemsettings.KeyDefaultPageSize: 1000000}})
	UpdateSettings(c)
	require.Equal(t, 422, w.Code)
}
