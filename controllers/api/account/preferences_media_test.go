package account

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The preference handlers run behind the real AuthMiddleware, the way the
// router mounts them: the Account it loads has no User association.
func TestMediaDisplayPreference(t *testing.T) {
	setup := testutil.NewTestSetup(t, testutil.WithModels(&models.User{}, &models.Account{}, &models.UserSetting{}, &models.AuthToken{}))
	database.SetTestMode()
	database.SetTestDB(setup.GormDB())
	t.Cleanup(database.ResetTestDB)
	old := config.C.EncryptedKey
	config.C.EncryptedKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.C.EncryptedKey = old })
	conn := setup.GormDB()

	user := models.User{Id: 701, UUID: "pref-user", UserName: "prefuser", EmailHash: "pref-email"}
	acct := models.Account{Id: 702, UserId: user.Id, Username: user.UserName}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&user).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&acct).Error)
	token := (models.AuthTokenData{UserId: user.Id, AuthType: models.AuthTokenTypeWeb}).GetTokenStr(config.C.EncryptedKey)
	sum := sha256.Sum256([]byte(token))
	require.NoError(t, conn.Create(&models.AuthToken{Token: hex.EncodeToString(sum[:]), Type: models.AuthTokenTypeWeb, UserId: user.Id}).Error)

	r := gin.New()
	r.Use(middleware.AuthMiddleware())
	g := r.Group("/api/v1", middleware.CheckAuthMiddleware())
	g.GET("/preferences", GetPreferences)
	g.PATCH("/preferences", middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteAccounts}), UpdatePreferences)

	do := func(method, body string, authed bool) (int, map[string]any) {
		req := httptest.NewRequest(method, "/api/v1/preferences", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if authed {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)

		return w.Code, out
	}

	code, out := do(http.MethodGet, "", true)
	require.Equal(t, 200, code)
	assert.Equal(t, "default", out["reading:expand:media"])
	for _, mode := range []string{"hide_all", "show_all", "default"} {
		code, out = do(http.MethodPatch, `{"reading:expand:media":"`+mode+`"}`, true)
		require.Equal(t, 200, code, out)
		assert.Equal(t, mode, out["reading:expand:media"])
		var saved models.UserSetting
		require.NoError(t, conn.Where("user_id = ?", user.Id).First(&saved).Error)
		assert.Equal(t, mode, saved.Data["reading_expand_media"])
		code, out = do(http.MethodGet, "", true)
		require.Equal(t, 200, code)
		assert.Equal(t, mode, out["reading:expand:media"], "GET reads back the stored value")
	}
	code, _ = do(http.MethodPatch, `{"reading:expand:media":"sometimes"}`, true)
	assert.Equal(t, 422, code)
	code, _ = do(http.MethodPatch, `{"reading:expand:media":"hide_all"}`, false)
	assert.Equal(t, 401, code)
	code, _ = do(http.MethodGet, "", false)
	assert.Equal(t, 401, code)
}
