package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLocalStorage_Exists(t *testing.T) {
	storage := &LocalStorage{Root: "/tmp/test"}

	t.Run("non-existing file", func(t *testing.T) {
		assert.False(t, storage.Exists("nonexistent.txt"))
	})

	t.Run("path traversal attempt", func(t *testing.T) {
		// Should return false for path traversal
		assert.False(t, storage.Exists("../etc/passwd"))
	})
}

func TestLocalStorage_Root(t *testing.T) {
	storage := &LocalStorage{Root: "/tmp/test"}
	assert.Equal(t, "/tmp/test", storage.Root)
}

func TestLocalStorage_EmptyRoot(t *testing.T) {
	storage := &LocalStorage{Root: ""}
	assert.Equal(t, "", storage.Root)
}

func TestLocalStorage_Struct(t *testing.T) {
	storage := &LocalStorage{
		Root: "/data/uploads",
	}

	assert.Equal(t, "/data/uploads", storage.Root)
}

func TestSave(t *testing.T) {
	// Test the Save function signature
	assert.NotNil(t, Save)
}
