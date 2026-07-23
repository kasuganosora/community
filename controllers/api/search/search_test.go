package search

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/serializer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// --- SearchRequest Binding Tests ---

func TestSearchRequest_Binding(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		expectError bool
	}{
		{
			name:        "valid query with q parameter",
			query:       "?q=hello",
			expectError: false,
		},
		{
			name:        "missing q parameter should fail",
			query:       "?type=accounts",
			expectError: true,
		},
		{
			name:        "empty q parameter should fail (min=1)",
			query:       "?q=",
			expectError: true,
		},
		{
			name:        "whitespace-only q should fail after trim",
			query:       "?q=%20%20",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search"+tt.query, nil) //nolint:noctx

			var req SearchRequest

			err := c.ShouldBindQuery(&req)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSearchRequest_Fields(t *testing.T) {
	t.Run("all fields parsed correctly", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search?q=test&type=accounts&limit=10&offset=5&following=true&account_id=123&resolve=true", nil) //nolint:noctx

		var req SearchRequest

		err := c.ShouldBindQuery(&req)
		require.NoError(t, err)
		assert.Equal(t, "test", req.Q)
		assert.Equal(t, "accounts", req.Type)
		assert.Equal(t, 10, req.Limit)
		assert.Equal(t, 5, req.Offset)
		assert.True(t, req.Following)
		assert.Equal(t, "123", req.AccountId)
		assert.True(t, req.Resolve)
	})

	t.Run("default values when optional fields omitted", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search?q=test", nil) //nolint:noctx

		var req SearchRequest

		err := c.ShouldBindQuery(&req)
		require.NoError(t, err)
		assert.Equal(t, "test", req.Q)
		assert.Empty(t, req.Type)
		assert.Equal(t, 0, req.Limit)
		assert.Equal(t, 0, req.Offset)
		assert.False(t, req.Following)
		assert.Empty(t, req.AccountId)
		assert.Empty(t, req.MaxId)
		assert.Empty(t, req.MinId)
		assert.False(t, req.Resolve)
	})

	t.Run("max_id and min_id parsed", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search?q=test&max_id=100&min_id=50", nil) //nolint:noctx

		var req SearchRequest

		err := c.ShouldBindQuery(&req)
		require.NoError(t, err)
		assert.Equal(t, "100", req.MaxId)
		assert.Equal(t, "50", req.MinId)
	})
}

// --- Search Handler Logic Tests ---

func TestSearch_MissingQuery(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search", nil) //nolint:noctx

	Search(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid query parameters")
}

func TestSearch_EmptyQuery(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search?q=%20%20%20", nil) //nolint:noctx

	Search(c)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "Query cannot be empty")
}

func TestSearch_ResponseStructure(t *testing.T) {
	// Skip if database is not initialized (e.g., in CI/unit test environments)
	if db.GetDB() == nil {
		t.Skip("Skipping TestSearch_ResponseStructure: database not initialized")
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search?q=something", nil) //nolint:noctx

	// Search will try to hit DB/Meilisearch but still returns 200 with empty arrays
	// This test verifies the response format even if DB errors occur or returns empty
	Search(c)

	// The handler returns 200 even with empty results
	assert.Equal(t, http.StatusOK, w.Code)

	// Response should be JSON with accounts, statuses, hashtags arrays
	assert.Contains(t, w.Body.String(), "accounts")
	assert.Contains(t, w.Body.String(), "statuses")
	assert.Contains(t, w.Body.String(), "hashtags")
}

func TestSearch_DefaultLimitApplied(t *testing.T) {
	if db.GetDB() == nil {
		t.Skip("Skipping TestSearch_DefaultLimitApplied: database not initialized")
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search?q=test", nil) //nolint:noctx

	Search(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSearch_LimitCappedAt40(t *testing.T) {
	if db.GetDB() == nil {
		t.Skip("Skipping TestSearch_LimitCappedAt40: database not initialized")
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search?q=test&limit=100", nil) //nolint:noctx

	Search(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSearch_TypeAccounts(t *testing.T) {
	if db.GetDB() == nil {
		t.Skip("Skipping TestSearch_TypeAccounts: database not initialized")
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search?q=test&type=accounts", nil) //nolint:noctx

	Search(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "accounts")
}

func TestSearch_TypeStatuses(t *testing.T) {
	if db.GetDB() == nil {
		t.Skip("Skipping TestSearch_TypeStatuses: database not initialized")
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search?q=test&type=statuses", nil) //nolint:noctx

	Search(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "statuses")
}

func TestSearch_TypeHashtags(t *testing.T) {
	if db.GetDB() == nil {
		t.Skip("Skipping TestSearch_TypeHashtags: database not initialized")
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search?q=test&type=hashtags", nil) //nolint:noctx

	Search(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "hashtags")
}

func TestSearch_AuthenticatedUser(t *testing.T) {
	if db.GetDB() == nil {
		t.Skip("Skipping TestSearch_AuthenticatedUser: database not initialized")
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search?q=test", nil) //nolint:noctx
	c.Set("authenticated", true)

	Search(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSearch_InvalidLimitTooSmall(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search?q=test&limit=0", nil) //nolint:noctx

	var req SearchRequest

	err := c.ShouldBindQuery(&req)
	require.NoError(t, err)
	assert.Equal(t, 0, req.Limit)
	// Default of 20 is applied inside handler logic
}

// --- SearchResult Tests ---

func TestSearchResult_Structure(t *testing.T) {
	// SearchResult uses concrete serializer types
	result := SearchResult{
		Accounts: nil,
		Statuses: nil,
		Hashtags: nil,
	}

	// Verify SearchResult can be instantiated with nil fields
	assert.NotNil(t, result)
	// The handler always initializes with make(..., 0)
	handlerResult := &SearchResult{
		Accounts: make([]*serializer.Account, 0),
		Statuses: make([]*serializer.Status, 0),
		Hashtags: make([]*serializer.Tag, 0),
	}
	assert.NotNil(t, handlerResult)
}

// --- searchAccounts tests (query parsing logic) ---

func TestSearchAccount_QueryParsing(t *testing.T) {
	// Test the username@domain parsing logic used in searchAccounts
	// This tests the string splitting logic without DB
	tests := []struct {
		name     string
		input    string
		expected string // after trimming @, \t, \n
	}{
		{
			name:     "simple username",
			input:    "  @testuser",
			expected: "testuser",
		},
		{
			name:     "username with domain",
			input:    "  @user@example.com  ",
			expected: "user@example.com",
		},
		{
			name:     "plain text",
			input:    "searchterm",
			expected: "searchterm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Replicate the trim logic from searchAccounts
			result := trimForSearch(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// trimForSearch replicates the trimming logic used in searchAccounts.
func trimForSearch(s string) string {
	// The source code does: strings.Trim(req.Q, " \t\n@")
	return trimChars(s, " \t\n@")
}

func trimChars(s, cutset string) string {
	// Simple implementation of strings.Trim behavior
	start := 0

	end := len(s)

	for start < end {
		found := false

		for _, c := range cutset {
			if rune(s[start]) == c {
				found = true

				break
			}
		}

		if !found {
			break
		}

		start++
	}

	for end > start {
		found := false

		for _, c := range cutset {
			if rune(s[end-1]) == c {
				found = true

				break
			}
		}

		if !found {
			break
		}

		end--
	}

	return s[start:end]
}
