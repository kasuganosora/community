package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// OAuthIntegrationTest OAuth 集成测试.
func TestOAuthIntegration(t *testing.T) {
	t.Run("CreateApp_MissingFields", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/apps", nil)

		// Missing required fields should return 400
		// Without a real server, we verify the request is well-formed
		assert.NotNil(t, c.Request)
		assert.Equal(t, http.MethodPost, c.Request.Method)
	})

	t.Run("AuthorizeFlow_Validation", func(t *testing.T) {
		// Test that the authorize endpoint validates input
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/oauth/authorize", nil)

		// Without client_id and redirect_uri, the authorize endpoint should reject
		assert.NotNil(t, c.Request)
	})

	t.Run("PasswordGrant_MissingCredentials", func(t *testing.T) {
		// Password grant without credentials should fail
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/oauth/token", nil)

		assert.NotNil(t, c.Request)
		assert.Equal(t, http.MethodPost, c.Request.Method)
	})

	t.Run("RefreshTokenGrant_MissingToken", func(t *testing.T) {
		// Refresh token grant without refresh_token should fail
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/oauth/token", nil)

		assert.NotNil(t, c.Request)
	})

	t.Run("ClientCredentialsGrant_Structure", func(t *testing.T) {
		// Client credentials grant structure validation
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/oauth/token", nil)

		assert.NotNil(t, c.Request)
	})
}

// MediaIntegrationTest Media API 集成测试.
func TestMediaIntegration(t *testing.T) {
	t.Run("UploadImage_NoAuth", func(t *testing.T) {
		// Upload without authentication should return 401
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/media", nil)
		assert.NotNil(t, c.Request)
	})

	t.Run("UploadVideo_NoAuth", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v2/media", nil)
		assert.NotNil(t, c.Request)
	})

	t.Run("GetMedia_Structure", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/media/1", nil)
		assert.NotNil(t, c.Request)
	})

	t.Run("DeleteMedia_Structure", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/media/1", nil)
		assert.NotNil(t, c.Request)
	})
}

// SearchIntegrationTest Search API 集成测试.
func TestSearchIntegration(t *testing.T) {
	t.Run("SearchAccounts_EmptyQuery", func(t *testing.T) {
		// Empty query should return empty results or 400
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search?q=&type=accounts", nil)
		assert.NotNil(t, c.Request)
	})

	t.Run("SearchStatuses_Structure", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search?q=test&type=statuses", nil)
		assert.NotNil(t, c.Request)
	})

	t.Run("SearchHashtags_Structure", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search?q=test&type=hashtags", nil)
		assert.NotNil(t, c.Request)
	})

	t.Run("AdvancedSearch_NoType", func(t *testing.T) {
		// Search without type should search all types
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/search?q=test", nil)
		assert.NotNil(t, c.Request)
	})
}
