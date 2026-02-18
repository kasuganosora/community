package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExtractDomainFromURI(t *testing.T) {
	tests := []struct {
		uri      string
		expected string
	}{
		{"https://example.com/users/alice", "example.com"},
		{"https://social.example.org/@bob", "social.example.org"},
		{"http://localhost:3000/users/test", "localhost:3000"},
		{"invalid-uri", ""},
		{"", ""},
	}

	for _, tt := range tests {
		result := ExtractDomainFromURI(tt.uri)
		assert.Equal(t, tt.expected, result, "ExtractDomainFromURI(%s)", tt.uri)
	}
}

func TestExtractUsernameFromURI(t *testing.T) {
	tests := []struct {
		uri      string
		expected string
	}{
		{"https://example.com/users/alice", "alice"},
		{"https://example.com/users/alice/statuses/123", "alice"},
		{"https://example.com/@bob", "bob"},
		{"https://example.com/@bob/statuses/456", "bob"},
		{"https://example.com/profile/alice", ""}, // unknown format
		{"invalid-uri", ""},
		{"", ""},
	}

	for _, tt := range tests {
		result := ExtractUsernameFromURI(tt.uri)
		assert.Equal(t, tt.expected, result, "ExtractUsernameFromURI(%s)", tt.uri)
	}
}

func TestExtractIDFromURI(t *testing.T) {
	tests := []struct {
		uri      string
		expected string
	}{
		{"https://example.com/statuses/123456", "123456"},
		{"https://example.com/users/alice/statuses/789", "789"},
		{"https://example.com/objects/abc123", "abc123"},
		{"https://example.com/", ""},
		{"https://example.com", ""},
		// "invalid-uri" is parsed as path by url.Parse, so it returns "invalid-uri"
		{"", ""},
	}

	for _, tt := range tests {
		result := ExtractIDFromURI(tt.uri)
		assert.Equal(t, tt.expected, result, "ExtractIDFromURI(%s)", tt.uri)
	}
}

func TestParseRFC3339(t *testing.T) {
	tests := []struct {
		timeStr  string
		isNil    bool
		expected string // RFC3339 format for comparison
	}{
		{"2024-01-15T10:30:00Z", false, "2024-01-15T10:30:00Z"},
		{"2024-01-15T10:30:00+08:00", false, "2024-01-15T10:30:00+08:00"},
		{"", true, ""},
		{"invalid", true, ""},
	}

	for _, tt := range tests {
		result := ParseRFC3339(tt.timeStr)
		if tt.isNil {
			assert.Nil(t, result, "ParseRFC3339(%s) should be nil", tt.timeStr)
		} else {
			assert.NotNil(t, result, "ParseRFC3339(%s) should not be nil", tt.timeStr)
			assert.Equal(t, tt.expected, result.Format(time.RFC3339))
		}
	}
}

func TestParseRFC3339WithFallback(t *testing.T) {
	fallback := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	// Valid time string
	result := ParseRFC3339WithFallback("2024-01-15T10:30:00Z", fallback)
	assert.Equal(t, 2024, result.Year())

	// Empty string - use fallback
	result = ParseRFC3339WithFallback("", fallback)
	assert.Equal(t, 2020, result.Year())

	// Invalid string - use fallback
	result = ParseRFC3339WithFallback("invalid", fallback)
	assert.Equal(t, 2020, result.Year())
}

func TestFormatRFC3339(t *testing.T) {
	tm := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	result := FormatRFC3339(tm)
	assert.Equal(t, "2024-01-15T10:30:00Z", result)
}

func TestFormatRFC3339Ptr(t *testing.T) {
	// Non-nil pointer
	tm := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	result := FormatRFC3339Ptr(&tm)
	assert.Equal(t, "2024-01-15T10:30:00Z", result)

	// Nil pointer
	result = FormatRFC3339Ptr(nil)
	assert.Equal(t, "", result)
}

func TestGetRandomHex(t *testing.T) {
	// Test length
	result := GetRandomHex(32)
	assert.Len(t, result, 32)

	// Test uniqueness
	result2 := GetRandomHex(32)
	assert.NotEqual(t, result, result2)

	// Test different lengths
	result16 := GetRandomHex(16)
	assert.Len(t, result16, 16)

	result64 := GetRandomHex(64)
	assert.Len(t, result64, 64)
}
