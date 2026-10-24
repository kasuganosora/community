package common

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/kasuganosora/community/captcha"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
)

func TestAdminCaptchaReadPreservesStoredDisabledAndNeverReturnsSecret(t *testing.T) {
	setupTest(t)
	t.Cleanup(func() {
		systemsettings.Set(systemsettings.KeyCaptchaProvider, "none")
		systemsettings.Set(systemsettings.KeyCaptchaEnabled, false)
		systemsettings.Set(systemsettings.KeyCaptchaSiteKey, "")
		systemsettings.Set(systemsettings.KeyCaptchaSecretKey, "")
		systemsettings.Set(systemsettings.KeyCaptchaScoreThreshold, 0.5)
		require.NoError(t, captcha.GetService().Reload())
	})
	for _, name := range []string{"CAPTCHA_PROVIDER", "CAPTCHA_SITE_KEY", "CAPTCHA_SECRET_KEY", "HCAPTCHA_SITE_KEY", "HCAPTCHA_SECRET_KEY"} {
		t.Setenv(name, "")
	}
	systemsettings.Set(systemsettings.KeyCaptchaProvider, "hcaptcha")
	systemsettings.Set(systemsettings.KeyCaptchaEnabled, false)
	systemsettings.Set(systemsettings.KeyCaptchaSiteKey, "saved-public-key")
	systemsettings.Set(systemsettings.KeyCaptchaSecretKey, "private-test-secret")
	require.NoError(t, captcha.GetService().Reload())
	require.False(t, captcha.GetService().IsEnabled(), "explicit disable must not be undone by stored credentials")
	c, w := newGetContext(t, "/api/v1/admin/captcha/config")
	AdminGetCaptchaConfig(c)
	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, false, got["enabled"])
	require.Equal(t, "saved-public-key", got["site_key"])
	require.NotContains(t, w.Body.String(), "private-test-secret")
	require.NotContains(t, got, "secret_key")
}

func TestAdminCaptchaUpdateActuallyConfiguresRuntimeAndPreservesSecret(t *testing.T) {
	setupTest(t)
	t.Cleanup(func() {
		systemsettings.Set(systemsettings.KeyCaptchaProvider, "none")
		systemsettings.Set(systemsettings.KeyCaptchaEnabled, false)
		systemsettings.Set(systemsettings.KeyCaptchaSiteKey, "")
		systemsettings.Set(systemsettings.KeyCaptchaSecretKey, "")
		systemsettings.Set(systemsettings.KeyCaptchaScoreThreshold, 0.5)
		require.NoError(t, captcha.GetService().Reload())
	})
	for _, name := range []string{"CAPTCHA_PROVIDER", "HCAPTCHA_SITE_KEY", "HCAPTCHA_SECRET_KEY"} {
		t.Setenv(name, "")
	}
	systemsettings.Set(systemsettings.KeyCaptchaSecretKey, "original-secret")
	systemsettings.Set(systemsettings.KeyCaptchaScoreThreshold, 0.8)
	c, w := newJSONContext(t, http.MethodPut, "/api/v1/admin/captcha/config", map[string]any{
		"provider": "hcaptcha", "site_key": "new-public-key", "enabled": true,
	})
	AdminUpdateCaptchaConfig(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, captcha.GetService().IsEnabled())
	require.Equal(t, "new-public-key", captcha.GetService().GetSiteKey())
	require.Equal(t, "original-secret", systemsettings.GetString(systemsettings.KeyCaptchaSecretKey, ""))
	require.Equal(t, 0.8, systemsettings.GetFloat64(systemsettings.KeyCaptchaScoreThreshold, 0.5))
	// A saved database key wins over a legacy environment value.
	t.Setenv("HCAPTCHA_SITE_KEY", "deployment-public-key")
	require.NoError(t, captcha.GetService().Reload())
	require.Equal(t, "new-public-key", captcha.GetService().GetSiteKey())
	c, w = newGetContext(t, "/api/v1/admin/captcha/config")
	AdminGetCaptchaConfig(c)
	require.Contains(t, w.Body.String(), `"environment_override":false`)
}
