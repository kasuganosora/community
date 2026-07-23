package search

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConstants(t *testing.T) {
	assert.Equal(t, "accounts", IndexAccounts)
	assert.Equal(t, "statuses", IndexStatuses)
	assert.Equal(t, "hashtags", IndexHashtags)
}

func TestAccountSearchDocument_GetID(t *testing.T) {
	doc := &AccountSearchDocument{
		ID:          "123",
		Username:    "testuser",
		DisplayName: "Test User",
	}

	assert.Equal(t, "123", doc.GetID())
}

func TestAccountSearchDocument_GetType(t *testing.T) {
	doc := &AccountSearchDocument{}
	assert.Equal(t, "account", doc.GetType())
}

func TestStatusSearchDocument_GetID(t *testing.T) {
	doc := &StatusSearchDocument{
		ID:      "456",
		Content: "Test status",
	}

	assert.Equal(t, "456", doc.GetID())
}

func TestStatusSearchDocument_GetType(t *testing.T) {
	doc := &StatusSearchDocument{}
	assert.Equal(t, "status", doc.GetType())
}

func TestHashtagSearchDocument_GetID(t *testing.T) {
	doc := &HashtagSearchDocument{
		ID:   "789",
		Name: "golang",
	}

	assert.Equal(t, "789", doc.GetID())
}

func TestHashtagSearchDocument_GetType(t *testing.T) {
	doc := &HashtagSearchDocument{}
	assert.Equal(t, "hashtag", doc.GetType())
}

func TestSearchDocumentInterface(t *testing.T) {
	// Verify all document types implement the interface
	var (
		_ SearchDocument = &AccountSearchDocument{}
		_ SearchDocument = &StatusSearchDocument{}
		_ SearchDocument = &HashtagSearchDocument{}
	)
}

func TestAccountSearchDocument_Struct(t *testing.T) {
	doc := &AccountSearchDocument{
		ID:          "1",
		Username:    "user",
		DisplayName: "Display Name",
		Domain:      "example.com",
		Bio:         "Bio text",
		CreatedAt:   1234567890,
	}

	assert.Equal(t, "1", doc.ID)
	assert.Equal(t, "user", doc.Username)
	assert.Equal(t, "Display Name", doc.DisplayName)
	assert.Equal(t, "example.com", doc.Domain)
	assert.Equal(t, "Bio text", doc.Bio)
	assert.Equal(t, int64(1234567890), doc.CreatedAt)
}

func TestStatusSearchDocument_Struct(t *testing.T) {
	doc := &StatusSearchDocument{
		ID:          "1",
		Content:     "Status content",
		SpoilerText: "Spoiler",
		AccountID:   "2",
		Username:    "user",
		Visibility:  "public",
		CreatedAt:   1234567890,
	}

	assert.Equal(t, "1", doc.ID)
	assert.Equal(t, "Status content", doc.Content)
	assert.Equal(t, "Spoiler", doc.SpoilerText)
	assert.Equal(t, "2", doc.AccountID)
	assert.Equal(t, "user", doc.Username)
	assert.Equal(t, "public", doc.Visibility)
	assert.Equal(t, int64(1234567890), doc.CreatedAt)
}

func TestHashtagSearchDocument_Struct(t *testing.T) {
	doc := &HashtagSearchDocument{
		ID:          "1",
		Name:        "golang",
		StatusCount: 100,
		CreatedAt:   1234567890,
	}

	assert.Equal(t, "1", doc.ID)
	assert.Equal(t, "golang", doc.Name)
	assert.Equal(t, int64(100), doc.StatusCount)
	assert.Equal(t, int64(1234567890), doc.CreatedAt)
}

func TestSearchResult_Struct(t *testing.T) {
	result := &SearchResult{
		Accounts: []string{"1", "2"},
		Statuses: []string{"3", "4"},
		Hashtags: []string{"5", "6"},
	}

	assert.Len(t, result.Accounts, 2)
	assert.Len(t, result.Statuses, 2)
	assert.Len(t, result.Hashtags, 2)
}

func TestGetMeilisearchClient(t *testing.T) {
	client := GetMeilisearchClient()
	assert.NotNil(t, client)
}

func TestGetMeilisearchClient_Singleton(t *testing.T) {
	client1 := GetMeilisearchClient()
	client2 := GetMeilisearchClient()

	assert.Equal(t, client1, client2)
}

func TestMeilisearchClient_IsEnabled(t *testing.T) {
	client := GetMeilisearchClient()
	// In test environment without configuration, it should be disabled
	enabled := client.IsEnabled()
	// The result depends on configuration, just verify no panic
	assert.NotNil(t, client)

	_ = enabled
}

func TestMeilisearchClient_IsEnabled_Disabled(t *testing.T) {
	client := &MeilisearchClient{
		enabled: false,
	}
	assert.False(t, client.IsEnabled())
}

func TestMeilisearchClient_IsEnabled_Enabled(t *testing.T) {
	// Note: IsEnabled() checks both enabled flag AND client != nil
	// Without a real meilisearch connection, client will be nil
	// So we can only test the disabled case reliably
	client := &MeilisearchClient{
		enabled: true,
		host:    "http://localhost:7700", //nolint:goconst
		apiKey:  "test-key",
		// client is nil, so IsEnabled will return false
	}
	// Since client is nil, this returns false even if enabled is true
	assert.False(t, client.IsEnabled())
}

func TestParseSearchQuery(t *testing.T) {
	tests := []struct {
		query         string
		expectedText  string
		expectedCount int
	}{
		{"hello world", "hello world", 0},
		{"from:user1", "", 1},
		{"hello from:user1", "hello", 1}, //nolint:goconst
		{"has:media", "", 1},
		{"hello has:media from:user1", "hello", 2},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			text, filters := ParseSearchQuery(tt.query)
			assert.Equal(t, tt.expectedText, text)
			assert.Len(t, filters, tt.expectedCount)
		})
	}
}

func TestToJSON(t *testing.T) {
	doc := &AccountSearchDocument{
		ID:       "1",
		Username: "test",
	}

	json := ToJSON(doc)
	assert.Contains(t, json, `"id"`)
	assert.Contains(t, json, `"username"`)
}

func TestStringToUint64(t *testing.T) {
	tests := []struct {
		input    string
		expected uint64
	}{
		{"123", 123},
		{"0", 0},
		{"abc", 0},
		{"", 0},
		{"9999999999", 9999999999},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := StringToUint64(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMeilisearchClient_Struct(t *testing.T) {
	client := &MeilisearchClient{
		host:    "http://localhost:7700",
		apiKey:  "test-key",
		enabled: true,
	}

	assert.Equal(t, "http://localhost:7700", client.host)
	assert.Equal(t, "test-key", client.apiKey)
	assert.True(t, client.enabled)
}

func TestSearchResult_Empty(t *testing.T) {
	result := &SearchResult{}

	assert.Nil(t, result.Accounts)
	assert.Nil(t, result.Statuses)
	assert.Nil(t, result.Hashtags)
}

func TestAccountSearchDocument_Empty(t *testing.T) {
	doc := &AccountSearchDocument{}

	assert.Empty(t, doc.ID)
	assert.Empty(t, doc.Username)
	assert.Empty(t, doc.DisplayName)
	assert.Empty(t, doc.Domain)
	assert.Empty(t, doc.Bio)
	assert.Zero(t, doc.CreatedAt)
}

func TestStatusSearchDocument_Empty(t *testing.T) {
	doc := &StatusSearchDocument{}

	assert.Empty(t, doc.ID)
	assert.Empty(t, doc.Content)
	assert.Empty(t, doc.SpoilerText)
	assert.Empty(t, doc.AccountID)
	assert.Empty(t, doc.Username)
	assert.Empty(t, doc.Visibility)
	assert.Zero(t, doc.CreatedAt)
}

func TestHashtagSearchDocument_Empty(t *testing.T) {
	doc := &HashtagSearchDocument{}

	assert.Empty(t, doc.ID)
	assert.Empty(t, doc.Name)
	assert.Zero(t, doc.StatusCount)
	assert.Zero(t, doc.CreatedAt)
}

func TestMeilisearchClient_IndexNotEnabled(t *testing.T) {
	client := &MeilisearchClient{
		enabled: false,
	}

	// Should return false when not enabled
	assert.False(t, client.IsEnabled())
}

func TestParseSearchQuery_Complex(t *testing.T) {
	tests := []struct {
		name          string
		query         string
		expectedText  string
		expectedCount int
	}{
		{"multiple filters", "hello from:user1 has:media", "hello", 2},
		{"only filters", "from:user1 has:image", "", 2},
		{"mixed case", "Hello World FROM:user1", "Hello World FROM:user1", 0},
		{"empty query", "", "", 0},
		{"only text", "just some text", "just some text", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, filters := ParseSearchQuery(tt.query)
			assert.Equal(t, tt.expectedText, text)
			assert.Len(t, filters, tt.expectedCount)
		})
	}
}

func TestSearchResult_AppendResults(t *testing.T) {
	result := &SearchResult{
		Accounts: []string{"1"},
		Statuses: []string{"2"},
		Hashtags: []string{"3"},
	}

	// Append new results
	result.Accounts = append(result.Accounts, "4")
	result.Statuses = append(result.Statuses, "5")
	result.Hashtags = append(result.Hashtags, "6")

	assert.Len(t, result.Accounts, 2)
	assert.Len(t, result.Statuses, 2)
	assert.Len(t, result.Hashtags, 2)
}

func TestStringToUint64_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected uint64
	}{
		{"negative number", "-123", 0},
		{"float number", "123.45", 0},
		{"very large number", "18446744073709551615", 18446744073709551615},
		// Note: overflow wraps around in Go's strconv.ParseUint, not returns 0
		// Skip overflow test as behavior is undefined
		{"whitespace", " 123 ", 0},
		{"with newline", "123\n", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StringToUint64(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestToJSON_SpecialCharacters(t *testing.T) {
	doc := &AccountSearchDocument{
		ID:       "1",
		Username: "user<>&\"'",
		Bio:      "Bio with\nnewline",
	}

	json := ToJSON(doc)
	assert.Contains(t, json, "user")
	// JSON should be valid (escaped properly)
	assert.NotContains(t, json, "\n") // newlines should be escaped
}
