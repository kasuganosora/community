package redis

import (
	goredis "github.com/redis/go-redis/v9"
)

// SetRedisesForTest sets the redis clients slice for testing.
// This allows tests to inject mock redis clients.
func SetRedisesForTest(clients []*goredis.Client) {
	redises = clients
}

// GetRedisesForTest returns the current redis clients slice (for saving/restoring in tests).
func GetRedisesForTest() []*goredis.Client {
	return redises
}
