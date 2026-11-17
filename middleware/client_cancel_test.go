package middleware

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	apperrors "github.com/kasuganosora/community/errors"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestClientDisconnectDuringAuthAndTimelineDoesNotLogUnhandledOrWipeCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setup := testutil.NewTestSetup(t, testutil.WithModels(&models.User{}, &models.Account{}, &models.AuthToken{}))
	database.SetTestMode()
	database.SetTestDB(setup.GormDB())
	t.Cleanup(database.ResetTestDB)
	useTestRedis(t)

	oldKey := config.C.EncryptedKey
	config.C.EncryptedKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.C.EncryptedKey = oldKey })

	conn := setup.GormDB()
	user := models.User{Id: 91, UUID: "cancel-user", UserName: "canceluser", EmailHash: "cancel-email"}
	account := models.Account{Id: 92, UserId: user.Id, Username: user.UserName}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&user).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&account).Error)

	token := (models.AuthTokenData{UserId: user.Id, AuthType: models.AuthTokenTypeWeb}).GetTokenStr(config.C.EncryptedKey)
	require.NotEmpty(t, token)
	require.NoError(t, conn.Create(&models.AuthToken{UserId: user.Id, Token: token, Type: models.AuthTokenTypeWeb}).Error)

	logger := log.GetLogObject()
	var logs bytes.Buffer
	oldOut := logger.Out
	oldLevel := logger.GetLevel()
	logger.SetOutput(&logs)
	logger.SetLevel(logrus.InfoLevel)
	t.Cleanup(func() {
		logger.SetOutput(oldOut)
		logger.SetLevel(oldLevel)
	})

	t.Run("disconnect_before_token_and_account_queries_keeps_cookie_and_logs_nothing", func(t *testing.T) {
		logs.Reset()
		r := gin.New()
		r.Use(TimeoutMiddleware(5*time.Second), HandleErrorsMiddleware(), apperrors.ErrorHandler(), AuthMiddleware())
		handlerCalled := false
		r.GET("/api/v1/timelines/home", func(c *gin.Context) {
			handlerCalled = true
			c.Status(http.StatusOK)
		})

		// First case: uncached token lookup sees already-canceled client context.
		ctx1, cancel1 := context.WithCancel(context.Background())
		cancel1()
		req1 := httptest.NewRequest(http.MethodGet, "/api/v1/timelines/home", nil).WithContext(ctx1)
		req1.AddCookie(&http.Cookie{Name: "app-token", Value: token})
		req1.Header.Set("Authorization", "Bearer "+token)
		w1 := httptest.NewRecorder()
		r.ServeHTTP(w1, req1)
		require.False(t, handlerCalled)
		require.Equal(t, StatusClientClosedRequest, w1.Code)
		require.Empty(t, w1.Header().Get("Set-Cookie"), "client disconnect must not wipe session cookie")

		// Second case: cached token succeeds, then client disconnects before GetAccountBase query.
		require.NoError(t, globalRedisClient.Set(context.Background(), tokenCacheKey(token), "valid", tokenCacheTTL).Err())
		var cancelDuringAccount context.CancelFunc
		cbName := "test:cancel_on_accounts_select"
		require.NoError(t, conn.Callback().Query().Before("gorm:query").Register(cbName, func(tx *gorm.DB) {
			if tx.Statement != nil && tx.Statement.Table == "accounts" && cancelDuringAccount != nil {
				cancelDuringAccount()
			}
		}))
		defer func() { _ = conn.Callback().Query().Remove(cbName) }()

		ctx2, cancel2 := context.WithCancel(context.Background())
		cancelDuringAccount = cancel2
		req2 := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/lookup", nil).WithContext(ctx2)
		req2.Header.Set("Authorization", "Bearer "+token)

		rLookup := gin.New()
		rLookup.Use(TimeoutMiddleware(5*time.Second), HandleErrorsMiddleware(), apperrors.ErrorHandler(), AuthMiddleware())
		rLookup.GET("/api/v1/accounts/lookup", func(c *gin.Context) {
			handlerCalled = true
			c.Status(http.StatusOK)
		})
		w2 := httptest.NewRecorder()
		rLookup.ServeHTTP(w2, req2)
		require.False(t, handlerCalled)
		require.Equal(t, StatusClientClosedRequest, w2.Code)
		require.Empty(t, w2.Header().Get("Set-Cookie"))

		out := logs.String()
		require.NotContains(t, out, "Unhandled error")
		require.NotContains(t, out, "get account error")
		require.NotContains(t, out, "Request error")
	})

	t.Run("mid_request_handler_disconnect_under_timeout_middleware_logs_no_unhandled_error", func(t *testing.T) {
		logs.Reset()
		r := gin.New()
		r.Use(TimeoutMiddleware(5*time.Second), HandleErrorsMiddleware(), apperrors.ErrorHandler())
		r.GET("/api/v1/timelines/public", func(c *gin.Context) {
			<-c.Request.Context().Done()
			_ = c.Error(c.Request.Context().Err())
		})

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(15 * time.Millisecond)
			cancel()
		}()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/timelines/public", nil).WithContext(ctx)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		require.Equal(t, StatusClientClosedRequest, w.Code)
		out := logs.String()
		require.NotContains(t, out, "Unhandled error")
		require.NotContains(t, out, "Request error")
	})
}
