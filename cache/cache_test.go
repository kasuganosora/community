package cache

import (
	"context"
	"testing"
	"time"

	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
)

// testCache holds test cache setup
type testCache struct {
	cache       *Cache
	redisClient *testutil.RedisTestClient
}

// setupTestCache creates a Cache instance for testing using the test framework
// Uses real Redis if available, skips test otherwise
func setupTestCache(t *testing.T) *testCache {
	redisClient := testutil.SetupRealRedisTest(t)

	return &testCache{
		cache: &Cache{
			client: redisClient.Client,
			prefix: "test:",
		},
		redisClient: redisClient,
	}
}

func TestCache_SetAndGet(t *testing.T) {
	tc := setupTestCache(t)
	c := tc.cache
	ctx := context.Background()

	type TestData struct {
		Name  string
		Value int
	}

	t.Run("Set and Get struct", func(t *testing.T) {
		data := &TestData{Name: "test", Value: 123}
		err := c.Set(ctx, "testkey", data, time.Minute)
		assert.NoError(t, err)

		var result TestData
		err = c.Get(ctx, "testkey", &result)
		assert.NoError(t, err)
		assert.Equal(t, "test", result.Name)
		assert.Equal(t, 123, result.Value)
	})

	t.Run("Get non-existent key", func(t *testing.T) {
		var result TestData
		err := c.Get(ctx, "nonexistent", &result)
		assert.Error(t, err)
	})
}

func TestCache_SetAndGetString(t *testing.T) {
	tc := setupTestCache(t)
	c := tc.cache
	ctx := context.Background()

	t.Run("Set and Get string", func(t *testing.T) {
		err := c.SetString(ctx, "stringkey", "hello world", time.Minute)
		assert.NoError(t, err)

		result, err := c.GetString(ctx, "stringkey")
		assert.NoError(t, err)
		assert.Equal(t, "hello world", result)
	})
}

func TestCache_Delete(t *testing.T) {
	tc := setupTestCache(t)
	c := tc.cache
	ctx := context.Background()

	t.Run("Delete single key", func(t *testing.T) {
		c.SetString(ctx, "delkey1", "value1", time.Minute)
		c.SetString(ctx, "delkey2", "value2", time.Minute)

		err := c.Delete(ctx, "delkey1")
		assert.NoError(t, err)

		exists, _ := c.Exists(ctx, "delkey1")
		assert.False(t, exists)

		exists, _ = c.Exists(ctx, "delkey2")
		assert.True(t, exists)
	})

	t.Run("Delete multiple keys", func(t *testing.T) {
		c.SetString(ctx, "multidel1", "v1", time.Minute)
		c.SetString(ctx, "multidel2", "v2", time.Minute)
		c.SetString(ctx, "multidel3", "v3", time.Minute)

		err := c.Delete(ctx, "multidel1", "multidel2")
		assert.NoError(t, err)

		exists, _ := c.Exists(ctx, "multidel1")
		assert.False(t, exists)
		exists, _ = c.Exists(ctx, "multidel2")
		assert.False(t, exists)
		exists, _ = c.Exists(ctx, "multidel3")
		assert.True(t, exists)
	})
}

func TestCache_DeleteByPattern(t *testing.T) {
	tc := setupTestCache(t)
	c := tc.cache
	ctx := context.Background()

	t.Run("Delete by pattern", func(t *testing.T) {
		c.SetString(ctx, "pattern:a", "1", time.Minute)
		c.SetString(ctx, "pattern:b", "2", time.Minute)
		c.SetString(ctx, "pattern:c", "3", time.Minute)
		c.SetString(ctx, "other:d", "4", time.Minute)

		err := c.DeleteByPattern(ctx, "pattern:*")
		assert.NoError(t, err)

		exists, _ := c.Exists(ctx, "pattern:a")
		assert.False(t, exists)
		exists, _ = c.Exists(ctx, "pattern:b")
		assert.False(t, exists)
		exists, _ = c.Exists(ctx, "other:d")
		assert.True(t, exists)
	})
}

func TestCache_Exists(t *testing.T) {
	tc := setupTestCache(t)
	c := tc.cache
	ctx := context.Background()

	t.Run("Key exists", func(t *testing.T) {
		c.SetString(ctx, "existskey", "value", time.Minute)
		exists, err := c.Exists(ctx, "existskey")
		assert.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("Key not exists", func(t *testing.T) {
		exists, err := c.Exists(ctx, "notexistskey")
		assert.NoError(t, err)
		assert.False(t, exists)
	})
}

func TestCache_ExpireAndTTL(t *testing.T) {
	tc := setupTestCache(t)
	c := tc.cache
	ctx := context.Background()

	t.Run("Expire and TTL", func(t *testing.T) {
		c.SetString(ctx, "expirekey", "value", 0)

		err := c.Expire(ctx, "expirekey", 10*time.Second)
		assert.NoError(t, err)

		ttl, err := c.TTL(ctx, "expirekey")
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, ttl.Seconds(), float64(9))
		assert.LessOrEqual(t, ttl.Seconds(), float64(10))
	})
}

func TestCache_IncrementDecrement(t *testing.T) {
	tc := setupTestCache(t)
	c := tc.cache
	ctx := context.Background()

	t.Run("Increment", func(t *testing.T) {
		val, err := c.Increment(ctx, "counter")
		assert.NoError(t, err)
		assert.Equal(t, int64(1), val)

		val, err = c.Increment(ctx, "counter")
		assert.NoError(t, err)
		assert.Equal(t, int64(2), val)
	})

	t.Run("IncrementBy", func(t *testing.T) {
		val, err := c.IncrementBy(ctx, "counter2", 5)
		assert.NoError(t, err)
		assert.Equal(t, int64(5), val)

		val, err = c.IncrementBy(ctx, "counter2", 3)
		assert.NoError(t, err)
		assert.Equal(t, int64(8), val)
	})

	t.Run("Decrement", func(t *testing.T) {
		c.IncrementBy(ctx, "counter3", 10)
		val, err := c.Decrement(ctx, "counter3")
		assert.NoError(t, err)
		assert.Equal(t, int64(9), val)
	})
}

func TestCache_Hash(t *testing.T) {
	tc := setupTestCache(t)
	c := tc.cache
	ctx := context.Background()

	t.Run("HSet and HGet", func(t *testing.T) {
		data := map[string]int{"a": 1, "b": 2}
		err := c.HSet(ctx, "hashkey", "field1", data)
		assert.NoError(t, err)

		var result map[string]int
		err = c.HGet(ctx, "hashkey", "field1", &result)
		assert.NoError(t, err)
		assert.Equal(t, 1, result["a"])
		assert.Equal(t, 2, result["b"])
	})

	t.Run("HGetAll", func(t *testing.T) {
		c.client.HSet(ctx, c.prefix+"hashall", "f1", "v1")
		c.client.HSet(ctx, c.prefix+"hashall", "f2", "v2")

		result, err := c.HGetAll(ctx, "hashall")
		assert.NoError(t, err)
		assert.Equal(t, "v1", result["f1"])
		assert.Equal(t, "v2", result["f2"])
	})

	t.Run("HDel", func(t *testing.T) {
		c.client.HSet(ctx, c.prefix+"hashdel", "f1", "v1")
		c.client.HSet(ctx, c.prefix+"hashdel", "f2", "v2")

		err := c.HDel(ctx, "hashdel", "f1")
		assert.NoError(t, err)

		result, _ := c.HGetAll(ctx, "hashdel")
		assert.NotContains(t, result, "f1")
		assert.Contains(t, result, "f2")
	})
}

func TestCache_List(t *testing.T) {
	tc := setupTestCache(t)
	c := tc.cache
	ctx := context.Background()

	t.Run("LPush and LRange", func(t *testing.T) {
		err := c.LPush(ctx, "listkey", "a", "b", "c")
		assert.NoError(t, err)

		result, err := c.LRange(ctx, "listkey", 0, -1)
		assert.NoError(t, err)
		assert.Len(t, result, 3)
		assert.Equal(t, "c", result[0]) // LPush adds to left
	})

	t.Run("RPush", func(t *testing.T) {
		err := c.RPush(ctx, "listkey2", "x", "y", "z")
		assert.NoError(t, err)

		result, err := c.LRange(ctx, "listkey2", 0, -1)
		assert.NoError(t, err)
		assert.Equal(t, "x", result[0]) // RPush adds to right
	})

	t.Run("LLen", func(t *testing.T) {
		c.RPush(ctx, "listlen", "1", "2", "3")
		len, err := c.LLen(ctx, "listlen")
		assert.NoError(t, err)
		assert.Equal(t, int64(3), len)
	})
}

func TestCache_SetOperations(t *testing.T) {
	tc := setupTestCache(t)
	c := tc.cache
	ctx := context.Background()

	t.Run("SAdd and SMembers", func(t *testing.T) {
		err := c.SAdd(ctx, "setkey", "a", "b", "c")
		assert.NoError(t, err)

		members, err := c.SMembers(ctx, "setkey")
		assert.NoError(t, err)
		assert.Len(t, members, 3)
		assert.Contains(t, members, "a")
		assert.Contains(t, members, "b")
		assert.Contains(t, members, "c")
	})

	t.Run("SIsMember", func(t *testing.T) {
		c.SAdd(ctx, "setcheck", "member1", "member2")

		isMember, err := c.SIsMember(ctx, "setcheck", "member1")
		assert.NoError(t, err)
		assert.True(t, isMember)

		isMember, err = c.SIsMember(ctx, "setcheck", "notmember")
		assert.NoError(t, err)
		assert.False(t, isMember)
	})
}

func TestCache_SortedSet(t *testing.T) {
	tc := setupTestCache(t)
	c := tc.cache
	ctx := context.Background()

	t.Run("ZAdd and ZRange", func(t *testing.T) {
		err := c.ZAdd(ctx, "zsetkey", 1.0, "one")
		assert.NoError(t, err)
		c.ZAdd(ctx, "zsetkey", 2.0, "two")
		c.ZAdd(ctx, "zsetkey", 3.0, "three")

		result, err := c.ZRange(ctx, "zsetkey", 0, -1)
		assert.NoError(t, err)
		assert.Equal(t, []string{"one", "two", "three"}, result)
	})

	t.Run("ZRangeByScore", func(t *testing.T) {
		c.ZAdd(ctx, "zsetrange", 1.0, "a")
		c.ZAdd(ctx, "zsetrange", 5.0, "b")
		c.ZAdd(ctx, "zsetrange", 10.0, "c")

		result, err := c.ZRangeByScore(ctx, "zsetrange", "2", "8", 0, 10)
		assert.NoError(t, err)
		assert.Equal(t, []string{"b"}, result)
	})

	t.Run("ZRem", func(t *testing.T) {
		c.ZAdd(ctx, "zsetrem", 1.0, "remove_me")
		c.ZAdd(ctx, "zsetrem", 2.0, "keep_me")

		err := c.ZRem(ctx, "zsetrem", "remove_me")
		assert.NoError(t, err)

		result, _ := c.ZRange(ctx, "zsetrem", 0, -1)
		assert.NotContains(t, result, "remove_me")
		assert.Contains(t, result, "keep_me")
	})
}
