package account

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBindOptionalBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newCtx := func(body, contentType string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/api/v1/accounts/1/follow", strings.NewReader(body)) //nolint:noctx
		if contentType != "" {
			c.Request.Header.Set("Content-Type", contentType)
		}

		return c
	}

	// No body at all (what the web UI and most Mastodon clients send).
	var req FollowRequest
	require.NoError(t, bindOptionalBody(newCtx("", ""), &req))

	req = FollowRequest{}
	require.NoError(t, bindOptionalBody(newCtx(`{"reblogs":true}`, "application/json"), &req))
	assert.True(t, req.Reblogs)

	req = FollowRequest{}
	require.NoError(t, bindOptionalBody(newCtx("reblogs=true", "application/x-www-form-urlencoded"), &req))
	assert.True(t, req.Reblogs)

	assert.Error(t, bindOptionalBody(newCtx("{bad", "application/json"), &FollowRequest{}))
}
