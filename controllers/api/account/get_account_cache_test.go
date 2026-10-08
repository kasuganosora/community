package account

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/cache"
	"github.com/kasuganosora/community/db/database"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The production cache singleton retains its Redis client for the process.
// Keep one test server alive for that same lifetime, including go test -count=N.
var accountCacheRedis struct {
	once   sync.Once
	server *miniredis.Miniredis
	client *redis.Client
	err    error
}

func TestGetAccountCacheHydratesCounters(t *testing.T) {
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
	account := &models.Account{Id: 987654, Username: "cached_profile"}
	require.NoError(t, db.Create(account).Error)
	require.NoError(t, db.Model(&models.AccountExtra{}).Where("account_id = ?", account.Id).Update("followers_count", 7).Error)
	// Round-tripping through the production cache removes Extra due to json:"-".
	key := fmt.Sprintf(cache.KeyAccountByAcct, "987654")
	require.NoError(t, cache.GetCache().Set(context.Background(), key, account, cache.DefaultExpiration))
	request := func() float64 {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/v1/accounts/987654", nil)
		c.Params = gin.Params{{Key: "id", Value: "987654"}}
		GetAccount(c)
		require.Empty(t, c.Errors)
		require.Equal(t, 200, w.Code)
		var response map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		return response["followers_count"].(float64)
	}
	require.Equal(t, float64(7), request())
	require.NoError(t, db.Model(&models.AccountExtra{}).Where("account_id = ?", account.Id).Update("followers_count", 8).Error)
	require.Equal(t, float64(8), request(), "cached profile must reflect accepted follow count changes")
}
