package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/newacctmod"
	"github.com/kasuganosora/community/storage"
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

func TestSettingsDeploymentMetadataRemainsReadOnlyAndRedacted(t *testing.T) {
	newAdminTestEnv(t)
	t.Cleanup(func() {
		systemsettings.Set("config.storage.secret_key", nil)
		systemsettings.Set("config.storage.bucket", nil)
	})
	t.Setenv("S3_SECRET_KEY", "private-fixture-value")
	old := config.C.Other
	config.C.Other = map[string]string{"S3_SECRET_KEY": "private-fixture-value"}
	t.Cleanup(func() { config.C.Other = old })
	c, w := newJSONContext(t, http.MethodGet, gin.Params{{Key: "section", Value: "storage"}}, nil)
	GetSettings(c)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), "private-fixture-value")
	var result struct {
		Fields []struct {
			Key           string `json:"key"`
			DeploymentKey string `json:"deployment_key"`
			ReadOnly      bool   `json:"read_only"`
			SecretSet     bool   `json:"secret_set"`
			Value         any    `json:"value"`
		}
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	found := false
	for _, field := range result.Fields {
		if field.Key == "config.storage.secret_key" {
			found = true
			require.Equal(t, "S3_SECRET_KEY", field.DeploymentKey)
			require.False(t, field.ReadOnly)
			require.True(t, field.SecretSet)
			require.Nil(t, field.Value)
		}
		if field.Key == "config.storage.bucket" {
			require.NotEqual(t, "community", field.Value)
		}
	}
	require.True(t, found)
	c, w = newJSONContext(t, http.MethodPut, gin.Params{{Key: "section", Value: "storage"}}, map[string]any{"values": map[string]any{"config.storage.secret_key": "attempted-override", "config.storage.bucket": "maid-file"}})
	UpdateSettings(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "private-fixture-value")
	require.NotContains(t, w.Body.String(), "attempted-override")
	require.Equal(t, "attempted-override", systemsettings.GetString("config.storage.secret_key", ""))
	require.Equal(t, "maid-file", systemsettings.GetString("config.storage.bucket", ""))
	require.False(t, strings.Contains(w.Body.String(), `"restart_required":true`))
}

func TestStorageSettingsApplyWithoutRestart(t *testing.T) {
	newAdminTestEnv(t)
	prev := config.C
	t.Cleanup(func() {
		config.C = prev
		storage.ResetDefault()
		systemsettings.Set("config.storage.type", nil)
		systemsettings.Set("config.storage.endpoint", nil)
		systemsettings.Set("config.storage.bucket", nil)
	})
	config.C.StorageType = "local"
	if config.C.Other == nil {
		config.C.Other = map[string]string{}
	}
	storage.ResetDefault()
	require.Equal(t, storage.KindLocal, storage.Default().Kind())
	for _, field := range storageSettingsFields() {
		if field.Key == "config.storage.bucket" {
			require.Empty(t, field.Value)
		}
	}
	c, w := newJSONContext(t, http.MethodPut, gin.Params{{Key: "section", Value: "storage"}}, map[string]any{"values": map[string]any{
		"config.storage.type": "s3", "config.storage.endpoint": "https://acct.r2.example", "config.storage.bucket": "maid-file",
	}})
	UpdateSettings(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "s3", config.C.StorageType)
	require.Equal(t, "maid-file", config.C.Other["S3_BUCKET"])
	require.Equal(t, storage.KindS3, storage.Default().Kind())
}

func TestNewAccountModSettingsRoundTrip(t *testing.T) {
	env := newAdminTestEnv(t)
	keys := []string{newacctmod.KeyEnabled, newacctmod.KeyObservationN, newacctmod.KeyContextX, newacctmod.KeyPolicyText, newacctmod.KeyHoldFederate}
	type snap struct {
		ok bool
		v  any
	}
	original := map[string]snap{}
	for _, key := range keys {
		v, ok := systemsettings.Stored(key)
		original[key] = snap{ok, v}
		systemsettings.Unset(key)
	}
	t.Cleanup(func() {
		for _, key := range keys {
			if original[key].ok {
				systemsettings.Set(key, original[key].v)
			} else {
				systemsettings.Unset(key)
			}
		}
	})

	c, w := newJSONContext(t, http.MethodGet, gin.Params{{Key: "section", Value: "new_account_mod"}}, nil)
	GetSettings(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	var section SettingsSection
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &section))
	got := map[string]any{}
	for _, field := range section.Fields {
		got[field.Key] = field.Value
		require.False(t, field.ReadOnly)
		require.False(t, field.RestartRequired)
	}
	require.Equal(t, true, got[newacctmod.KeyEnabled])
	require.Equal(t, float64(newacctmod.DefaultObservationN()), got[newacctmod.KeyObservationN])
	require.Equal(t, float64(newacctmod.DefaultContextX()), got[newacctmod.KeyContextX])
	require.Equal(t, newacctmod.DefaultPolicy(), got[newacctmod.KeyPolicyText])
	require.Equal(t, true, got[newacctmod.KeyHoldFederate])
	require.True(t, newacctmod.Enabled())
	require.Equal(t, newacctmod.DefaultObservationN(), newacctmod.ObservationN())
	require.Equal(t, newacctmod.DefaultContextX(), newacctmod.ContextX())
	require.Equal(t, newacctmod.DefaultPolicy(), newacctmod.PolicyText())
	require.True(t, newacctmod.HoldFederation())

	c, w = newJSONContext(t, http.MethodPut, gin.Params{{Key: "section", Value: "new_account_mod"}}, map[string]any{"values": map[string]any{
		newacctmod.KeyEnabled:      false,
		newacctmod.KeyObservationN: 12,
		newacctmod.KeyContextX:     3,
		newacctmod.KeyPolicyText:   "No lottery spam.",
		newacctmod.KeyHoldFederate: false,
	}})
	UpdateSettings(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.False(t, newacctmod.Enabled())
	require.Equal(t, 12, newacctmod.ObservationN())
	require.Equal(t, 3, newacctmod.ContextX())
	require.Equal(t, "No lottery spam.", newacctmod.PolicyText())
	require.False(t, newacctmod.HoldFederation())

	var row models.SystemSetting
	require.NoError(t, env.gdb.Where(map[string]any{"key": newacctmod.KeyPolicyText}).First(&row).Error)
	require.Equal(t, "No lottery spam.", row.Val)

	c, w = newJSONContext(t, http.MethodPut, gin.Params{{Key: "section", Value: "new_account_mod"}}, map[string]any{"values": map[string]any{
		newacctmod.KeyObservationN: 0,
	}})
	UpdateSettings(c)
	require.Equal(t, 422, w.Code)
	require.Equal(t, 12, newacctmod.ObservationN())
}
