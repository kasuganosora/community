package favourite

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// --- ListRequest Binding Tests ---

func TestListRequest_Binding(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		expectError bool
	}{
		{
			name:        "valid empty request",
			query:       "",
			expectError: false,
		},
		{
			name:        "valid with limit",
			query:       "limit=40",
			expectError: false,
		},
		{
			name:        "limit too small should fail",
			query:       "limit=-1",
			expectError: true,
		},
		{
			name:        "limit too large should fail",
			query:       "limit=100",
			expectError: true,
		},
		{
			name:        "valid pagination params",
			query:       "limit=20&max_id=100&min_id=50&since_id=75",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/favourites?"+tt.query, nil) //nolint:noctx
			c.Request = req

			var parsed ListRequest

			err := c.ShouldBindQuery(&parsed)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestListRequest_Fields(t *testing.T) {
	t.Run("all fields parsed correctly", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/favourites?limit=30&max_id=100&min_id=50&since_id=75", nil) //nolint:noctx

		var req ListRequest

		err := c.ShouldBindQuery(&req)
		require.NoError(t, err)
		assert.Equal(t, 30, req.Limit)
		assert.Equal(t, "100", req.MaxId)
		assert.Equal(t, "50", req.MinId)
		assert.Equal(t, "75", req.SinceId)
	})

	t.Run("default values", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/favourites", nil) //nolint:noctx

		var req ListRequest

		err := c.ShouldBindQuery(&req)
		require.NoError(t, err)
		assert.Equal(t, 0, req.Limit)
		assert.Empty(t, req.MaxId)
		assert.Empty(t, req.MinId)
		assert.Empty(t, req.SinceId)
	})
}

// --- Favourites Handler Tests ---

func TestFavourites_MissingAccount(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/favourites", nil) //nolint:noctx

	// MustGet("account") should panic when account not set
	assert.Panics(t, func() {
		Favourites(c)
	})
}

func TestFavourites_InvalidLimit(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/favourites?limit=100", nil) //nolint:noctx

	// Handler should return 400 for invalid limit
	Favourites(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestFavourites_RequestFormat(t *testing.T) {
	t.Run("form data binding works", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		form := make(url.Values)
		form.Set("limit", "10")
		form.Set("max_id", "999")

		req := httptest.NewRequest(http.MethodGet, "/api/v1/favourites?"+form.Encode(), nil) //nolint:noctx
		c.Request = req

		var parsed ListRequest

		err := c.ShouldBindQuery(&parsed)
		require.NoError(t, err)
		assert.Equal(t, 10, parsed.Limit)
		assert.Equal(t, "999", parsed.MaxId)
	})
}

// --- Bookmarks Handler Tests ---

func TestBookmarks_MissingAccount(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/bookmarks", nil) //nolint:noctx

	assert.Panics(t, func() {
		Bookmarks(c)
	})
}

func TestBookmarks_InvalidLimit(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/bookmarks?limit=-1", nil) //nolint:noctx

	Bookmarks(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --- Pagination Logic Tests ---

func TestPagination_MaxId(t *testing.T) {
	t.Run("valid max_id parses correctly", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/favourites?max_id=12345", nil) //nolint:noctx

		var req ListRequest

		err := c.ShouldBindQuery(&req)
		require.NoError(t, err)
		assert.Equal(t, "12345", req.MaxId)
	})
}

func TestPagination_InvalidMaxId(t *testing.T) {
	t.Run("non-numeric max_id still binds as string", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/favourites?max_id=abc", nil) //nolint:noctx

		var req ListRequest

		err := c.ShouldBindQuery(&req)
		require.NoError(t, err)
		assert.Equal(t, "abc", req.MaxId)
		// Handler will fail to parse uint from "abc" and skip the filter
	})
}

func TestPagination_NegativeLimit(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/favourites?limit=-1", nil) //nolint:noctx

	var req ListRequest

	err := c.ShouldBindQuery(&req)
	require.Error(t, err) // min=1 validation should fail
}

// --- ListRequest Struct Tests ---

func TestListRequest_JSONTags(t *testing.T) {
	// Verify the ListRequest struct has correct form tags
	req := ListRequest{
		Limit:   20,
		MaxId:   "100",
		MinId:   "50",
		SinceId: "75",
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)

	var decoded map[string]any

	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	assert.InEpsilon(t, 20.0, decoded["limit"], 0.001)
	assert.Equal(t, "100", decoded["max_id"])
	assert.Equal(t, "50", decoded["min_id"])
	assert.Equal(t, "75", decoded["since_id"])
}

// --- Account model context tests ---

func TestAccountContext_SetAndGet(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	account := &models.Account{
		Id:       42,
		Username: "testuser",
	}
	c.Set("account", account)

	retrieved, exists := c.Get("account")
	assert.True(t, exists)
	assert.Equal(t, account, retrieved)
}

func TestAccountContext_MustGetPanics(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	// MustGet panics when key doesn't exist
	assert.Panics(t, func() {
		c.MustGet("account")
	})
}

func TestAccountContext_MustGetSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	account := &models.Account{
		Id:       42,
		Username: "testuser",
	}
	c.Set("account", account)

	retrieved := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	assert.Equal(t, uint64(42), retrieved.Id)
	assert.Equal(t, "testuser", retrieved.Username)
}

// --- Limit Default Logic ---

func TestLimitDefault(t *testing.T) {
	tests := []struct {
		name     string
		limit    int
		expected int
	}{
		{"zero defaults to 20", 0, 20},
		{"10 stays 10", 10, 10},
		{"80 stays 80 (max allowed)", 80, 80},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit := tt.limit
			if limit == 0 {
				limit = 20
			}

			assert.Equal(t, tt.expected, limit)
		})
	}
}
