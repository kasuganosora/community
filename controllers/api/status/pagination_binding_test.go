package status

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFavouritedByRequest_BindsQueryParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/?max_id=9&since_id=3&limit=2&offset=1", nil) //nolint:noctx

	var req FavouritedByRequest
	require.NoError(t, c.ShouldBindQuery(&req))
	assert.Equal(t, FavouritedByRequest{MaxId: 9, SinceId: 3, Limit: 2, Offset: 1}, req)
}
