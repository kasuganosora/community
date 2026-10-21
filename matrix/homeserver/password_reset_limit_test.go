package homeserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPasswordResetIPLimitAllowsBurstOfSix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/rt", newPasswordResetIPLimit(nil), func(c *gin.Context) { c.Status(http.StatusOK) })

	do := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/rt", nil)
		req.RemoteAddr = "203.0.113.7:1234"
		r.ServeHTTP(w, req)
		return w
	}
	for i := 1; i <= 6; i++ {
		require.Equal(t, http.StatusOK, do().Code, "request %d", i)
	}
	w := do()
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, "60", w.Header().Get("Retry-After"))
}
