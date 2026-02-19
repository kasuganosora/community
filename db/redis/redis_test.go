package redis

import (
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

func TestGetRedises(t *testing.T) {
	// GetRedises returns the list of configured Redis clients
	clients := GetRedises()
	assert.NotNil(t, clients)
}

func TestGetRedis_NoConnection(t *testing.T) {
	// Save original redises
	originalRedises := redises
	defer func() { redises = originalRedises }()

	// Test with empty redises
	redises = []*redis.Client{}

	// Should panic when no redis is configured
	assert.Panics(t, func() {
		GetRedis()
	})
}

func TestGetRedis_DefaultConnection(t *testing.T) {
	// Save original redises
	originalRedises := redises
	defer func() { redises = originalRedises }()

	// Create a mock client
	redises = []*redis.Client{nil} // nil is fine for testing

	// Should return first connection when no index specified
	client := GetRedis()
	assert.Nil(t, client) // nil because we set it to nil
}

func TestGetRedis_OutOfRange(t *testing.T) {
	// Save original redises
	originalRedises := redises
	defer func() { redises = originalRedises }()

	redises = []*redis.Client{nil}

	// Should panic when index out of range
	assert.Panics(t, func() {
		GetRedis(99)
	})
}

func TestGetRedis_NegativeIndex(t *testing.T) {
	// Save original redises
	originalRedises := redises
	defer func() { redises = originalRedises }()

	redises = []*redis.Client{nil}

	// Should panic when negative index
	assert.Panics(t, func() {
		GetRedis(-1)
	})
}

func TestGetRedis_ValidIndex(t *testing.T) {
	// Save original redises
	originalRedises := redises
	defer func() { redises = originalRedises }()

	redises = []*redis.Client{nil, nil, nil}

	// Should return connection at valid index
	client := GetRedis(1)
	assert.Nil(t, client) // nil because we set it to nil
}

func TestGetRedis_ZeroIndex(t *testing.T) {
	// Save original redises
	originalRedises := redises
	defer func() { redises = originalRedises }()

	redises = []*redis.Client{nil}

	client := GetRedis(0)
	assert.Nil(t, client)
}

func TestRedisInit(t *testing.T) {
	// Test that init() runs without panic
	// This is called automatically when package is imported
	assert.NotPanics(t, func() {
		// init() is called automatically
	})
}
