package account

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newPersistentRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v1/accounts/login", Login)
	r.POST("/api/v1/accounts/session/refresh", RefreshPersistentSession)
	r.POST("/api/v1/accounts/persistent/revoke", RevokePersistentSession)

	authed := r.Group("/api/v1", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware())
	authed.GET("/accounts/verify_credentials", VerifyCredentials)
	authed.POST("/accounts/logout", Logout)
	authed.Group("", middleware.InternalApiMiddleware()).PUT("/accounts/me/password", ChangePassword)

	return r
}

func performJSONRequest(r http.Handler, method, path string, body any, cookies []*http.Cookie, headers map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	var rawBody []byte
	if body != nil {
		rawBody, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(rawBody))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)

	return w, out
}

func extractAppTokenCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, ck := range w.Result().Cookies() {
		if ck.Name == middleware.AppTokenCookie {
			return ck
		}
	}

	return nil
}

func TestPersistentLogin_RotateAndInvalidateUsedToken(t *testing.T) {
	e := newTwoFactorEnv(t)
	oldKey := config.C.EncryptedKey
	config.C.EncryptedKey = "persistent-test-secret-key-32byte"
	t.Cleanup(func() { config.C.EncryptedKey = oldKey })

	r := newPersistentRouter()

	// 1. Login issues HttpOnly app-token cookie + encrypted persistent_token.
	w1, body1 := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/login", map[string]any{
		"username": "qa",
		"password": "correct-password",
	}, nil, nil)
	require.Equal(t, http.StatusOK, w1.Code)
	cookie1 := extractAppTokenCookie(w1)
	require.NotNil(t, cookie1)
	assert.True(t, cookie1.HttpOnly)
	assert.NotEmpty(t, cookie1.Value)

	pt1, _ := body1["persistent_token"].(string)
	require.True(t, strings.HasPrefix(pt1, models.PersistentTokenPrefix))

	// Persistent credential must NOT be accepted directly as a session bearer or cookie token.
	wDirectBearer, _ := performJSONRequest(r, http.MethodGet, "/api/v1/accounts/verify_credentials", nil, nil, map[string]string{
		"Authorization": "Bearer " + pt1,
	})
	assert.Equal(t, http.StatusUnauthorized, wDirectBearer.Code)

	wDirectCookie, _ := performJSONRequest(r, http.MethodGet, "/api/v1/accounts/verify_credentials", nil, []*http.Cookie{
		{Name: middleware.AppTokenCookie, Value: pt1},
	}, nil)
	assert.Equal(t, http.StatusUnauthorized, wDirectCookie.Code)

	// Cookie1 authenticates daily API requests.
	wVerify1, _ := performJSONRequest(r, http.MethodGet, "/api/v1/accounts/verify_credentials", nil, []*http.Cookie{cookie1}, nil)
	require.Equal(t, http.StatusOK, wVerify1.Code)

	// 2. Simulate cookie loss/expiry: exchange pt1 for a fresh app-token cookie + rotated pt2.
	wRefresh, bodyRefresh := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{
		"persistent_token": pt1,
	}, nil, nil)
	require.Equal(t, http.StatusOK, wRefresh.Code)
	cookie2 := extractAppTokenCookie(wRefresh)
	require.NotNil(t, cookie2)
	assert.NotEmpty(t, cookie2.Value)
	assert.NotEqual(t, cookie1.Value, cookie2.Value)

	pt2, _ := bodyRefresh["persistent_token"].(string)
	require.True(t, strings.HasPrefix(pt2, models.PersistentTokenPrefix))
	assert.NotEqual(t, pt1, pt2)

	// Old cookie1 was invalidated during rotation; new cookie2 works.
	wOldCookie, _ := performJSONRequest(r, http.MethodGet, "/api/v1/accounts/verify_credentials", nil, []*http.Cookie{cookie1}, nil)
	assert.Equal(t, http.StatusUnauthorized, wOldCookie.Code)

	wNewCookie, _ := performJSONRequest(r, http.MethodGet, "/api/v1/accounts/verify_credentials", nil, []*http.Cookie{cookie2}, nil)
	require.Equal(t, http.StatusOK, wNewCookie.Code)

	// 3. Replaying already-used pt1 fails with 401 AND revokes compromised device family (pt2 + cookie2).
	wReplay, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{
		"persistent_token": pt1,
	}, nil, nil)
	assert.Equal(t, http.StatusUnauthorized, wReplay.Code)

	wCompromisedCookie, _ := performJSONRequest(r, http.MethodGet, "/api/v1/accounts/verify_credentials", nil, []*http.Cookie{cookie2}, nil)
	assert.Equal(t, http.StatusUnauthorized, wCompromisedCookie.Code)

	wCompromisedPT2, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{
		"persistent_token": pt2,
	}, nil, nil)
	assert.Equal(t, http.StatusUnauthorized, wCompromisedPT2.Code)

	var persistRows []models.AuthToken
	require.NoError(t, e.conn.Where("type = ?", models.AuthTokenTypePersistent).Find(&persistRows).Error)
	require.Len(t, persistRows, 2)
}

func TestPersistentLogin_MultiDeviceAndPasswordChangeIsolation(t *testing.T) {
	e := newTwoFactorEnv(t)
	oldKey := config.C.EncryptedKey
	config.C.EncryptedKey = "multi-device-secret-key-32-bytes!"
	t.Cleanup(func() { config.C.EncryptedKey = oldKey })

	r := newPersistentRouter()

	// Device A and Device B log into the same account independently.
	wA, bodyA := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/login", map[string]any{
		"username": "qa", "password": "correct-password",
	}, nil, nil)
	require.Equal(t, http.StatusOK, wA.Code)
	cookieA := extractAppTokenCookie(wA)
	require.NotNil(t, cookieA)
	ptA1 := bodyA["persistent_token"].(string)

	wB, bodyB := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/login", map[string]any{
		"username": "qa", "password": "correct-password",
	}, nil, nil)
	require.Equal(t, http.StatusOK, wB.Code)
	cookieB := extractAppTokenCookie(wB)
	ptB1 := bodyB["persistent_token"].(string)
	assert.NotEqual(t, ptA1, ptB1)

	// Device A rotates its token -> Device B remains valid.
	wARefresh, bodyARefresh := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{
		"persistent_token": ptA1,
	}, nil, nil)
	require.Equal(t, http.StatusOK, wARefresh.Code)
	cookieA2 := extractAppTokenCookie(wARefresh)
	ptA2 := bodyARefresh["persistent_token"].(string)

	wBCheck, _ := performJSONRequest(r, http.MethodGet, "/api/v1/accounts/verify_credentials", nil, []*http.Cookie{cookieB}, nil)
	require.Equal(t, http.StatusOK, wBCheck.Code)

	// Device A changes password -> Device B's persistent credential still works.
	pwdReq := httptest.NewRequest(http.MethodPut, "/api/v1/accounts/me/password", strings.NewReader("old_password=correct-password&new_password=updated-password-123"))
	pwdReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	pwdReq.AddCookie(cookieA2)
	wPwd := httptest.NewRecorder()
	r.ServeHTTP(wPwd, pwdReq)
	require.Equal(t, http.StatusOK, wPwd.Code)

	wBRefresh, bodyBRefresh := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{
		"persistent_token": ptB1,
	}, nil, nil)
	require.Equal(t, http.StatusOK, wBRefresh.Code)
	cookieB2 := extractAppTokenCookie(wBRefresh)
	ptB2 := bodyBRefresh["persistent_token"].(string)
	assert.NotEmpty(t, ptB2)

	// Device A logs out -> Device A's cookie & ptA2 are revoked, while Device B stays logged in!
	wALogout, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/logout", nil, []*http.Cookie{cookieA2}, nil)
	require.Equal(t, http.StatusOK, wALogout.Code)

	wAAfterLogout, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{
		"persistent_token": ptA2,
	}, nil, nil)
	assert.Equal(t, http.StatusUnauthorized, wAAfterLogout.Code)

	wBAfterALogout, _ := performJSONRequest(r, http.MethodGet, "/api/v1/accounts/verify_credentials", nil, []*http.Cookie{cookieB2}, nil)
	require.Equal(t, http.StatusOK, wBAfterALogout.Code)
	_ = e
}

func TestPersistentLogin_MultiAccountSwitchAndNonActiveRevoke(t *testing.T) {
	e := newTwoFactorEnv(t)
	oldKey := config.C.EncryptedKey
	config.C.EncryptedKey = "multi-account-secret-key-32bytes"
	t.Cleanup(func() { config.C.EncryptedKey = oldKey })

	// Create a second user Bob on the same server.
	bobUser := &models.User{Id: 2, UserName: "bob", EmailHash: "bob-hash", Status: models.UserStatusNormal}
	require.NoError(t, e.conn.Create(bobUser).Error)
	bobAcc := &models.Account{Id: 2, UserId: bobUser.Id, Username: "bob"}
	require.NoError(t, e.conn.Create(bobAcc).Error)
	bobPwd, err := models.EncryptPassword("bob-password", "salt2")
	require.NoError(t, err)
	require.NoError(t, e.conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.UserSecretData{UserId: bobUser.Id, Password: bobPwd, Salt: "salt2"}).Error)

	r := newPersistentRouter()

	// 1. Sign in as Alice (qa).
	wAlice, bodyAlice := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/login", map[string]any{
		"username": "qa", "password": "correct-password",
	}, nil, nil)
	require.Equal(t, http.StatusOK, wAlice.Code)
	cookieAlice1 := extractAppTokenCookie(wAlice)
	ptAlice1 := bodyAlice["persistent_token"].(string)

	// 2. Add second account Bob in the same browser (sending Alice's cookie on the login request).
	wBob, bodyBob := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/login", map[string]any{
		"username": "bob", "password": "bob-password",
	}, []*http.Cookie{cookieAlice1}, nil)
	require.Equal(t, http.StatusOK, wBob.Code)
	cookieBob1 := extractAppTokenCookie(wBob)
	ptBob1 := bodyBob["persistent_token"].(string)

	// Alice's old cookie was cleanly invalidated, while Alice's persistent credential remains usable!
	wOldAliceCookie, _ := performJSONRequest(r, http.MethodGet, "/api/v1/accounts/verify_credentials", nil, []*http.Cookie{cookieAlice1}, nil)
	assert.Equal(t, http.StatusUnauthorized, wOldAliceCookie.Code)

	// 3. Switch back from Bob to Alice using Alice's ptAlice1 (sending Bob's active cookie).
	wSwitchAlice, bodySwitchAlice := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{
		"persistent_token": ptAlice1,
	}, []*http.Cookie{cookieBob1}, nil)
	require.Equal(t, http.StatusOK, wSwitchAlice.Code)
	cookieAlice2 := extractAppTokenCookie(wSwitchAlice)
	ptAlice2 := bodySwitchAlice["persistent_token"].(string)
	assert.NotEqual(t, ptAlice1, ptAlice2)

	// Verify current cookie now belongs to Alice ("qa").
	wVerifyAlice, verifyBody := performJSONRequest(r, http.MethodGet, "/api/v1/accounts/verify_credentials", nil, []*http.Cookie{cookieAlice2}, nil)
	require.Equal(t, http.StatusOK, wVerifyAlice.Code)
	assert.Equal(t, "qa", verifyBody["username"])

	// 4. Remove non-active Bob from account manager (clear_cookie: false) without wiping Alice's cookie.
	wRevokeBob, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/persistent/revoke", map[string]any{
		"persistent_token": ptBob1,
		"clear_cookie":     false,
	}, []*http.Cookie{cookieAlice2}, nil)
	require.Equal(t, http.StatusOK, wRevokeBob.Code)
	assert.Nil(t, extractAppTokenCookie(wRevokeBob), "non-active account revocation must not clear active account cookie")

	// Bob's persistent credential is now revoked, while Alice's cookieAlice2 still works!
	wBobRefresh, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{
		"persistent_token": ptBob1,
	}, nil, nil)
	assert.Equal(t, http.StatusUnauthorized, wBobRefresh.Code)

	wAliceStillOK, _ := performJSONRequest(r, http.MethodGet, "/api/v1/accounts/verify_credentials", nil, []*http.Cookie{cookieAlice2}, nil)
	require.Equal(t, http.StatusOK, wAliceStillOK.Code)

	// 5. Cross-origin CSRF attempt against refresh/revoke is blocked with 403 without consuming ptAlice2.
	wCSRF, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{
		"persistent_token": ptAlice2,
	}, nil, map[string]string{"Origin": "https://attacker.example"})
	assert.Equal(t, http.StatusForbidden, wCSRF.Code)

	wAliceRotateAfterCSRF, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{
		"persistent_token": ptAlice2,
	}, nil, nil)
	assert.Equal(t, http.StatusOK, wAliceRotateAfterCSRF.Code)
}
