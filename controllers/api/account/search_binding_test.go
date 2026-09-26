package account

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Gin binds query parameters via the `form` tag; the previous `query` tags
// were ignored, so ?q= never reached Keyword and every search returned 400.
func TestSearchRequest_BindsQueryParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/v1/accounts/search?q=alice&limit=1&offset=2&following=true", nil) //nolint:noctx

	var req SearchRequest
	require.NoError(t, c.ShouldBindQuery(&req))
	assert.Equal(t, "alice", req.Keyword)
	assert.Equal(t, 1, req.Limit)
	assert.Equal(t, 2, req.Offset)
	assert.True(t, req.Following)

	// limit is optional (defaults to 4 in the handler)
	c.Request = httptest.NewRequest("GET", "/api/v1/accounts/search?q=%E4%B8%AD%E6%96%87", nil) //nolint:noctx
	req = SearchRequest{}
	require.NoError(t, c.ShouldBindQuery(&req))
	assert.Equal(t, "中文", req.Keyword)
}
