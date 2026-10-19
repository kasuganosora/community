package middleware

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
)

func cookieOriginFixture(t *testing.T) (server *httptest.Server, token string) {
	t.Helper()
	setup := testutil.NewTestSetup(t, testutil.WithModels(&models.User{}, &models.Account{}))
	database.SetTestMode()
	database.SetTestDB(setup.GormDB())
	t.Cleanup(database.ResetTestDB)
	useTestRedis(t)
	oldKey, oldBase, oldAllowed := config.C.EncryptedKey, config.C.AppBaseURL, config.C.WebSocketAllowedOrigins
	config.C.EncryptedKey = "0123456789abcdef0123456789abcdef"
	config.C.AppBaseURL = "https://social.example"
	config.C.WebSocketAllowedOrigins = "https://trusted.example"
	t.Cleanup(func() {
		config.C.EncryptedKey, config.C.AppBaseURL, config.C.WebSocketAllowedOrigins = oldKey, oldBase, oldAllowed
	})
	conn := setup.GormDB()
	user := models.User{Id: 81, UUID: "cswsh-user", UserName: "cswsh", EmailHash: "cswsh-email"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&user).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.Account{Id: 82, UserId: user.Id, Username: user.UserName}).Error)
	token = (models.AuthTokenData{UserId: user.Id, AuthType: models.AuthTokenTypeWeb}).GetTokenStr(config.C.EncryptedKey)
	require.NoError(t, globalRedisClient.Set(context.Background(), tokenCacheKey(token), "valid", tokenCacheTTL).Err())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware())
	// Like production with WEBSOCKET_ALLOWED_ORIGINS unset: the upgrader
	// itself accepts any origin, so the middleware is the only guard.
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }, Subprotocols: []string{"community.v1"}}
	authed := func(c *gin.Context) bool {
		_, ok := c.Get("account")
		return ok
	}
	r.GET("/api/v1/streaming/feed", func(c *gin.Context) {
		if !authed(c) {
			c.AbortWithStatus(401)
			return
		}
		ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err == nil {
			_ = ws.WriteMessage(websocket.TextMessage, []byte("hello"))
			_ = ws.Close()
		}
	})
	r.POST("/api/v1/statuses", func(c *gin.Context) {
		if !authed(c) {
			c.AbortWithStatus(401)
			return
		}
		c.Status(204)
	})
	server = httptest.NewServer(r)
	t.Cleanup(server.Close)
	return server, token
}

func TestCrossSiteWebSocketHijackingIsRefused(t *testing.T) {
	server, token := cookieOriginFixture(t)
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/streaming/feed?stream=user"
	sameOrigin := server.URL                        // httptest host, i.e. the request Host
	cookie := "app-token=" + url.QueryEscape(token) // gin unescapes cookie values
	subprotocol := "community.bearer." + base64.RawURLEncoding.EncodeToString([]byte(token))

	dial := func(origin string, header http.Header, protocols ...string) int {
		t.Helper()
		if header == nil {
			header = http.Header{}
		}
		if origin != "" {
			header.Set("Origin", origin)
		}
		d := websocket.Dialer{Subprotocols: protocols}
		ws, resp, err := d.Dial(wsURL, header)
		if err == nil {
			defer ws.Close()
			_, msg, err := ws.ReadMessage()
			require.NoError(t, err)
			require.Equal(t, "hello", string(msg))
			return 101
		}
		require.NotNil(t, resp, err)
		return resp.StatusCode
	}

	require.Equal(t, 403, dial("https://evil.example", http.Header{"Cookie": {cookie}}), "cross-origin cookie-only WebSocket")
	require.Equal(t, 403, dial("null", http.Header{"Cookie": {cookie}}), "opaque origin with cookie")
	require.Equal(t, 101, dial(sameOrigin, http.Header{"Cookie": {cookie}}), "same-origin cookie")
	require.Equal(t, 101, dial("https://social.example", http.Header{"Cookie": {cookie}}), "APP_BASE_URL origin")
	require.Equal(t, 101, dial("https://trusted.example", http.Header{"Cookie": {cookie}}), "WEBSOCKET_ALLOWED_ORIGINS")
	require.Equal(t, 101, dial("https://elk.zone", nil, "community.v1", subprotocol), "cross-origin bearer subprotocol")
	require.Equal(t, 101, dial("https://elk.zone", http.Header{"Cookie": {cookie}}, "community.v1", subprotocol), "explicit token wins over the cookie")
	require.Equal(t, 101, dial("https://phanpy.social", http.Header{"Authorization": {"Bearer " + token}}), "cross-origin Authorization header")
	require.Equal(t, 101, dial("", nil, "community.v1", subprotocol), "native app without Origin, subprotocol")
	require.Equal(t, 101, dial("", http.Header{"Authorization": {"Bearer " + token}}), "native app without Origin, header")
	require.Equal(t, 401, dial("https://evil.example", nil), "no credentials at all")
}

func TestCookieOnlyStateChangeRequiresSameOrigin(t *testing.T) {
	server, token := cookieOriginFixture(t)
	post := func(origin string, bearer bool) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/statuses", nil)
		require.NoError(t, err)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if bearer {
			req.Header.Set("Authorization", "Bearer "+token)
		} else {
			req.AddCookie(&http.Cookie{Name: AppTokenCookie, Value: url.QueryEscape(token)})
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	require.Equal(t, 403, post("https://evil.example", false))
	require.Equal(t, 204, post(server.URL, false))
	require.Equal(t, 204, post("", false))
	require.Equal(t, 204, post("https://evil.example", true))
}

func TestAppTokenCookieAttributes(t *testing.T) {
	old := config.C.AppBaseURL
	t.Cleanup(func() { config.C.AppBaseURL = old })
	for _, tc := range []struct {
		base, proto string
		secure      bool
	}{
		{"http://localhost:8080", "", false},
		{"http://localhost:8080", "https", true},
		{"https://social.example", "", true},
	} {
		config.C.AppBaseURL = tc.base
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/login", nil)
		if tc.proto != "" {
			c.Request.Header.Set("X-Forwarded-Proto", tc.proto)
		}
		SetAppTokenCookie(c, "value", 3600)
		cookies := w.Result().Cookies()
		require.Len(t, cookies, 1)
		require.True(t, cookies[0].HttpOnly)
		require.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite)
		require.Equal(t, tc.secure, cookies[0].Secure, tc)
	}
}
