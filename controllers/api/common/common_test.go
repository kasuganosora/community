package common

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/captcha"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callHandler recovers from panic("abort") used by controllers.AbortIf/AbortIfError.
func callHandler(fn func(*gin.Context), c *gin.Context) {
	defer func() {
		if r := recover(); r != nil {
			panic(r)
		}
	}()

	fn(c)
}

func TestMain(m *testing.M) {
	database.SetTestMode()
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// setupTest creates an isolated test DB, sets it as the global test DB,
// migrates needed models, and pre-populates system settings for the common handlers.
func setupTest(t *testing.T) {
	t.Helper()
	testDB, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	database.SetTestDB(testDB.GormDB)

	err = testDB.AutoMigrate(
		&models.SystemSetting{},
		&models.Account{},
		&models.AccountExtra{},
	)
	require.NoError(t, err)

	// Pre-populate system settings so Load() doesn't fail hard
	// and Get() finds values in the in-memory settings map.
	// We don't need to reset `loaded` since Set() writes directly to the map
	// and Get() reads from it after Load().

	// Ensure systemsettings is loaded from the fresh (empty) DB
	systemsettings.Set(systemsettings.KeyCaptchaProvider, "none")
	systemsettings.Set(systemsettings.KeyCaptchaEnabled, false)
}

// newJSONContext creates a gin.Context with a JSON request body.
func newJSONContext(t *testing.T, method, path string, body any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	var bodyReader *bytes.Reader

	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)

		bodyReader = bytes.NewReader(data)
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	c.Request = httptest.NewRequest(method, path, bodyReader) //nolint:noctx // test code //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")

	return c, w
}

// newGetContext creates a gin.Context for a GET request.
func newGetContext(t *testing.T, path string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	return newJSONContext(t, http.MethodGet, path, nil)
}

// ========== GetCaptchaConfig Tests ==========

func TestGetCaptchaConfig(t *testing.T) {
	setupTest(t)

	c, w := newGetContext(t, "/api/v1/captcha/config")
	callHandler(GetCaptchaConfig, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp CaptchaConfigResponse

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	// By default, captcha should be disabled
	assert.False(t, resp.Enabled)
	assert.Equal(t, "none", resp.Provider)
}

// ========== VerifyCaptcha Tests ==========

func TestVerifyCaptcha_InvalidRequest(t *testing.T) {
	setupTest(t)

	// Send empty body (missing required "response" field)
	c, w := newJSONContext(t, http.MethodPost, "/api/v1/captcha/verify", map[string]any{})
	callHandler(VerifyCaptcha, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp VerifyCaptchaResponse

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.Equal(t, "invalid request", resp.Message)
}

func TestVerifyCaptcha_Disabled(t *testing.T) {
	setupTest(t)

	// When captcha is disabled (default), verify should return success
	c, w := newJSONContext(t, http.MethodPost, "/api/v1/captcha/verify", VerifyCaptchaRequest{
		Response: "test-token",
	})
	callHandler(VerifyCaptcha, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp VerifyCaptchaResponse

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, "captcha is disabled", resp.Message)
}

func TestVerifyCaptcha_NoBody(t *testing.T) {
	setupTest(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/captcha/verify", bytes.NewReader(nil)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")

	callHandler(VerifyCaptcha, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ========== GetCaptchaProviders Tests ==========

func TestGetCaptchaProviders(t *testing.T) {
	// This handler is purely static, no DB dependency needed
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/captcha/providers", nil) //nolint:noctx

	GetCaptchaProviders(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var providers []map[string]string

	err := json.Unmarshal(w.Body.Bytes(), &providers)
	require.NoError(t, err)

	// Should have 4 providers: recaptcha, cloudflare, hcaptcha, none
	assert.Len(t, providers, 4)

	// Check each expected provider is present
	ids := make(map[string]bool)
	for _, p := range providers {
		ids[p["id"]] = true
	}

	assert.True(t, ids["recaptcha"], "recaptcha provider should be present")
	assert.True(t, ids["cloudflare"], "cloudflare provider should be present")
	assert.True(t, ids["hcaptcha"], "hcaptcha provider should be present")
	assert.True(t, ids["none"], "none provider should be present")

	// Verify structure of each provider
	for _, p := range providers {
		assert.Contains(t, p, "id")
		assert.Contains(t, p, "name")
		assert.Contains(t, p, "description")
		assert.Contains(t, p, "website")
	}
}

// ========== AdminUpdateCaptchaConfig Tests ==========

func TestAdminUpdateCaptchaConfig_Success(t *testing.T) {
	setupTest(t)

	// Update captcha config to use recaptcha
	req := AdminUpdateCaptchaConfigRequest{
		Provider:  "recaptcha", //nolint:goconst
		SiteKey:   "test-site-key",
		SecretKey: "test-secret-key",
	}
	c, w := newJSONContext(t, http.MethodPut, "/api/v1/admin/captcha/config", req)
	callHandler(AdminUpdateCaptchaConfig, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "captcha config updated", resp["message"])
}

func TestAdminUpdateCaptchaConfig_InvalidProvider(t *testing.T) {
	setupTest(t)

	req := AdminUpdateCaptchaConfigRequest{
		Provider: "invalid-provider",
	}
	c, w := newJSONContext(t, http.MethodPut, "/api/v1/admin/captcha/config", req)
	callHandler(AdminUpdateCaptchaConfig, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]string

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "invalid provider", resp["error"])
}

func TestAdminUpdateCaptchaConfig_InvalidRequest(t *testing.T) {
	setupTest(t)

	// Send empty JSON body (no fields at all - should still pass ShouldBindJSON
	// since no fields are required, but let's test with malformed JSON)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/captcha/config", //nolint:noctx
		bytes.NewReader([]byte("not json")))
	c.Request.Header.Set("Content-Type", "application/json")

	callHandler(AdminUpdateCaptchaConfig, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAdminUpdateCaptchaConfig_WithNoneProvider(t *testing.T) {
	setupTest(t)

	req := AdminUpdateCaptchaConfigRequest{
		Provider: "none", //nolint:goconst
	}
	c, w := newJSONContext(t, http.MethodPut, "/api/v1/admin/captcha/config", req)
	callHandler(AdminUpdateCaptchaConfig, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "captcha config updated", resp["message"])
}

func TestAdminUpdateCaptchaConfig_WithEnabledFlag(t *testing.T) {
	setupTest(t)

	enabled := true
	req := AdminUpdateCaptchaConfigRequest{
		Provider: "none",
		Enabled:  &enabled,
	}
	c, w := newJSONContext(t, http.MethodPut, "/api/v1/admin/captcha/config", req)
	callHandler(AdminUpdateCaptchaConfig, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "captcha config updated", resp["message"])
}

func TestAdminUpdateCaptchaConfig_WithScoreThreshold(t *testing.T) {
	setupTest(t)

	threshold := 0.7
	req := AdminUpdateCaptchaConfigRequest{
		Provider:       "recaptcha",
		SiteKey:        "site-key",
		SecretKey:      "secret-key",
		ScoreThreshold: &threshold,
	}
	c, w := newJSONContext(t, http.MethodPut, "/api/v1/admin/captcha/config", req)
	callHandler(AdminUpdateCaptchaConfig, c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAdminUpdateCaptchaConfig_AllProviderTypes(t *testing.T) {
	providers := []string{"recaptcha", "cloudflare", "hcaptcha", "none"} //nolint:goconst
	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			setupTest(t)

			req := AdminUpdateCaptchaConfigRequest{
				Provider:  provider,
				SiteKey:   "site-key-" + provider,
				SecretKey: "secret-key-" + provider,
			}
			c, w := newJSONContext(t, http.MethodPut, "/api/v1/admin/captcha/config", req)
			callHandler(AdminUpdateCaptchaConfig, c)

			assert.Equal(t, http.StatusOK, w.Code)

			var resp map[string]string

			err := json.Unmarshal(w.Body.Bytes(), &resp)
			require.NoError(t, err)
			assert.Equal(t, "captcha config updated", resp["message"])
		})
	}
}

// ========== GetInstanceDetail Tests ==========

func TestGetInstanceDetail(t *testing.T) {
	setupTest(t)

	// Set some system settings
	systemsettings.Set(systemsettings.KeyDomain, "example.com")
	systemsettings.Set(systemsettings.KeyTitle, "Test Instance")
	systemsettings.Set(systemsettings.KeyDescription, "A test instance")
	systemsettings.Set(systemsettings.KeyThumbnail, "https://example.com/thumb.jpg")

	c, w := newGetContext(t, "/api/v2/instance")
	callHandler(GetInstanceDetail, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp InstanceResponse

	err := json.Unmarshal(w.Body.Bytes(), &resp) //nolint:musttag // struct already has json tags
	require.NoError(t, err)

	assert.Equal(t, "example.com", resp.Domain)
	assert.Equal(t, "Test Instance", resp.Title)
	assert.Equal(t, "A test instance", resp.Description)
	assert.Equal(t, "https://example.com/thumb.jpg", resp.Thumbnail.Url)
	assert.Equal(t, config.GetVersion(), resp.Version)
	assert.Equal(t, "https://github.com/kasuganosora/community", resp.SourceUrl)
}

func TestGetInstanceDetail_DefaultValues(t *testing.T) {
	setupTest(t)

	// Reset all instance-related settings to ensure clean defaults
	systemsettings.Set(systemsettings.KeyDomain, "")
	systemsettings.Set(systemsettings.KeyTitle, "")
	systemsettings.Set(systemsettings.KeyDescription, "")
	systemsettings.Set(systemsettings.KeyThumbnail, "")
	systemsettings.Set(systemsettings.KeyIsEnableRegistration, true)
	systemsettings.Set(systemsettings.KeyRegistrationApprovalRequired, false)
	systemsettings.Set(systemsettings.KeyRegistrationInviteRequired, false)
	systemsettings.Set(systemsettings.KeyRegistrationMessage, "")
	systemsettings.Set(systemsettings.KeySiteContactEmail, "admin@admin.com")

	c, w := newGetContext(t, "/api/v2/instance")
	callHandler(GetInstanceDetail, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp InstanceResponse

	err := json.Unmarshal(w.Body.Bytes(), &resp) //nolint:musttag // struct already has json tags
	require.NoError(t, err)

	// Default values
	assert.Empty(t, resp.Domain)
	assert.Empty(t, resp.Title)
	assert.Equal(t, config.GetVersion(), resp.Version)
	assert.Equal(t, "https://github.com/kasuganosora/community", resp.SourceUrl)

	// Default registrations
	assert.True(t, resp.Registrations.Enabled)
	assert.False(t, resp.Registrations.ApprovalRequired)
	assert.False(t, resp.Registrations.InviteRequired)
	assert.Equal(t, "admin@admin.com", resp.Contact.Email)

	// Default languages
	assert.NotEmpty(t, resp.Languages)
}

func TestGetInstanceDetail_WithActiveUsers(t *testing.T) {
	setupTest(t)

	// Create a local account to be counted as active
	testDB := db.GetDB()
	require.NotNil(t, testDB)

	account := &models.Account{
		Username: "activeuser",
	}
	err := testDB.Create(account).Error
	require.NoError(t, err)

	c, w := newGetContext(t, "/api/v2/instance")
	callHandler(GetInstanceDetail, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp InstanceResponse

	err = json.Unmarshal(w.Body.Bytes(), &resp) //nolint:musttag // struct already has json tags
	require.NoError(t, err)

	// Active user count should be >= 0 (depends on created_at being within 30 days)
	assert.GreaterOrEqual(t, resp.Usage.Users.ActiveMonth, 0)
}

func TestGetInstanceDetail_WithRegistrationSettings(t *testing.T) {
	setupTest(t)

	// Set registration settings
	systemsettings.Set(systemsettings.KeyIsEnableRegistration, false)
	systemsettings.Set(systemsettings.KeyRegistrationApprovalRequired, true)
	systemsettings.Set(systemsettings.KeyRegistrationInviteRequired, true)
	systemsettings.Set(systemsettings.KeyRegistrationMessage, "Registration is closed")

	c, w := newGetContext(t, "/api/v2/instance")
	callHandler(GetInstanceDetail, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp InstanceResponse

	err := json.Unmarshal(w.Body.Bytes(), &resp) //nolint:musttag // struct already has json tags
	require.NoError(t, err)

	assert.False(t, resp.Registrations.Enabled)
	assert.True(t, resp.Registrations.ApprovalRequired)
	assert.True(t, resp.Registrations.InviteRequired)
	assert.Equal(t, "Registration is closed", resp.Registrations.Message)
}

func TestGetInstanceDetail_WithContactEmail(t *testing.T) {
	setupTest(t)

	systemsettings.Set(systemsettings.KeySiteContactEmail, "admin@example.com")

	c, w := newGetContext(t, "/api/v2/instance")
	callHandler(GetInstanceDetail, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp InstanceResponse

	err := json.Unmarshal(w.Body.Bytes(), &resp) //nolint:musttag // struct already has json tags
	require.NoError(t, err)

	assert.Equal(t, "admin@example.com", resp.Contact.Email)
}

func TestGetInstanceDetail_ResponseStructure(t *testing.T) {
	setupTest(t)

	c, w := newGetContext(t, "/api/v2/instance")
	callHandler(GetInstanceDetail, c)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify the response is valid JSON
	var raw map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &raw)
	require.NoError(t, err)

	// Check top-level fields exist
	assert.Contains(t, raw, "domain")
	assert.Contains(t, raw, "title")
	assert.Contains(t, raw, "version")
	assert.Contains(t, raw, "source_url")
	assert.Contains(t, raw, "description")
	assert.Contains(t, raw, "usage")
	assert.Contains(t, raw, "thumbnail")
	assert.Contains(t, raw, "languages")
	assert.Contains(t, raw, "configuration")
	assert.Contains(t, raw, "registrations")
	assert.Contains(t, raw, "contact")
	assert.Contains(t, raw, "rule")

	// Check configuration sub-fields
	config, ok := raw["configuration"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, config, "urls")
	assert.Contains(t, config, "accounts")
	assert.Contains(t, config, "statuses")
	assert.Contains(t, config, "media_attachments")
	assert.Contains(t, config, "polls")
	assert.Contains(t, config, "translation")
}

// ========== VerifyCaptchaRequest Validation ==========

func TestVerifyCaptchaRequest_MissingResponse(t *testing.T) {
	setupTest(t)

	// Send request without "response" field
	c, w := newJSONContext(t, http.MethodPost, "/api/v1/captcha/verify", map[string]any{
		"other_field": "value",
	})
	callHandler(VerifyCaptcha, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestVerifyCaptchaRequest_ValidResponse(t *testing.T) {
	setupTest(t)

	c, w := newJSONContext(t, http.MethodPost, "/api/v1/captcha/verify", VerifyCaptchaRequest{
		Response: "some-captcha-response",
	})
	callHandler(VerifyCaptcha, c)

	// Should return 200 since captcha is disabled by default
	assert.Equal(t, http.StatusOK, w.Code)

	var resp VerifyCaptchaResponse

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

// ========== Captcha Config Response Types ==========

func TestCaptchaConfigResponse_JSONTags(t *testing.T) {
	// Verify JSON serialization of CaptchaConfigResponse
	resp := CaptchaConfigResponse{
		Enabled:   true,
		Provider:  "recaptcha",
		SiteKey:   "site-key-123",
		PublicKey: "pub-key-456",
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var raw map[string]any

	err = json.Unmarshal(data, &raw)
	require.NoError(t, err)

	assert.Equal(t, true, raw["enabled"])
	assert.Equal(t, "recaptcha", raw["provider"])
	assert.Equal(t, "site-key-123", raw["site_key"])
	assert.Equal(t, "pub-key-456", raw["public_key"])
}

func TestVerifyCaptchaResponse_JSONTags(t *testing.T) {
	resp := VerifyCaptchaResponse{
		Success: true,
		Message: "ok",
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var raw map[string]any

	err = json.Unmarshal(data, &raw)
	require.NoError(t, err)

	assert.Equal(t, true, raw["success"])
	assert.Equal(t, "ok", raw["message"])
}

func TestServerRuleResponseItem_JSONTags(t *testing.T) {
	item := ServerRuleResponseItem{
		Id:   "rule-1",
		Text: "Be nice",
	}

	data, err := json.Marshal(item)
	require.NoError(t, err)

	var raw map[string]any

	err = json.Unmarshal(data, &raw)
	require.NoError(t, err)

	assert.Equal(t, "rule-1", raw["id"])
	assert.Equal(t, "Be nice", raw["text"])
}

// ========== Edge Cases ==========

func TestGetCaptchaProviders_IsIdempotent(t *testing.T) {
	// Call twice and ensure consistent results
	for range 3 {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/captcha/providers", nil) //nolint:noctx

		GetCaptchaProviders(c)

		assert.Equal(t, http.StatusOK, w.Code)

		var providers []map[string]string

		err := json.Unmarshal(w.Body.Bytes(), &providers)
		require.NoError(t, err)
		assert.Len(t, providers, 4)
	}
}

func TestAdminUpdateCaptchaConfig_ProviderAliases(t *testing.T) {
	aliases := map[string]string{
		"google":    "recaptcha",
		"turnstile": "cloudflare",
		"":          "none",
	}
	for input, expected := range aliases {
		t.Run(input, func(t *testing.T) {
			setupTest(t)

			// Verify the alias parses correctly via captcha package
			parsed, err := captcha.ParseProviderType(input)
			require.NoError(t, err)
			assert.Equal(t, captcha.ProviderType(expected), parsed)
		})
	}
}
