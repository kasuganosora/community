package activitypub

import (
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

func TestGetVisibilityFromActivity(t *testing.T) {
	tests := []struct {
		name     string
		object   map[string]any
		expected uint8
	}{
		{
			name: "public - Public in to",
			object: map[string]any{
				"to": []any{"https://www.w3.org/ns/activitystreams#Public"}, //nolint:goconst
				"cc": []any{"https://example.com/users/alice/followers"},
			},
			expected: models.StatusVisibilityPublic,
		},
		{
			name: "public - as:Public shorthand",
			object: map[string]any{
				"to": []any{"as:Public"},
				"cc": []any{},
			},
			expected: models.StatusVisibilityPublic,
		},
		{
			name: "public - Public string",
			object: map[string]any{
				"to": []any{"Public"}, //nolint:goconst
			},
			expected: models.StatusVisibilityPublic,
		},
		{
			name: "unlisted - Public in cc only",
			object: map[string]any{
				"to": []any{"https://example.com/users/alice/followers"},
				"cc": []any{"https://www.w3.org/ns/activitystreams#Public"},
			},
			expected: models.StatusVisibilityUnlisted,
		},
		{
			name: "private - followers collection, no public",
			object: map[string]any{
				"to": []any{"https://example.com/users/alice/followers"},
				"cc": []any{},
			},
			expected: models.StatusVisibilityPrivate,
		},
		{
			name: "private - followers in cc, no public",
			object: map[string]any{
				"to": []any{},
				"cc": []any{"https://example.com/users/alice/followers"},
			},
			expected: models.StatusVisibilityPrivate,
		},
		{
			name: "direct - specific user only, no followers",
			object: map[string]any{
				"to": []any{"https://remote.com/users/bob"},
				"cc": []any{},
			},
			expected: models.StatusVisibilityDirect,
		},
		{
			name: "direct - multiple specific users only",
			object: map[string]any{
				"to": []any{
					"https://remote.com/users/bob",
					"https://other.com/users/carol",
				},
				"cc": []any{},
			},
			expected: models.StatusVisibilityDirect,
		},
		{
			name: "direct - user with @ format",
			object: map[string]any{
				"to": []any{"https://remote.com/@bob"},
				"cc": []any{},
			},
			expected: models.StatusVisibilityDirect,
		},
		{
			name:     "private - empty to and cc",
			object:   map[string]any{},
			expected: models.StatusVisibilityPrivate,
		},
		{
			name: "private - nil to and cc",
			object: map[string]any{
				"to": nil,
				"cc": nil,
			},
			expected: models.StatusVisibilityPrivate,
		},
		{
			name: "public takes priority over everything",
			object: map[string]any{
				"to": []any{
					"https://www.w3.org/ns/activitystreams#Public",
					"https://example.com/users/alice/followers",
					"https://remote.com/users/bob",
				},
				"cc": []any{},
			},
			expected: models.StatusVisibilityPublic,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getVisibilityFromActivity(tt.object)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsValidActivityPubContentType_Extended(t *testing.T) {
	// Additional edge cases beyond what's tested in signature_test.go
	tests := []struct {
		contentType string
		expected    bool
	}{
		{"application/activity+json", true},
		{"application/ld+json", true},
		{"application/json", true},
		{"application/ld+json; profile=\"https://www.w3.org/ns/activitystreams\"", true},
		{"Application/Activity+JSON", true}, // case insensitive
		{"application/activity+json; charset=utf-8", true},
		{"text/html", false},
		{"text/plain", false},
		{"", false},
		{"multipart/form-data", false},
		{"application/octet-stream", false},
		{"application/xml", false},
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			assert.Equal(t, tt.expected, isValidActivityPubContentType(tt.contentType))
		})
	}
}

func TestParseTagsToAccountIDs(t *testing.T) {
	// Test with nil tags
	result := parseTagsToAccountIDs(nil)
	assert.Empty(t, result)

	// Test with empty tags
	result = parseTagsToAccountIDs([]any{})
	assert.Empty(t, result)

	// Test with non-mention tags (Hashtag, Emoji) - should be filtered out
	tags := []any{
		map[string]any{
			"type": "Hashtag", //nolint:goconst
			"name": "#test",   //nolint:goconst
		},
		map[string]any{
			"type": "Emoji",
			"name": ":smile:",
		},
	}
	result = parseTagsToAccountIDs(tags)
	assert.Empty(t, result)

	// Test with invalid tag format
	invalidTags := []any{
		"not a map",
		42,
		nil,
	}
	result = parseTagsToAccountIDs(invalidTags)
	assert.Empty(t, result)

	// Test with mention tag but empty href
	mentionNoHref := []any{
		map[string]any{
			"type": "Mention", //nolint:goconst
			"name": "@test",
			"href": "", //nolint:goconst
		},
	}
	result = parseTagsToAccountIDs(mentionNoHref)
	assert.Empty(t, result)
}

func TestMaxRequestBodySize(t *testing.T) {
	assert.Equal(t, int64(1024*1024), int64(MaxRequestBodySize))
}
