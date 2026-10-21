package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAccessGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	run := func(token, cidrs, auth, remote string) int {
		r := gin.New()
		r.GET("/m", AccessGuard(token, cidrs), func(c *gin.Context) { c.Status(200) })
		req := httptest.NewRequest(http.MethodGet, "/m", nil)
		req.RemoteAddr = remote + ":1234"
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	cases := []struct {
		token, cidrs, auth, ip string
		want                   int
	}{
		{"", "", "", "8.8.8.8", 200},
		{"s3cret", "", "", "8.8.8.8", 404},
		{"s3cret", "", "Bearer wrong", "8.8.8.8", 404},
		{"s3cret", "", "Bearer s3cret", "8.8.8.8", 200},
		{"", "10.0.0.0/8", "", "10.1.2.3", 200},
		{"", "10.0.0.0/8", "", "8.8.8.8", 404},
		{"", "192.0.2.7", "", "192.0.2.7", 200},
	}
	for _, tc := range cases {
		if got := run(tc.token, tc.cidrs, tc.auth, tc.ip); got != tc.want {
			t.Errorf("%+v: got %d", tc, got)
		}
	}
}
