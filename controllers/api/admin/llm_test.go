package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/llmsvc"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils/translation"
	"github.com/stretchr/testify/require"
)

func TestLLMSettingsKeyIsWriteOnlyAndEncrypted(t *testing.T) {
	env := newAdminTestEnv(t)
	require.NoError(t, env.gdb.AutoMigrate(llmsvc.Models()...))
	if config.C.EncryptedKey == "" {
		config.C.EncryptedKey = "unit-test-key-0123456789abcdef"
		t.Cleanup(func() { config.C.EncryptedKey = "" })
	}
	for _, k := range []string{llmsvc.KeyConfig, translation.KeyMode, systemsettings.KeyIsEnableTranslation, translation.KeyUserDailyLimit, translation.KeyDailyLimit, translation.KeyUserPerMinute} {
		old := systemsettings.Get(k, nil)
		t.Cleanup(func() { systemsettings.Set(k, old) })
	}
	const secret = "sk-example-never-returned-9876"
	body := map[string]any{
		"providers":   []map[string]any{{"name": "Example", "type": "openai", "model": "example-model", "api_key": secret, "timeout_seconds": 30}},
		"services":    map[string]string{"translation": "#0"},
		"translation": map[string]any{"enabled": true, "mode": "llm", "user_daily_limit": 5, "daily_limit": 50, "user_per_minute": 3},
	}
	c, w := newJSONContext(t, http.MethodPut, nil, body)
	UpdateLLMSettings(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), secret)
	require.NotContains(t, w.Body.String(), "api_key_enc")
	var resp llmSettingsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Providers, 1)
	require.True(t, resp.Providers[0].APIKeySet)
	id := resp.Providers[0].ID
	require.Equal(t, id, resp.Services["translation"])
	require.Equal(t, "llm", resp.Translation.Mode)

	stored, _ := json.Marshal(systemsettings.Get(llmsvc.KeyConfig, ""))
	require.NotContains(t, string(stored), secret, "API key must be encrypted at rest")
	require.NotZero(t, countSetting(t, env, llmsvc.KeyConfig))

	// Saving without api_key keeps the stored key.
	body["providers"] = []map[string]any{{"id": id, "name": "Renamed", "type": "openai", "model": "example-model"}}
	body["services"] = map[string]string{"translation": id}
	c, w = newJSONContext(t, http.MethodPut, nil, body)
	UpdateLLMSettings(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp.Providers[0].APIKeySet)
	require.Equal(t, "Renamed", resp.Providers[0].Name)

	// LLM mode without a translation provider is rejected with a stable code.
	body["services"] = map[string]string{}
	c, w = newJSONContext(t, http.MethodPut, nil, body)
	UpdateLLMSettings(c)
	require.Equal(t, 422, w.Code)
	require.Contains(t, w.Body.String(), "llm_translation_provider_required")

	c, w = newJSONContext(t, http.MethodGet, nil, nil)
	LLMUsage(c)
	require.Equal(t, 200, w.Code, w.Body.String())
}

func countSetting(t *testing.T, env *adminTestEnv, key string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, env.gdb.Model(&models.SystemSetting{}).Where(map[string]any{"key": key}).Count(&n).Error)
	return n
}
