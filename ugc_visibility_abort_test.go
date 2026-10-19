package community

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/stretchr/testify/require"
)

// A handler that aborts with controllers.AbortPanic (GetIdFromContext on a
// missing or malformed id) must reach a logged-out visitor with its status
// and body, not as an empty 200.
func TestUGCVisitorGuardFlushesAbortedResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(controllers.AbortRecoveryMiddleware())
	r.GET("/api/v1/things/:id", UGCVisitorGuard(), func(c *gin.Context) {
		c.AbortWithStatusJSON(404, gin.H{"error": "not found"})
		controllers.AbortPanic()
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/things/abc", nil))
	require.Equal(t, 404, w.Code)
	require.JSONEq(t, `{"error":"not found"}`, w.Body.String())
}
