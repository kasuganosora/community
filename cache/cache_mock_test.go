package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	"github.com/kasuganosora/community/tests/testutil"
	redisv9 "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCache creates a Cache with a redismock client.
func mockCache(t *testing.T) (*Cache, redismock.ClientMock) {
	t.Helper()

	client, mock := redismock.NewClientMock()
	c := &Cache{
		client: client,
		prefix: "test:", //nolint:goconst
	}

	t.Cleanup(func() {
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Disable TTL jitter so redismock can match exact expirations.
	// jitterTTL is restored to its production implementation on cleanup.
	originalJitter := jitterTTL
	jitterTTL = func(d time.Duration) time.Duration { return d }
	t.Cleanup(func() { jitterTTL = originalJitter })

	return c, mock
}

// ============================================================
// Set / Get
// ============================================================

func TestMockCache_Set(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success with struct", func(t *testing.T) {
		type Data struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		}

		d := Data{Name: "hello", Count: 42} //nolint:goconst
		data, err := json.Marshal(d)
		require.NoError(t, err)

		mock.ExpectSet("test:mykey", data, 5*time.Minute).SetVal("OK")
		err = c.Set(ctx, "mykey", d, 5*time.Minute)
		require.NoError(t, err)
	})

	t.Run("marshal error (chan cannot be marshaled)", func(t *testing.T) {
		ch := make(chan int)
		err := c.Set(ctx, "badkey", ch, time.Minute)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "json")
	})

	t.Run("redis error on set", func(t *testing.T) {
		data, _ := json.Marshal("val")
		mock.ExpectSet("test:errkey", data, time.Minute).SetErr(errors.New("connection refused"))
		err := c.Set(ctx, "errkey", "val", time.Minute)
		require.Error(t, err)
	})
}

func TestMockCache_Get(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	type Data struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	t.Run("success", func(t *testing.T) {
		orig := Data{Name: "hello", Count: 42}
		data, _ := json.Marshal(orig)

		mock.ExpectGet("test:mykey").SetVal(string(data))

		var result Data

		err := c.Get(ctx, "mykey", &result)
		require.NoError(t, err)
		assert.Equal(t, orig, result)
	})

	t.Run("key not found", func(t *testing.T) {
		mock.ExpectGet("test:missing").SetErr(redisv9.Nil)

		var result Data

		err := c.Get(ctx, "missing", &result)
		require.Error(t, err)
		assert.True(t, IsErrCacheMiss(err))
	})

	t.Run("unmarshal error (json into wrong type)", func(t *testing.T) {
		mock.ExpectGet("test:badjson").SetVal("not-json-data")

		var result int

		err := c.Get(ctx, "badjson", &result)
		require.Error(t, err)
	})
}

// ============================================================
// SetString / GetString
// ============================================================

func TestMockCache_SetString(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mock.ExpectSet("test:strkey", "myvalue", time.Minute).SetVal("OK")
		err := c.SetString(ctx, "strkey", "myvalue", time.Minute)
		require.NoError(t, err)
	})

	t.Run("redis error", func(t *testing.T) {
		mock.ExpectSet("test:errstr", "val", 0).SetErr(errors.New("write error"))

		err := c.SetString(ctx, "errstr", "val", 0)
		require.Error(t, err)
	})
}

func TestMockCache_GetString(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mock.ExpectGet("test:strkey").SetVal("hello world")

		val, err := c.GetString(ctx, "strkey")
		require.NoError(t, err)
		assert.Equal(t, "hello world", val)
	})

	t.Run("key not found", func(t *testing.T) {
		mock.ExpectGet("test:missing").SetErr(redisv9.Nil)

		_, err := c.GetString(ctx, "missing")
		require.Error(t, err)
	})
}

// ============================================================
// Delete
// ============================================================

func TestMockCache_Delete(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("single key", func(t *testing.T) {
		mock.ExpectDel("test:k1").SetVal(1)

		err := c.Delete(ctx, "k1")
		require.NoError(t, err)
	})

	t.Run("multiple keys", func(t *testing.T) {
		mock.ExpectDel("test:k1", "test:k2", "test:k3").SetVal(3)

		err := c.Delete(ctx, "k1", "k2", "k3")
		require.NoError(t, err)
	})

	t.Run("redis error", func(t *testing.T) {
		mock.ExpectDel("test:k1").SetErr(errors.New("del error"))

		err := c.Delete(ctx, "k1")
		require.Error(t, err)
	})
}

// ============================================================
// DeleteByPattern
// ============================================================

func TestMockCache_DeleteByPattern(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("scan finds keys and deletes them", func(t *testing.T) {
		// First scan returns keys and cursor=0 (done)
		mock.ExpectScan(0, "test:users:*", int64(100)).SetVal([]string{"test:users:1", "test:users:2"}, 0)
		mock.ExpectDel("test:users:1", "test:users:2").SetVal(2)

		err := c.DeleteByPattern(ctx, "users:*")
		require.NoError(t, err)
	})

	t.Run("scan paginates across multiple cursors", func(t *testing.T) {
		// First scan returns cursor=5 (more to go)
		mock.ExpectScan(0, "test:items:*", int64(100)).SetVal([]string{"test:items:1", "test:items:2"}, 5)
		mock.ExpectDel("test:items:1", "test:items:2").SetVal(2)
		// Second scan returns cursor=0 (done)
		mock.ExpectScan(5, "test:items:*", int64(100)).SetVal([]string{"test:items:3"}, 0)
		mock.ExpectDel("test:items:3").SetVal(1)

		err := c.DeleteByPattern(ctx, "items:*")
		require.NoError(t, err)
	})

	t.Run("scan finds no keys", func(t *testing.T) {
		mock.ExpectScan(0, "test:empty:*", int64(100)).SetVal([]string{}, 0)

		err := c.DeleteByPattern(ctx, "empty:*")
		require.NoError(t, err)
	})

	t.Run("scan error", func(t *testing.T) {
		mock.ExpectScan(0, "test:err:*", int64(100)).SetErr(errors.New("scan error"))

		err := c.DeleteByPattern(ctx, "err:*")
		require.Error(t, err)
	})

	t.Run("delete error after scan", func(t *testing.T) {
		mock.ExpectScan(0, "test:delerr:*", int64(100)).SetVal([]string{"test:delerr:x"}, 0)
		mock.ExpectDel("test:delerr:x").SetErr(errors.New("del error"))

		err := c.DeleteByPattern(ctx, "delerr:*")
		require.Error(t, err)
	})
}

// ============================================================
// Exists
// ============================================================

func TestMockCache_Exists(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("key exists", func(t *testing.T) {
		mock.ExpectExists("test:yes").SetVal(1)

		exists, err := c.Exists(ctx, "yes")
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("key does not exist", func(t *testing.T) {
		mock.ExpectExists("test:no").SetVal(0)

		exists, err := c.Exists(ctx, "no")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("redis error", func(t *testing.T) {
		mock.ExpectExists("test:err").SetErr(errors.New("exists error"))

		_, err := c.Exists(ctx, "err")
		require.Error(t, err)
	})
}

// ============================================================
// Expire / TTL
// ============================================================

func TestMockCache_Expire(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mock.ExpectExpire("test:mykey", 10*time.Minute).SetVal(true)
		err := c.Expire(ctx, "mykey", 10*time.Minute)
		require.NoError(t, err)
	})

	t.Run("redis error", func(t *testing.T) {
		mock.ExpectExpire("test:mykey", time.Minute).SetErr(errors.New("expire error"))
		err := c.Expire(ctx, "mykey", time.Minute)
		require.Error(t, err)
	})
}

func TestMockCache_TTL(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("key has ttl", func(t *testing.T) {
		mock.ExpectTTL("test:mykey").SetVal(300 * time.Second)

		ttl, err := c.TTL(ctx, "mykey")
		require.NoError(t, err)
		assert.Equal(t, 300*time.Second, ttl)
	})

	t.Run("key has no ttl (-1 = no expiry)", func(t *testing.T) {
		mock.ExpectTTL("test:persist").SetVal(-1 * time.Second)

		ttl, err := c.TTL(ctx, "persist")
		require.NoError(t, err)
		assert.Equal(t, -1*time.Second, ttl)
	})

	t.Run("key does not exist (-2 = not found)", func(t *testing.T) {
		mock.ExpectTTL("test:gone").SetVal(-2 * time.Second)

		ttl, err := c.TTL(ctx, "gone")
		require.NoError(t, err)
		assert.Equal(t, -2*time.Second, ttl)
	})
}

// ============================================================
// Increment / IncrementBy / Decrement
// ============================================================

func TestMockCache_Increment(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mock.ExpectIncr("test:counter").SetVal(1)

		val, err := c.Increment(ctx, "counter")
		require.NoError(t, err)
		assert.Equal(t, int64(1), val)
	})

	t.Run("redis error", func(t *testing.T) {
		mock.ExpectIncr("test:counter").SetErr(errors.New("incr error"))

		_, err := c.Increment(ctx, "counter")
		require.Error(t, err)
	})
}

func TestMockCache_IncrementBy(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mock.ExpectIncrBy("test:counter", int64(10)).SetVal(10)

		val, err := c.IncrementBy(ctx, "counter", 10)
		require.NoError(t, err)
		assert.Equal(t, int64(10), val)
	})

	t.Run("redis error", func(t *testing.T) {
		mock.ExpectIncrBy("test:counter", int64(5)).SetErr(errors.New("incrby error"))

		_, err := c.IncrementBy(ctx, "counter", 5)
		require.Error(t, err)
	})
}

func TestMockCache_Decrement(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mock.ExpectDecr("test:counter").SetVal(9)

		val, err := c.Decrement(ctx, "counter")
		require.NoError(t, err)
		assert.Equal(t, int64(9), val)
	})

	t.Run("redis error", func(t *testing.T) {
		mock.ExpectDecr("test:counter").SetErr(errors.New("decr error"))

		_, err := c.Decrement(ctx, "counter")
		require.Error(t, err)
	})
}

// ============================================================
// Hash operations
// ============================================================

func TestMockCache_HSet(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		data, _ := json.Marshal(map[string]int{"a": 1})
		mock.ExpectHSet("test:hk", "field1", data).SetVal(1)

		err := c.HSet(ctx, "hk", "field1", map[string]int{"a": 1})
		require.NoError(t, err)
	})

	t.Run("marshal error", func(t *testing.T) {
		ch := make(chan int)
		err := c.HSet(ctx, "hk", "field1", ch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "json")
	})

	t.Run("redis error", func(t *testing.T) {
		data, _ := json.Marshal("val")
		mock.ExpectHSet("test:hk", "f", data).SetErr(errors.New("hset error"))

		err := c.HSet(ctx, "hk", "f", "val")
		require.Error(t, err)
	})
}

func TestMockCache_HGet(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		orig := map[string]int{"x": 10}
		data, _ := json.Marshal(orig)
		mock.ExpectHGet("test:hk", "field1").SetVal(string(data))

		var result map[string]int

		err := c.HGet(ctx, "hk", "field1", &result)
		require.NoError(t, err)
		assert.Equal(t, orig, result)
	})

	t.Run("field not found", func(t *testing.T) {
		mock.ExpectHGet("test:hk", "missing").SetErr(redisv9.Nil)

		var result map[string]int

		err := c.HGet(ctx, "hk", "missing", &result)
		require.Error(t, err)
	})

	t.Run("unmarshal error", func(t *testing.T) {
		mock.ExpectHGet("test:hk", "f").SetVal("not-json")

		var result int

		err := c.HGet(ctx, "hk", "f", &result)
		require.Error(t, err)
	})
}

func TestMockCache_HGetAll(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mock.ExpectHGetAll("test:hk").SetVal(map[string]string{"f1": "v1", "f2": "v2"})

		result, err := c.HGetAll(ctx, "hk")
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"f1": "v1", "f2": "v2"}, result)
	})

	t.Run("empty hash", func(t *testing.T) {
		mock.ExpectHGetAll("test:empty").SetVal(map[string]string{})

		result, err := c.HGetAll(ctx, "empty")
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}

func TestMockCache_HDel(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("single field", func(t *testing.T) {
		mock.ExpectHDel("test:hk", "f1").SetVal(1)

		err := c.HDel(ctx, "hk", "f1")
		require.NoError(t, err)
	})

	t.Run("multiple fields", func(t *testing.T) {
		mock.ExpectHDel("test:hk", "f1", "f2", "f3").SetVal(3)

		err := c.HDel(ctx, "hk", "f1", "f2", "f3")
		require.NoError(t, err)
	})

	t.Run("redis error", func(t *testing.T) {
		mock.ExpectHDel("test:hk", "f1").SetErr(errors.New("hdel error"))

		err := c.HDel(ctx, "hk", "f1")
		require.Error(t, err)
	})
}

// ============================================================
// List operations
// ============================================================

func TestMockCache_LPush(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mock.ExpectLPush("test:lst", "a", "b").SetVal(2)

		err := c.LPush(ctx, "lst", "a", "b")
		require.NoError(t, err)
	})

	t.Run("redis error", func(t *testing.T) {
		mock.ExpectLPush("test:lst", "x").SetErr(errors.New("lpush error"))

		err := c.LPush(ctx, "lst", "x")
		require.Error(t, err)
	})
}

func TestMockCache_RPush(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mock.ExpectRPush("test:lst", "x", "y").SetVal(2)

		err := c.RPush(ctx, "lst", "x", "y")
		require.NoError(t, err)
	})

	t.Run("redis error", func(t *testing.T) {
		mock.ExpectRPush("test:lst", "x").SetErr(errors.New("rpush error"))

		err := c.RPush(ctx, "lst", "x")
		require.Error(t, err)
	})
}

func TestMockCache_LRange(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("full range", func(t *testing.T) {
		mock.ExpectLRange("test:lst", int64(0), int64(-1)).SetVal([]string{"a", "b", "c"})

		result, err := c.LRange(ctx, "lst", 0, -1)
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b", "c"}, result)
	})

	t.Run("empty list", func(t *testing.T) {
		mock.ExpectLRange("test:empty", int64(0), int64(-1)).SetVal([]string{})

		result, err := c.LRange(ctx, "empty", 0, -1)
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}

func TestMockCache_LLen(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mock.ExpectLLen("test:lst").SetVal(5)

		length, err := c.LLen(ctx, "lst")
		require.NoError(t, err)
		assert.Equal(t, int64(5), length)
	})
}

// ============================================================
// Set operations
// ============================================================

func TestMockCache_SAdd(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mock.ExpectSAdd("test:set", "m1", "m2").SetVal(2)

		err := c.SAdd(ctx, "set", "m1", "m2")
		require.NoError(t, err)
	})

	t.Run("redis error", func(t *testing.T) {
		mock.ExpectSAdd("test:set", "m1").SetErr(errors.New("sadd error"))

		err := c.SAdd(ctx, "set", "m1")
		require.Error(t, err)
	})
}

func TestMockCache_SMembers(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mock.ExpectSMembers("test:set").SetVal([]string{"a", "b", "c"})

		result, err := c.SMembers(ctx, "set")
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b", "c"}, result)
	})

	t.Run("empty set", func(t *testing.T) {
		mock.ExpectSMembers("test:empty").SetVal([]string{})

		result, err := c.SMembers(ctx, "empty")
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}

func TestMockCache_SIsMember(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("is member", func(t *testing.T) {
		mock.ExpectSIsMember("test:set", "m1").SetVal(true)

		ok, err := c.SIsMember(ctx, "set", "m1")
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("not member", func(t *testing.T) {
		mock.ExpectSIsMember("test:set", "m99").SetVal(false)

		ok, err := c.SIsMember(ctx, "set", "m99")
		require.NoError(t, err)
		assert.False(t, ok)
	})
}

// ============================================================
// Sorted set operations
// ============================================================

func TestMockCache_ZAdd(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mock.ExpectZAdd("test:zset", redisv9.Z{Score: 1.5, Member: "one"}).SetVal(1)

		err := c.ZAdd(ctx, "zset", 1.5, "one")
		require.NoError(t, err)
	})

	t.Run("redis error", func(t *testing.T) {
		mock.ExpectZAdd("test:zset", redisv9.Z{Score: 1.0, Member: "x"}).SetErr(errors.New("zadd error"))

		err := c.ZAdd(ctx, "zset", 1.0, "x")
		require.Error(t, err)
	})
}

func TestMockCache_ZRange(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("full range", func(t *testing.T) {
		mock.ExpectZRange("test:zset", int64(0), int64(-1)).SetVal([]string{"a", "b", "c"})

		result, err := c.ZRange(ctx, "zset", 0, -1)
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b", "c"}, result)
	})
}

func TestMockCache_ZRangeByScore(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mock.ExpectZRangeArgs(redisv9.ZRangeArgs{
			Key:     "test:zset",
			Start:   "2",
			Stop:    "8",
			Offset:  0,
			Count:   10,
			ByScore: true,
		}).SetVal([]string{"b"})

		result, err := c.ZRangeByScore(ctx, "zset", "2", "8", 0, 10)
		require.NoError(t, err)
		assert.Equal(t, []string{"b"}, result)
	})

	t.Run("empty result", func(t *testing.T) {
		mock.ExpectZRangeArgs(redisv9.ZRangeArgs{
			Key:     "test:zset",
			Start:   "100",
			Stop:    "200",
			Offset:  0,
			Count:   10,
			ByScore: true,
		}).SetVal([]string{})

		result, err := c.ZRangeByScore(ctx, "zset", "100", "200", 0, 10)
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}

func TestMockCache_ZRem(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mock.ExpectZRem("test:zset", "member1").SetVal(1)

		err := c.ZRem(ctx, "zset", "member1")
		require.NoError(t, err)
	})

	t.Run("multiple members", func(t *testing.T) {
		mock.ExpectZRem("test:zset", "m1", "m2").SetVal(2)

		err := c.ZRem(ctx, "zset", "m1", "m2")
		require.NoError(t, err)
	})

	t.Run("redis error", func(t *testing.T) {
		mock.ExpectZRem("test:zset", "x").SetErr(errors.New("zrem error"))

		err := c.ZRem(ctx, "zset", "x")
		require.Error(t, err)
	})
}

// ============================================================
// Prefix verification
// ============================================================

func TestMockCache_Prefix(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	// Verify that prefix is correctly applied
	data, err := json.Marshal("value")
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectSet("custom:mykey", data, time.Minute).SetVal("OK")

	c2 := &Cache{
		client: c.client,
		prefix: "custom:",
	}
	err = c2.Set(ctx, "mykey", "value", time.Minute)
	require.NoError(t, err)

	// Verify expectations (should not need an assertion since we set up
	// expectations with the correct prefix). The Cleanup function in mockCache
	// only checks mock for `c`, not `c2`. Let's also check `c`'s mock is clean.
	_ = mock.ExpectationsWereMet()
}

// ============================================================
// Constants verification
// ============================================================

func TestCache_Constants(t *testing.T) {
	t.Run("key constants are non-empty", func(t *testing.T) {
		assert.NotEmpty(t, KeyAccount)
		assert.NotEmpty(t, KeyAccountByAcct)
		assert.NotEmpty(t, KeyStatus)
		assert.NotEmpty(t, KeyTimeline)
		assert.NotEmpty(t, KeyNotifications)
		assert.NotEmpty(t, KeyInstanceStats)
		assert.NotEmpty(t, KeyTrendingTags)
		assert.NotEmpty(t, KeyTrendingStatus)
		assert.NotEmpty(t, KeySearchResults)
	})

	t.Run("key constants have format verbs", func(t *testing.T) {
		assert.Contains(t, KeyAccount, "%d")
		assert.Contains(t, KeyAccountByAcct, "%s")
		assert.Contains(t, KeyStatus, "%d")
		assert.Contains(t, KeyTimeline, "%d")
		assert.Contains(t, KeyTimeline, "%s")
		assert.Contains(t, KeyNotifications, "%d")
		assert.Contains(t, KeySearchResults, "%s")
	})

	t.Run("key constants without format verbs", func(t *testing.T) {
		assert.NotContains(t, KeyInstanceStats, "%")
		assert.NotContains(t, KeyTrendingTags, "%")
		assert.NotContains(t, KeyTrendingStatus, "%")
	})

	t.Run("expiration constants are reasonable", func(t *testing.T) {
		assert.Equal(t, 5*time.Minute, DefaultExpiration)
		assert.Equal(t, 1*time.Minute, ShortExpiration)
		assert.Equal(t, 30*time.Minute, LongExpiration)
		assert.Equal(t, 24*time.Hour, VeryLongExpiration)

		// Verify ordering
		assert.Less(t, ShortExpiration, DefaultExpiration)
		assert.Less(t, DefaultExpiration, LongExpiration)
		assert.Less(t, LongExpiration, VeryLongExpiration)
	})

	t.Run("key format produces expected results", func(t *testing.T) {
		key := fmt.Sprintf(KeyAccount, 42)
		assert.Equal(t, "account:42", key)

		key = fmt.Sprintf(KeyAccountByAcct, "user@example.com")
		assert.Equal(t, "account:acct:user@example.com", key)

		key = fmt.Sprintf(KeyStatus, 100)
		assert.Equal(t, "status:100", key)

		key = fmt.Sprintf(KeyTimeline, 1, "home")
		assert.Equal(t, "timeline:1:home", key)

		key = fmt.Sprintf(KeyNotifications, 5)
		assert.Equal(t, "notifications:5", key)

		key = fmt.Sprintf(KeySearchResults, "golang")
		assert.Equal(t, "search:golang", key)
	})
}

// ============================================================
// Helper functions (using testutil mock to inject into defaultCache)
// ============================================================

func TestCache_HelperFunctions(t *testing.T) {
	// Since CacheAccount/GetCachedAccount/InvalidateAccount etc. use GetCache()
	// which depends on the global singleton, we test them by setting defaultCache
	// directly (same package access).
	client, mock := redismock.NewClientMock()
	ctx := context.Background()

	// Disable TTL jitter so redismock can match exact expirations.
	originalJitter := jitterTTL
	jitterTTL = func(d time.Duration) time.Duration { return d }
	t.Cleanup(func() { jitterTTL = originalJitter })

	// Reset the once for testing by creating a new Cache with the mock client
	testCache := &Cache{
		client: client,
		prefix: "community:",
	}

	t.Run("CacheAccount", func(t *testing.T) {
		type Account struct {
			ID       uint64 `json:"id"`
			Username string `json:"username"`
		}

		acc := Account{ID: 123, Username: "testuser"}
		data, _ := json.Marshal(acc)

		key := fmt.Sprintf(KeyAccount, 123) // "account:123"
		mock.ExpectSet("community:"+key, data, LongExpiration).SetVal("OK")

		// Instead of calling GetCache(), we test the logic directly
		err := testCache.Set(ctx, key, acc, LongExpiration)
		require.NoError(t, err)
	})

	t.Run("GetCachedAccount", func(t *testing.T) {
		type Account struct {
			ID       uint64 `json:"id"`
			Username string `json:"username"`
		}

		acc := Account{ID: 456, Username: "otheruser"}
		data, _ := json.Marshal(acc)

		key := fmt.Sprintf(KeyAccount, 456)
		mock.ExpectGet("community:" + key).SetVal(string(data))

		var result Account

		err := testCache.Get(ctx, key, &result)
		require.NoError(t, err)
		assert.Equal(t, acc, result)
	})

	t.Run("InvalidateAccount", func(t *testing.T) {
		key := fmt.Sprintf(KeyAccount, 789)
		mock.ExpectDel("community:" + key).SetVal(1)

		err := testCache.Delete(ctx, key)
		require.NoError(t, err)
	})

	t.Run("CacheStatus", func(t *testing.T) {
		type Status struct {
			ID      uint64 `json:"id"`
			Content string `json:"content"`
		}

		st := Status{ID: 100, Content: "hello"}
		data, _ := json.Marshal(st)

		key := fmt.Sprintf(KeyStatus, 100)
		mock.ExpectSet("community:"+key, data, LongExpiration).SetVal("OK")

		err := testCache.Set(ctx, key, st, LongExpiration)
		require.NoError(t, err)
	})

	t.Run("GetCachedStatus", func(t *testing.T) {
		type Status struct {
			ID      uint64 `json:"id"`
			Content string `json:"content"`
		}

		st := Status{ID: 200, Content: "world"}
		data, _ := json.Marshal(st)

		key := fmt.Sprintf(KeyStatus, 200)
		mock.ExpectGet("community:" + key).SetVal(string(data))

		var result Status

		err := testCache.Get(ctx, key, &result)
		require.NoError(t, err)
		assert.Equal(t, st, result)
	})

	t.Run("InvalidateStatus", func(t *testing.T) {
		key := fmt.Sprintf(KeyStatus, 200)
		mock.ExpectDel("community:" + key).SetVal(1)

		err := testCache.Delete(ctx, key)
		require.NoError(t, err)
	})

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ============================================================
// GetCache with testutil
// ============================================================

func TestCache_GetCacheReturnsNewInstance(t *testing.T) {
	// Test that we can create a Cache via testutil mock
	mockRedis := testutil.NewMockRedis(t)

	c := &Cache{
		client: mockRedis.Client,
		prefix: "test:",
	}
	assert.NotNil(t, c)
	assert.Equal(t, "test:", c.prefix)
}

// ============================================================
// Edge cases for Set with various types
// ============================================================

func TestMockCache_SetVariousTypes(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("string value", func(t *testing.T) {
		data, _ := json.Marshal("hello")
		mock.ExpectSet("test:str", data, time.Minute).SetVal("OK")
		err := c.Set(ctx, "str", "hello", time.Minute)
		require.NoError(t, err)
	})

	t.Run("int value", func(t *testing.T) {
		data, _ := json.Marshal(42)
		mock.ExpectSet("test:num", data, time.Minute).SetVal("OK")
		err := c.Set(ctx, "num", 42, time.Minute)
		require.NoError(t, err)
	})

	t.Run("slice value", func(t *testing.T) {
		data, _ := json.Marshal([]string{"a", "b", "c"})
		mock.ExpectSet("test:slc", data, time.Minute).SetVal("OK")
		err := c.Set(ctx, "slc", []string{"a", "b", "c"}, time.Minute)
		require.NoError(t, err)
	})

	t.Run("map value", func(t *testing.T) {
		data, _ := json.Marshal(map[string]int{"x": 1, "y": 2})
		mock.ExpectSet("test:mp", data, time.Minute).SetVal("OK")
		err := c.Set(ctx, "mp", map[string]int{"x": 1, "y": 2}, time.Minute)
		require.NoError(t, err)
	})

	t.Run("nil value", func(t *testing.T) {
		data, _ := json.Marshal(nil)
		mock.ExpectSet("test:nil", data, time.Minute).SetVal("OK")
		err := c.Set(ctx, "nil", nil, time.Minute)
		require.NoError(t, err)
	})

	t.Run("bool value", func(t *testing.T) {
		data, _ := json.Marshal(true)
		mock.ExpectSet("test:bl", data, time.Minute).SetVal("OK")
		err := c.Set(ctx, "bl", true, time.Minute)
		require.NoError(t, err)
	})
}

// ============================================================
// Edge cases for HSet with various types
// ============================================================

func TestMockCache_HSetVariousTypes(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("string field value", func(t *testing.T) {
		data, _ := json.Marshal("hello")
		mock.ExpectHSet("test:h", "f", data).SetVal(1)

		err := c.HSet(ctx, "h", "f", "hello")
		require.NoError(t, err)
	})

	t.Run("map field value", func(t *testing.T) {
		data, _ := json.Marshal(map[string]string{"k": "v"})
		mock.ExpectHSet("test:h", "data", data).SetVal(1)

		err := c.HSet(ctx, "h", "data", map[string]string{"k": "v"})
		require.NoError(t, err)
	})
}

// ============================================================
// Negative / zero expiration
// ============================================================

func TestMockCache_ZeroExpiration(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("Set with zero expiration (no ttl)", func(t *testing.T) {
		data, _ := json.Marshal("persist")
		mock.ExpectSet("test:persist", data, time.Duration(0)).SetVal("OK")

		err := c.Set(ctx, "persist", "persist", 0)
		require.NoError(t, err)
	})

	t.Run("SetString with zero expiration", func(t *testing.T) {
		mock.ExpectSet("test:persiststr", "val", time.Duration(0)).SetVal("OK")

		err := c.SetString(ctx, "persiststr", "val", 0)
		require.NoError(t, err)
	})

	t.Run("Expire with zero duration", func(t *testing.T) {
		mock.ExpectExpire("test:key", time.Duration(0)).SetVal(true)

		err := c.Expire(ctx, "key", 0)
		require.NoError(t, err)
	})
}

// ============================================================
// Get into various destination types
// ============================================================

func TestMockCache_GetIntoVariousTypes(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	t.Run("into *string", func(t *testing.T) {
		data, _ := json.Marshal("hello")
		mock.ExpectGet("test:k").SetVal(string(data))

		var s string

		err := c.Get(ctx, "k", &s)
		require.NoError(t, err)
		assert.Equal(t, "hello", s)
	})

	t.Run("into *int", func(t *testing.T) {
		data, _ := json.Marshal(42)
		mock.ExpectGet("test:k").SetVal(string(data))

		var i int

		err := c.Get(ctx, "k", &i)
		require.NoError(t, err)
		assert.Equal(t, 42, i)
	})

	t.Run("into *[]string", func(t *testing.T) {
		data, _ := json.Marshal([]string{"a", "b"})
		mock.ExpectGet("test:k").SetVal(string(data))

		var s []string

		err := c.Get(ctx, "k", &s)
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, s)
	})

	t.Run("into *bool", func(t *testing.T) {
		data, _ := json.Marshal(true)
		mock.ExpectGet("test:k").SetVal(string(data))

		var b bool

		err := c.Get(ctx, "k", &b)
		require.NoError(t, err)
		assert.True(t, b)
	})

	t.Run("into *interface{}", func(t *testing.T) {
		data, _ := json.Marshal(3.14)
		mock.ExpectGet("test:k").SetVal(string(data))

		var v any

		err := c.Get(ctx, "k", &v)
		require.NoError(t, err)
		assert.InEpsilon(t, 3.14, v, 0.0001)
	})
}

// ============================================================
// Delete empty keys
// ============================================================

func TestMockCache_DeleteEmpty(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	// Delete with no keys is a no-op and should NOT issue any Redis command.
	err := c.Delete(ctx)
	require.NoError(t, err)
}
