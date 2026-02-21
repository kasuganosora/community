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

	// Create a mock client (nil for testing purposes)
	redises = []*redis.Client{nil}

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

func TestGetRedis_MultipleClients(t *testing.T) {
	// Save original redises
	originalRedises := redises
	defer func() { redises = originalRedises }()

	// Create multiple nil clients
	redises = []*redis.Client{nil, nil, nil}

	// Test getting different indices
	assert.Nil(t, GetRedis(0))
	assert.Nil(t, GetRedis(1))
	assert.Nil(t, GetRedis(2))
}

func TestGetRedises_ReturnsSlice(t *testing.T) {
	clients := GetRedises()
	assert.IsType(t, []*redis.Client{}, clients)
}

func TestGetRedis_WithEmptySlice(t *testing.T) {
	// Save original redises
	originalRedises := redises
	defer func() { redises = originalRedises }()

	redises = []*redis.Client{}

	// Should panic when slice is empty
	assert.Panics(t, func() {
		GetRedis()
	})
}

func TestGetRedis_BoundaryConditions(t *testing.T) {
	// Save original redises
	originalRedises := redises
	defer func() { redises = originalRedises }()

	t.Run("exact boundary index", func(t *testing.T) {
		redises = []*redis.Client{nil, nil, nil}
		// Index 2 is valid (length 3)
		client := GetRedis(2)
		assert.Nil(t, client)
	})

	t.Run("just beyond boundary", func(t *testing.T) {
		redises = []*redis.Client{nil, nil, nil}
		// Index 3 is invalid (length 3)
		assert.Panics(t, func() {
			GetRedis(3)
		})
	})
}

func TestRedisesGlobalVariable(t *testing.T) {
	// Verify that redises is accessible and modifiable for testing
	originalRedises := redises
	defer func() { redises = originalRedises }()

	// Modify the global variable
	redises = []*redis.Client{nil, nil}

	// Verify it was modified
	assert.Len(t, redises, 2)
}
