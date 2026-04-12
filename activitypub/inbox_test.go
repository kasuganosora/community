package activitypub

import (
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

func TestGetVisibilityFromActivity(t *testing.T) {
	tests := []struct {
		name     string
		object   map[string]interface{}
		expected uint8
	}{
		{
			name: "public - Public in to",
			object: map[string]interface{}{
				"to": []interface{}{"https://www.w3.org/ns/activitystreams#Public"},
				"cc": []interface{}{"https://example.com/users/alice/followers"},
			},
			expected: models.StatusVisibilityPublic,
		},
		{
			name: "public - as:Public shorthand",
			object: map[string]interface{}{
				"to": []interface{}{"as:Public"},
				"cc": []interface{}{},
			},
			expected: models.StatusVisibilityPublic,
		},
		{
			name: "public - Public string",
			object: map[string]interface{}{
				"to": []interface{}{"Public"},
			},
			expected: models.StatusVisibilityPublic,
		},
		{
			name: "unlisted - Public in cc only",
			object: map[string]interface{}{
				"to": []interface{}{"https://example.com/users/alice/followers"},
				"cc": []interface{}{"https://www.w3.org/ns/activitystreams#Public"},
			},
			expected: models.StatusVisibilityUnlisted,
		},
		{
			name: "private - followers collection, no public",
			object: map[string]interface{}{
				"to": []interface{}{"https://example.com/users/alice/followers"},
				"cc": []interface{}{},
			},
			expected: models.StatusVisibilityPrivate,
		},
		{
			name: "private - followers in cc, no public",
			object: map[string]interface{}{
				"to": []interface{}{},
				"cc": []interface{}{"https://example.com/users/alice/followers"},
			},
			expected: models.StatusVisibilityPrivate,
		},
		{
			name: "direct - specific user only, no followers",
			object: map[string]interface{}{
				"to": []interface{}{"https://remote.com/users/bob"},
				"cc": []interface{}{},
			},
			expected: models.StatusVisibilityDirect,
		},
		{
			name: "direct - multiple specific users only",
			object: map[string]interface{}{
				"to": []interface{}{
					"https://remote.com/users/bob",
					"https://other.com/users/carol",
				},
				"cc": []interface{}{},
			},
			expected: models.StatusVisibilityDirect,
		},
		{
			name: "direct - user with @ format",
			object: map[string]interface{}{
				"to": []interface{}{"https://remote.com/@bob"},
				"cc": []interface{}{},
			},
			expected: models.StatusVisibilityDirect,
		},
		{
			name:     "private - empty to and cc",
			object:   map[string]interface{}{},
			expected: models.StatusVisibilityPrivate,
		},
		{
			name: "private - nil to and cc",
			object: map[string]interface{}{
				"to": nil,
				"cc": nil,
			},
			expected: models.StatusVisibilityPrivate,
		},
		{
			name: "public takes priority over everything",
			object: map[string]interface{}{
				"to": []interface{}{
					"https://www.w3.org/ns/activitystreams#Public",
					"https://example.com/users/alice/followers",
					"https://remote.com/users/bob",
				},
				"cc": []interface{}{},
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
	result = parseTagsToAccountIDs([]interface{}{})
	assert.Empty(t, result)

	// Test with non-mention tags (Hashtag, Emoji) - should be filtered out
	tags := []interface{}{
		map[string]interface{}{
			"type": "Hashtag",
			"name": "#test",
		},
		map[string]interface{}{
			"type": "Emoji",
			"name": ":smile:",
		},
	}
	result = parseTagsToAccountIDs(tags)
	assert.Empty(t, result)

	// Test with invalid tag format
	invalidTags := []interface{}{
		"not a map",
		42,
		nil,
	}
	result = parseTagsToAccountIDs(invalidTags)
	assert.Empty(t, result)

	// Test with mention tag but empty href
	mentionNoHref := []interface{}{
		map[string]interface{}{
			"type": "Mention",
			"name": "@test",
			"href": "",
		},
	}
	result = parseTagsToAccountIDs(mentionNoHref)
	assert.Empty(t, result)
}

func TestMaxRequestBodySize(t *testing.T) {
	assert.Equal(t, int64(1024*1024), int64(MaxRequestBodySize))
}
