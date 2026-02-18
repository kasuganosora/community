package testutil

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewTestRedis creates a Redis client for testing
// Returns nil if Redis is not available
// Configuration is loaded from .env.test file or environment variables
// Deprecated: Use NewRealRedis or NewAutoRedis from redis_mock.go instead
func NewTestRedis(t *testing.T) *redis.Client {
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

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available at %s:%d, skipping test: %v", config.RedisHost, config.RedisPort, err)
		return nil
	}

	// Flush test database before each test
	client.FlushDB(ctx)

	return client
}

// SkipIfNoRedis skips the test if Redis is not available
// Deprecated: Use NewRealRedis or NewAutoRedis from redis_mock.go instead
func SkipIfNoRedis(t *testing.T) *redis.Client {
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

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available at %s:%d, skipping test: %v", config.RedisHost, config.RedisPort, err)
		return nil
	}

	// Flush test database
	client.FlushDB(ctx)

	return client
}

// IsRedisAvailable checks if Redis is available
func IsRedisAvailable() bool {
	config := GetTestConfig()

	client := redis.NewClient(&redis.Options{
		Addr:        GetRedisAddr(),
		Password:    config.RedisPassword,
		DB:          config.RedisDB,
		DialTimeout: 2 * time.Second,
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	return client.Ping(ctx).Err() == nil
}

// GetRedisAddr returns the Redis address for testing
func GetRedisAddr() string {
	config := GetTestConfig()
	return fmt.Sprintf("%s:%d", config.RedisHost, config.RedisPort)
}

// GetRedisPassword returns the Redis password for testing
func GetRedisPassword() string {
	config := GetTestConfig()
	return config.RedisPassword
}

// GetRedisDB returns the Redis database number for testing
func GetRedisDB() int {
	config := GetTestConfig()
	return config.RedisDB
}

// Helper to parse port from environment
func init() {
	config := GetTestConfig()
	if portStr := getEnv("REDIS_TEST_PORT", ""); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil && port > 0 {
			config.RedisPort = port
		}
	}
}

// FlushTestRedis flushes the test Redis database
// Useful for cleanup between tests
func FlushTestRedis(t *testing.T, client *redis.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Logf("Warning: failed to flush test Redis database: %v", err)
	}
}

// NewRedisClient creates a new Redis client with test configuration
// Does not check connectivity - use NewTestRedis for that
func NewRedisClient() *redis.Client {
	config := GetTestConfig()

	return redis.NewClient(&redis.Options{
		Addr:         GetRedisAddr(),
		Password:     config.RedisPassword,
		DB:           config.RedisDB,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})
}
