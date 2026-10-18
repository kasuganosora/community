package middleware

import (
	"bytes"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	apperrors "github.com/kasuganosora/community/errors"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/secretredact"
	"github.com/stretchr/testify/require"
)

const leakedToken = "tok_SECRET_0123456789abcdefABCDEF"

// Error bodies and logs must never contain the token the request presented,
// whether it came in the Authorization header, access_token or a cookie, and
// whichever error path rendered it (dev-mode details included).
func TestErrorBodiesAndLogsNeverEchoRequestToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDev := config.C.DevMode
	config.C.DevMode = "true" // the most verbose error rendering
	defer func() { config.C.DevMode = oldDev }()
	logger := log.GetLogObject()
	var logs bytes.Buffer
	oldOut := logger.Out
	logger.SetOutput(&logs)
	defer logger.SetOutput(oldOut)

	router := gin.New()
	router.Use(secretredact.Middleware(), RequestLogMiddleware(), HandleErrorsMiddleware(), apperrors.ErrorHandler())
	echo := func(c *gin.Context) string {
		return "bad token " + c.GetHeader("Authorization") + " " + c.Query("access_token")
	}
	router.GET("/direct", func(c *gin.Context) { c.JSON(401, gin.H{"error": echo(c)}) })
	router.GET("/plain", func(c *gin.Context) { c.String(503, "%s", echo(c)) })
	router.GET("/wrapped", func(c *gin.Context) { _ = c.Error(stderrors.New(echo(c))) })
	router.GET("/app", func(c *gin.Context) { _ = c.Error(apperrors.ErrInternal.WithDetail(echo(c))) })
	router.GET("/ok", func(c *gin.Context) { c.JSON(200, gin.H{"token": "fresh-token-from-login-endpoint"}) })

	for _, path := range []string{"/direct", "/plain", "/wrapped", "/app"} {
		for _, how := range []string{"header", "query", "cookie"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			switch how {
			case "header":
				req.Header.Set("Authorization", "Bearer "+leakedToken)
			case "query":
				req.URL.RawQuery = "access_token=" + leakedToken
			case "cookie":
				req.AddCookie(&http.Cookie{Name: "app-token", Value: leakedToken})
				req.Header.Set("Authorization", "Bearer "+leakedToken)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.GreaterOrEqual(t, w.Code, 400, path)
			require.NotContains(t, w.Body.String(), leakedToken, "%s via %s: body %s", path, how, w.Body.String())
			require.True(t, strings.Contains(w.Body.String(), secretredact.Placeholder) || path == "/app" || path == "/wrapped", "%s via %s: %s", path, how, w.Body.String())
		}
	}
	require.NotContains(t, logs.String(), leakedToken)

	// Successful responses are untouched (login endpoints return tokens).
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("Authorization", "Bearer "+leakedToken)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "fresh-token-from-login-endpoint")
}
