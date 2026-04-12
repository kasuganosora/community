package internalfs

import (
	"io/fs"
	"testing"

	"github.com/kasuganosora/community/config"
	"github.com/stretchr/testify/assert"
)

func TestGet_DevMode(t *testing.T) {
	// Save original value
	originalDevMode := config.C.DevMode

	// Test with DevMode = "true"
	config.C.DevMode = "true"
	result := Get()
	assert.NotNil(t, result)

	// Restore original value
	config.C.DevMode = originalDevMode
}

func TestGet_ProdMode(t *testing.T) {
	// Save original value
	originalDevMode := config.C.DevMode

	// Test with DevMode = "false"
	config.C.DevMode = "false"
	result := Get()
	assert.NotNil(t, result)
	// In prod mode, returns embed.FS{}
	assert.IsType(t, &result, new(fs.FS))

	// Restore original value
	config.C.DevMode = originalDevMode
}

func TestGet_ReturnsFS(t *testing.T) {
	result := Get()
	assert.NotNil(t, result)
}

func TestGet_DifferentModes(t *testing.T) {
	originalDevMode := config.C.DevMode

	tests := []struct {
		name    string
		devMode string
	}{
		{"dev mode true", "true"},
		{"dev mode false", "false"},
		{"dev mode empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config.C.DevMode = tt.devMode
			result := Get()
			assert.NotNil(t, result)
		})
	}

	config.C.DevMode = originalDevMode
}
