package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

func TestNewMockRedis(t *testing.T) {
	client := NewMockRedis(t)
	assert.NotNil(t, client)
	assert.NotNil(t, client.Client)
	assert.NotNil(t, client.Mock)
	assert.True(t, client.IsMock())
	assert.False(t, client.IsReal())
}

func TestMockRedis_SetGet(t *testing.T) {
	client := NewMockRedis(t)
	ctx := context.Background()

	// Setup expectations
	client.Mock.ExpectSet("test-key", "test-value", time.Minute).SetVal("OK")
	client.Mock.ExpectGet("test-key").SetVal("test-value")

	// Set value
	err := client.Client.Set(ctx, "test-key", "test-value", time.Minute).Err()
	assert.NoError(t, err)

	// Get value
	val, err := client.Client.Get(ctx, "test-key").Result()
	assert.NoError(t, err)
	assert.Equal(t, "test-value", val)

	// Verify expectations
	assert.NoError(t, client.Mock.ExpectationsWereMet())
}

func TestMockRedis_HashOperations(t *testing.T) {
	client := NewMockRedis(t)
	ctx := context.Background()

	// Setup expectations
	client.Mock.ExpectHSet("hash-key", "field1", "value1").SetVal(1)
	client.Mock.ExpectHGet("hash-key", "field1").SetVal("value1")

	// HSet
	err := client.Client.HSet(ctx, "hash-key", "field1", "value1").Err()
	assert.NoError(t, err)

	// HGet
	val, err := client.Client.HGet(ctx, "hash-key", "field1").Result()
	assert.NoError(t, err)
	assert.Equal(t, "value1", val)

	// Verify expectations
	assert.NoError(t, client.Mock.ExpectationsWereMet())
}

func TestMockRedis_ListOperations(t *testing.T) {
	client := NewMockRedis(t)
	ctx := context.Background()

	// Setup expectations
	client.Mock.ExpectLPush("list-key", "value1").SetVal(1)
	client.Mock.ExpectRPush("list-key", "value2").SetVal(2)
	client.Mock.ExpectLLen("list-key").SetVal(2)

	// LPush
	err := client.Client.LPush(ctx, "list-key", "value1").Err()
	assert.NoError(t, err)

	// RPush
	err = client.Client.RPush(ctx, "list-key", "value2").Err()
	assert.NoError(t, err)

	// LLen
	len, err := client.Client.LLen(ctx, "list-key").Result()
	assert.NoError(t, err)
	assert.Equal(t, int64(2), len)

	// Verify expectations
	assert.NoError(t, client.Mock.ExpectationsWereMet())
}

func TestMockRedis_SetOperations(t *testing.T) {
	client := NewMockRedis(t)
	ctx := context.Background()

	// Setup expectations
	client.Mock.ExpectSAdd("set-key", "member1", "member2").SetVal(2)
	client.Mock.ExpectSIsMember("set-key", "member1").SetVal(true)
	client.Mock.ExpectSIsMember("set-key", "member3").SetVal(false)

	// SAdd
	err := client.Client.SAdd(ctx, "set-key", "member1", "member2").Err()
	assert.NoError(t, err)

	// SIsMember
	isMember, err := client.Client.SIsMember(ctx, "set-key", "member1").Result()
	assert.NoError(t, err)
	assert.True(t, isMember)

	isMember, err = client.Client.SIsMember(ctx, "set-key", "member3").Result()
	assert.NoError(t, err)
	assert.False(t, isMember)

	// Verify expectations
	assert.NoError(t, client.Mock.ExpectationsWereMet())
}

func TestMockRedis_SortedSetOperations(t *testing.T) {
	client := NewMockRedis(t)
	ctx := context.Background()

	// Setup expectations
	client.Mock.ExpectZAdd("zset-key", redis.Z{Score: 1, Member: "one"}).SetVal(1)
	client.Mock.ExpectZAdd("zset-key", redis.Z{Score: 2, Member: "two"}).SetVal(1)

	// ZAdd
	err := client.Client.ZAdd(ctx, "zset-key", redis.Z{Score: 1, Member: "one"}).Err()
	assert.NoError(t, err)

	err = client.Client.ZAdd(ctx, "zset-key", redis.Z{Score: 2, Member: "two"}).Err()
	assert.NoError(t, err)

	// Verify expectations
	assert.NoError(t, client.Mock.ExpectationsWereMet())
}

func TestMockRedis_Delete(t *testing.T) {
	client := NewMockRedis(t)
	ctx := context.Background()

	// Setup expectations
	client.Mock.ExpectDel("delete-key").SetVal(1)

	// Delete
	val, err := client.Client.Del(ctx, "delete-key").Result()
	assert.NoError(t, err)
	assert.Equal(t, int64(1), val)

	// Verify expectations
	assert.NoError(t, client.Mock.ExpectationsWereMet())
}

func TestMockRedis_IncrDecr(t *testing.T) {
	client := NewMockRedis(t)
	ctx := context.Background()

	// Setup expectations
	client.Mock.ExpectIncr("counter").SetVal(1)
	client.Mock.ExpectIncr("counter").SetVal(2)
	client.Mock.ExpectDecr("counter").SetVal(1)

	// Incr
	val, err := client.Client.Incr(ctx, "counter").Result()
	assert.NoError(t, err)
	assert.Equal(t, int64(1), val)

	val, err = client.Client.Incr(ctx, "counter").Result()
	assert.NoError(t, err)
	assert.Equal(t, int64(2), val)

	// Decr
	val, err = client.Client.Decr(ctx, "counter").Result()
	assert.NoError(t, err)
	assert.Equal(t, int64(1), val)

	// Verify expectations
	assert.NoError(t, client.Mock.ExpectationsWereMet())
}

func TestMockRedis_Exists(t *testing.T) {
	client := NewMockRedis(t)
	ctx := context.Background()

	// Setup expectations
	client.Mock.ExpectExists("existing-key").SetVal(1)
	client.Mock.ExpectExists("non-existing-key").SetVal(0)

	// Exists
	exists, err := client.Client.Exists(ctx, "existing-key").Result()
	assert.NoError(t, err)
	assert.Equal(t, int64(1), exists)

	exists, err = client.Client.Exists(ctx, "non-existing-key").Result()
	assert.NoError(t, err)
	assert.Equal(t, int64(0), exists)

	// Verify expectations
	assert.NoError(t, client.Mock.ExpectationsWereMet())
}

func TestMockRedis_Expire(t *testing.T) {
	client := NewMockRedis(t)
	ctx := context.Background()

	// Setup expectations
	client.Mock.ExpectExpire("expire-key", time.Minute).SetVal(true)

	// Expire
	result, err := client.Client.Expire(ctx, "expire-key", time.Minute).Result()
	assert.NoError(t, err)
	assert.True(t, result)

	// Verify expectations
	assert.NoError(t, client.Mock.ExpectationsWereMet())
}

func TestSetupMockRedisTest(t *testing.T) {
	client := SetupMockRedisTest(t)
	assert.NotNil(t, client)
	assert.True(t, client.IsMock())
	// Cleanup and assertion happens automatically
}

func TestNewAutoRedis_WhenRedisUnavailable(t *testing.T) {
	// This test demonstrates the auto behavior
	// If Redis is available, it will use real Redis
	// If not, it will use mock
	client := NewAutoRedis(t)
	if client == nil {
		return // Test was skipped
	}
	defer client.Close()

	// Either mock or real Redis should work
	ctx := context.Background()
	if client.IsMock() {
		// Setup mock expectation
		client.Mock.ExpectPing().SetVal("PONG")
	}

	// Ping should work
	err := client.Client.Ping(ctx).Err()
	if client.IsMock() {
		assert.NoError(t, err)
	}
}

func TestRedisTestClient_SkipIfMock(t *testing.T) {
	client := NewMockRedis(t)

	// This should skip the test
	// client.SkipIfMock(t, "Test requires real Redis")
	// We can't actually test the skip here as it would stop the test
	// But we can test the condition
	assert.True(t, client.IsMock())
}

func TestRedisTestClient_Close(t *testing.T) {
	client := NewMockRedis(t)
	assert.NotNil(t, client)

	// Close should not error
	err := client.Close()
	assert.NoError(t, err)
}

func TestRedisWithMode_Mock(t *testing.T) {
	client := NewRedisWithMode(t, RedisModeMock)
	assert.NotNil(t, client)
	assert.True(t, client.IsMock())
	defer client.Close()
}
