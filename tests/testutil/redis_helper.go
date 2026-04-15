package testutil

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// IsRedisAvailable checks if Redis is available
func IsRedisAvailable() bool {
	config := GetTestConfig()

	client := redis.NewClient(&redis.Options{
		Addr:        GetRedisAddr(),
		Password:    config.RedisPassword,
		DB:          config.RedisDB,
		DialTimeout: 2 * time.Second,
		ReadTimeout: 2 * time.Second,
	})
	defer func() { _ = client.Close() }()

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

// FlushTestRedis flushes the test Redis database
// Useful for cleanup between tests
func FlushTestRedis(t *testing.T, client *redis.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Logf("Warning: failed to flush test Redis database: %v", err)
	}
}
