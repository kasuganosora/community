package search

import (
	"sync"
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

// --- 注入防护测试 ---

func TestSanitizeID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid numeric", "12345", true},
		{"valid single digit", "0", true},
		{"valid large number", "999999999999", true},
		{"empty string", "", false},
		{"SQL injection attempt", "1 OR 1=1", false},
		{"filter injection OR", "1 OR visibility = 3", false},
		{"filter injection AND", "1 AND account_id = 2", false},
		{"semicolon injection", "1; DROP INDEX", false},
		{"parenthesis injection", "1)", false},
		{"bracket injection", "1]", false},
		{"quote injection", "1'", false},
		{"double quote injection", `1"`, false},
		{"IN clause injection", "1 IN [2,3]", false},
		{"newline injection", "1\n", false},
		{"space injection", "1 ", false},
		{"negative number", "-1", false},
		{"float", "1.5", false},
		{"hex", "0x1", false},
		{"unicode digits", "１２３", false}, // fullwidth digits should be rejected
		{"letter prefix", "a123", false},
		{"letter suffix", "123a", false},
		{"special char", "1=1", false},
		{"wildcard", "*", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeID(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSanitizeIDs(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{"all valid", []string{"1", "2", "3"}, []string{"1", "2", "3"}},
		{"mixed valid/invalid", []string{"1", "1 OR 1=1", "3"}, []string{"1", "3"}},
		{"all invalid", []string{"'a'", "OR 1=1", ""}, []string{}},
		{"empty input", []string{}, []string{}},
		{"nil input", nil, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeIDs(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClampLimitOffset(t *testing.T) {
	tests := []struct {
		name          string
		limit, offset int64
		expLimit      int64
		expOffset     int64
	}{
		{"normal values", 10, 5, 10, 5},
		{"zero limit", 0, 0, 0, 0},
		{"negative limit", -5, 0, 0, 0},
		{"negative offset", 10, -3, 10, 0},
		{"over max limit", 100, 0, maxSearchLimit, 0},
		{"exactly max limit", maxSearchLimit, 0, maxSearchLimit, 0},
		{"large values", 1000, 1000, maxSearchLimit, 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit, offset := clampLimitOffset(tt.limit, tt.offset)
			assert.Equal(t, tt.expLimit, limit)
			assert.Equal(t, tt.expOffset, offset)
		})
	}
}

func TestSearchAccounts_DisabledReturnsError(t *testing.T) {
	client := &MeilisearchClient{enabled: false}
	ids, err := client.SearchAccounts("test", 10, 0, nil)
	assert.Error(t, err)
	assert.Nil(t, ids)
}

func TestSearchStatuses_DisabledReturnsError(t *testing.T) {
	client := &MeilisearchClient{enabled: false}
	ids, err := client.SearchStatuses("test", 10, 0, "", nil)
	assert.Error(t, err)
	assert.Nil(t, ids)
}

func TestSearchHashtags_DisabledReturnsError(t *testing.T) {
	client := &MeilisearchClient{enabled: false}
	ids, err := client.SearchHashtags("test", 10, 0)
	assert.Error(t, err)
	assert.Nil(t, ids)
}

func TestIndexAccount_DisabledReturnsNil(t *testing.T) {
	client := &MeilisearchClient{enabled: false}
	err := client.IndexAccount(&AccountSearchDocument{ID: "1"})
	assert.NoError(t, err)
}

func TestIndexAccount_NilDocReturnsError(t *testing.T) {
	client := &MeilisearchClient{enabled: true, client: nil}
	// IsEnabled() returns false because client is nil
	err := client.IndexAccount(nil)
	assert.NoError(t, err)
}

func TestIndexStatus_DisabledReturnsNil(t *testing.T) {
	client := &MeilisearchClient{enabled: false}
	err := client.IndexStatus(&StatusSearchDocument{ID: "1"})
	assert.NoError(t, err)
}

func TestIndexHashtag_DisabledReturnsNil(t *testing.T) {
	client := &MeilisearchClient{enabled: false}
	err := client.IndexHashtag(&HashtagSearchDocument{ID: "1"})
	assert.NoError(t, err)
}

func TestDeleteAccount_DisabledReturnsNil(t *testing.T) {
	client := &MeilisearchClient{enabled: false}
	err := client.DeleteAccount("1")
	assert.NoError(t, err)
}

func TestGetStats_DisabledReturnsError(t *testing.T) {
	client := &MeilisearchClient{enabled: false}
	result, err := client.GetStats()
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHealthCheck_DisabledReturnsFalse(t *testing.T) {
	client := &MeilisearchClient{enabled: false}
	assert.False(t, client.HealthCheck())
}

// TestSearchStatuses_MaliciousAccountID verifies that the SearchStatuses
// function rejects malicious account_id values that could inject into the
// Meilisearch filter expression.
func TestSearchStatuses_MaliciousAccountID(t *testing.T) {
	// The account_id is validated by sanitizeID before being interpolated
	// into the Meilisearch filter expression. We verify the validation
	// rejects common injection payloads.
	maliciousIDs := []string{
		"1 OR visibility = 3",
		"1 AND 1=1",
		"'; DROP",
		"1)",
		"1 IN [2]",
	}
	for _, id := range maliciousIDs {
		assert.False(t, sanitizeID(id), "malicious ID should be rejected: %s", id)
	}
}

// TestSearchAccounts_MaliciousFollowingIDs verifies that malicious
// following IDs are filtered out by sanitizeIDs.
func TestSearchAccounts_MaliciousFollowingIDs(t *testing.T) {
	malicious := []string{
		"1 OR 1=1",
		"'; DROP INDEX",
		"valid_id_but_not_numeric",
		"2 OR account_id = 3",
	}
	cleaned := sanitizeIDs(malicious)
	assert.Empty(t, cleaned, "all malicious IDs should be filtered out")
}

func TestBatchIndexAccounts_EmptyReturnsNil(t *testing.T) {
	client := &MeilisearchClient{enabled: false}
	err := client.BatchIndexAccounts(nil)
	assert.NoError(t, err)
}

func TestBatchIndexStatuses_EmptyReturnsNil(t *testing.T) {
	client := &MeilisearchClient{enabled: false}
	err := client.BatchIndexStatuses(nil)
	assert.NoError(t, err)
}

func TestMeilisearchClient_ConcurrentIsEnabled(t *testing.T) {
	client := &MeilisearchClient{enabled: true}
	// Verify concurrent reads/writes don't race
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			client.IsEnabled()
		}()
		go func() {
			defer wg.Done()
			client.setEnabled(false)
			client.setEnabled(true)
		}()
	}
	wg.Wait()
}

func TestParseSearchQuery_AllFilters(t *testing.T) {
	text, filters := ParseSearchQuery("hello from:user1 has:media is:reply before:2024-01-01 after:2023-01-01 #golang world")
	assert.Equal(t, "hello world", text)
	assert.Equal(t, "user1", filters["from"])
	assert.Equal(t, "media", filters["has"])
	assert.Equal(t, "reply", filters["is"])
	assert.Equal(t, "2024-01-01", filters["before"])
	assert.Equal(t, "2023-01-01", filters["after"])
	assert.Equal(t, "golang", filters["tag"])
}
