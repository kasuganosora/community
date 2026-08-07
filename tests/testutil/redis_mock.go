package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
)

// RedisTestMode defines how Redis should be set up for tests.
type RedisTestMode int

const (
	// RedisModeMock uses redismock for unit testing (no real Redis needed).
	RedisModeMock RedisTestMode = iota
	// RedisModeReal uses a real Redis connection from .env.test.
	RedisModeReal
	// RedisModeAuto tries real Redis first, falls back to mock if unavailable.
	RedisModeAuto
)

// RedisTestClient holds the Redis client and optional mock.
type RedisTestClient struct {
	Client *redis.Client
	Mock   redismock.ClientMock
}

// NewMockRedis creates a mock Redis client for unit testing
// Use the Mock field to set up expectations.
func NewMockRedis(t *testing.T) *RedisTestClient {
	client, mock := redismock.NewClientMock()

	return &RedisTestClient{
		Client: client,
		Mock:   mock,
	}
}

// NewRealRedis creates a real Redis client for integration testing
// Returns nil if Redis is not available
// Configuration is loaded from .env.test file or environment variables.
func NewRealRedis(t *testing.T) *RedisTestClient {
	config := GetTestConfig()

	client := redis.NewClient(&redis.Options{
		Addr:         GetRedisAddr(),
		Password:     config.RedisPassword,
		DB:           config.RedisDB,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := client.Ping(ctx).Err()
	if err != nil {
		_ = client.Close()

		t.Skipf("Redis not available at %s, skipping test: %v", GetRedisAddr(), err)

		return nil
	}

	// Flush test database before test so each test starts from a clean slate.
	// Use a fresh context because the ping context above is about to be
	// cancelled by the deferred cancel().
	flushCtx, flushCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer flushCancel()

	if flushErr := client.FlushDB(flushCtx).Err(); flushErr != nil {
		// Flush failure is not fatal, but it may cause cross-test data bleed;
		// surface it in the test log so the cause is diagnosable.
		t.Logf("Warning: failed to flush test Redis database before test: %v", flushErr)
	}

	return &RedisTestClient{
		Client: client,
		Mock:   nil, // No mock for real Redis
	}
}

// NewAutoRedis tries to create a real Redis client, falls back to mock if unavailable
// If real Redis is available, Mock will be nil.
func NewAutoRedis(t *testing.T) *RedisTestClient {
	if IsRedisAvailable() {
		return NewRealRedis(t)
	}

	return NewMockRedis(t)
}

// NewRedisWithMode creates a Redis client based on the specified mode.
func NewRedisWithMode(t *testing.T, mode RedisTestMode) *RedisTestClient {
	switch mode {
	case RedisModeMock:
		return NewMockRedis(t)
	case RedisModeReal:
		return NewRealRedis(t)
	case RedisModeAuto:
		return NewAutoRedis(t)
	default:
		return NewAutoRedis(t)
	}
}

// Close closes the Redis client.
func (r *RedisTestClient) Close() error {
	if r.Client != nil {
		return r.Client.Close()
	}

	return nil
}

// AssertExpectations asserts all mock expectations were met
// Only works when using mock mode.
func (r *RedisTestClient) AssertExpectations(t *testing.T) {
	if r.Mock != nil {
		err := r.Mock.ExpectationsWereMet()
		if err != nil {
			t.Errorf("there were unfulfilled expectations: %s", err)
		}
	}
}

// IsMock returns true if this is a mock client.
func (r *RedisTestClient) IsMock() bool {
	return r.Mock != nil
}

// IsReal returns true if this is a real Redis connection.
func (r *RedisTestClient) IsReal() bool {
	return r.Mock == nil
}

// SkipIfMock skips the test if using mock Redis (for tests that need real Redis).
func (r *RedisTestClient) SkipIfMock(t *testing.T, reason string) {
	if r.IsMock() {
		t.Skipf("Skipping test with mock Redis: %s", reason)
	}
}

// SetupRedisTest creates a Redis test client with automatic cleanup
// mode determines whether to use mock, real, or auto-detect.
func SetupRedisTest(t *testing.T, mode RedisTestMode) *RedisTestClient {
	client := NewRedisWithMode(t, mode)
	if client == nil {
		return nil
	}

	t.Cleanup(func() {
		client.AssertExpectations(t)
		_ = client.Close()
	})

	return client
}

// SetupMockRedisTest creates a mock Redis client with automatic cleanup.
func SetupMockRedisTest(t *testing.T) *RedisTestClient {
	return SetupRedisTest(t, RedisModeMock)
}

// SetupRealRedisTest creates a real Redis client with automatic cleanup
// Skips the test if Redis is not available.
func SetupRealRedisTest(t *testing.T) *RedisTestClient {
	return SetupRedisTest(t, RedisModeReal)
}

// SetupAutoRedisTest creates a Redis client with automatic cleanup
// Uses real Redis if available, otherwise uses mock.
func SetupAutoRedisTest(t *testing.T) *RedisTestClient {
	return SetupRedisTest(t, RedisModeAuto)
}

// RedisClient returns the underlying redis.Client
// This is a convenience method for functions that expect *redis.Client.
func (r *RedisTestClient) RedisClient() *redis.Client {
	return r.Client
}
