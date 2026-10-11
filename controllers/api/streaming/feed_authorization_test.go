package streaming

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func feedAuthFixture(t *testing.T, conn *gorm.DB, viewer *models.Account) func(*gin.Context) {
	t.Helper()
	require.NoError(t, conn.AutoMigrate(&models.User{}, &models.AuthToken{}))
	uid := uint64(7001)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Account").Create(&models.User{Id: uid, UserName: "stream-auth", Status: models.UserStatusNormal}).Error)
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", viewer.Id).UpdateColumn("user_id", uid).Error)
	viewer.UserId = uid
	sum := sha256.Sum256([]byte("stream-private-fixture"))
	require.NoError(t, conn.Create(&models.AuthToken{Token: hex.EncodeToString(sum[:]), UserId: uid}).Error)
	return func(c *gin.Context) {
		c.Request.Header.Set("Authorization", "Bearer stream-private-fixture")
		c.Set("token_data", &models.AuthTokenData{UserId: uid})
		c.Set("account", viewer)
	}
}

func feedAuthDB(t *testing.T) (*gorm.DB, *models.Account, func(*gin.Context)) {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth.db")), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	old := database.DB
	database.DB = conn
	t.Cleanup(func() { database.DB = old; raw, _ := conn.DB(); raw.Close() })
	require.NoError(t, conn.AutoMigrate(&models.Account{}))
	viewer := &models.Account{Id: 2, Username: "viewer"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Extra").Create(viewer).Error)
	return conn, viewer, feedAuthFixture(t, conn, viewer)
}

func TestFeedCurrentAuthorizationRevocationAndModeration(t *testing.T) {
	cases := []struct{ name, query string }{
		{"deleted token", "DELETE FROM auth_tokens"},
		{"revoked token", "UPDATE auth_tokens SET revoke=CURRENT_TIMESTAMP"},
		{"expired token", "UPDATE auth_tokens SET expired_at='2000-01-01 00:00:00'"},
		{"banned user", "UPDATE users SET is_ban=1"},
		{"disabled user", "UPDATE users SET status=1"},
		{"deleted user", "UPDATE users SET deleted_at=CURRENT_TIMESTAMP"},
		{"banned account", "UPDATE accounts SET is_ban=1"},
		{"deleted account", "UPDATE accounts SET deleted_at=CURRENT_TIMESTAMP"},
		{"database failure", "DROP TABLE auth_tokens"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, viewer, auth := feedAuthDB(t)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/feed", nil)
			auth(c)
			_, err := currentFeedStreamingAccount(c, viewer)
			require.NoError(t, err)
			require.NoError(t, conn.Exec(tc.query).Error)
			_, err = currentFeedStreamingAccount(c, viewer)
			require.Error(t, err)
		})
	}
	t.Run("cookie and payload expiry", func(t *testing.T) {
		_, viewer, auth := feedAuthDB(t)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/feed", nil)
		auth(c)
		c.Request.Header.Del("Authorization")
		c.Request.AddCookie(&http.Cookie{Name: "app-token", Value: "stream-private-fixture"})
		_, err := currentFeedStreamingAccount(c, viewer)
		require.NoError(t, err)
		c.Set("token_data", &models.AuthTokenData{UserId: 7001, ExpiredAt: time.Now().Add(-time.Second).Unix()})
		_, err = currentFeedStreamingAccount(c, viewer)
		require.Error(t, err)
	})
}

func TestFeedLiveStreamClosesBeforeRevokedHint(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"revoked token", "UPDATE auth_tokens SET revoke=CURRENT_TIMESTAMP"},
		{"disabled user", "UPDATE users SET status=1"},
		{"database error", "DROP TABLE auth_tokens"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetFeedLifecycle(t)
			conn, viewer, auth := feedAuthDB(t)
			setupRedisForTest(t)
			router := gin.New()
			router.GET("/feed", func(c *gin.Context) { auth(c); FeedStreaming(c) })
			server := httptest.NewServer(router)
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/feed", nil)
			response, err := http.DefaultClient.Do(request)
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, 200, response.StatusCode)
			reader := bufio.NewReader(response.Body)
			for i := 0; i < 3; i++ {
				_, err = reader.ReadString('\n')
				require.NoError(t, err)
			}
			require.NoError(t, conn.Exec(tc.query).Error)
			require.NoError(t, feed.PublishChanged(ctx, &home.HomeTimeline{Account: viewer}, "private-after-revoke", "secret-revision"))
			remaining, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.Empty(t, remaining)
			// No replacement SQL connection or authorization cache was used.
			require.Same(t, conn, db.GetDB())
		})
	}
}

func TestFeedInitialSubscriptionRechecksRevokedAuthentication(t *testing.T) {
	resetFeedLifecycle(t)
	conn, viewer, auth := feedAuthDB(t)
	setupRedisForTest(t)
	require.NoError(t, conn.Exec("DELETE FROM auth_tokens").Error)
	router := gin.New()
	router.GET("/feed", func(c *gin.Context) { auth(c); FeedStreaming(c) })
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/feed", nil))
	require.Equal(t, http.StatusUnauthorized, response.Code)
	require.NotContains(t, response.Body.String(), "feed.changed")
	var n int64
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ? AND feed_last_seen_at IS NOT NULL", viewer.Id).Count(&n).Error)
	require.Zero(t, n, "revoked initial connection must not record activity")
}
