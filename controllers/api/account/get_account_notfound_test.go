package account

import (
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/redis/go-redis/v9"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetAccountUnknownIDIs404(t *testing.T) {
	db, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&models.Account{}, &models.AccountExtra{}, &models.Emoji{}))
	database.SetTestDB(db)
	t.Cleanup(database.ResetTestDB)
	accountCacheRedis.once.Do(func() {
		accountCacheRedis.server, accountCacheRedis.err = miniredis.Run()
		if accountCacheRedis.err == nil {
			accountCacheRedis.client = redis.NewClient(&redis.Options{Addr: accountCacheRedis.server.Addr()})
		}
	})
	require.NoError(t, accountCacheRedis.err)
	previous := cRedis.GetRedisesForTest()
	cRedis.SetRedisesForTest([]*redis.Client{accountCacheRedis.client})
	t.Cleanup(func() { cRedis.SetRedisesForTest(previous) })

	for _, id := range []string{"9999999", "0", "abc", "-1"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/v1/accounts/"+id, nil)
		c.Params = gin.Params{{Key: "id", Value: id}}
		GetAccount(c)
		require.Equal(t, 404, w.Code, "id %s: %s", id, w.Body.String())
	}

	for name, handler := range map[string]gin.HandlerFunc{"statuses": AccountStatuses, "featured_tags": AccountFeaturedTags} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/v1/accounts/9999999/"+name, nil)
		c.Params = gin.Params{{Key: "id", Value: "9999999"}}
		handler(c)
		require.Equal(t, 404, w.Code, "%s: %s", name, w.Body.String())
	}
}
