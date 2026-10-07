package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExistingWebAndOAuthTokensRespectCurrentModeration(t *testing.T) {
	setup := testutil.NewTestSetup(t, testutil.WithModels(&models.User{}, &models.Account{}))
	database.SetTestMode()
	database.SetTestDB(setup.GormDB())
	t.Cleanup(database.ResetTestDB)
	useTestRedis(t)
	old := config.C.EncryptedKey
	config.C.EncryptedKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.C.EncryptedKey = old })
	conn := setup.GormDB()
	user := models.User{Id: 71, UUID: "moderation-token", UserName: "moderationtoken", EmailHash: "moderation-email"}
	account := models.Account{Id: 72, UserId: user.Id, Username: user.UserName}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&user).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&account).Error)
	for _, kind := range []uint8{models.AuthTokenTypeWeb, models.AuthTokenTypeOauth} {
		token := (models.AuthTokenData{UserId: user.Id, AuthType: kind}).GetTokenStr(config.C.EncryptedKey)
		require.NotEmpty(t, token)
		require.NoError(t, globalRedisClient.Set(context.Background(), tokenCacheKey(token), "valid", tokenCacheTTL).Err())
		request := func() int {
			r := gin.New()
			r.Use(AuthMiddleware())
			r.GET("/protected", func(c *gin.Context) { c.Status(204) })
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			r.ServeHTTP(w, req)
			return w.Code
		}
		require.Equal(t, 204, request())
		require.NoError(t, conn.Model(&user).UpdateColumn("is_ban", true).Error)
		require.Equal(t, 401, request())
		require.NoError(t, conn.Model(&user).UpdateColumn("is_ban", false).Error)
		require.Equal(t, 204, request())
		require.NoError(t, conn.Model(&user).UpdateColumn("status", models.UserStatusRejected).Error)
		require.Equal(t, 401, request())
		require.NoError(t, conn.Model(&user).UpdateColumn("status", models.UserStatusNormal).Error)
		require.Equal(t, 204, request())
		require.NoError(t, conn.Model(&account).UpdateColumn("is_ban", true).Error)
		require.Equal(t, 401, request())
		require.NoError(t, conn.Model(&account).UpdateColumn("is_ban", false).Error)
		require.Equal(t, 204, request())
	}
}
