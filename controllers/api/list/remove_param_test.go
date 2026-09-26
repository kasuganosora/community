package list

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRemoveAccountIdParam(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name, target, body, ctype, want string
	}{
		{"query array", "/lists/1/accounts?account_ids[]=7", "", "", "7"},
		{"query plain", "/lists/1/accounts?account_ids=8", "", "", "8"},
		{"json", "/lists/1/accounts", `{"account_ids":["10"]}`, "application/json", "10"},
		{"missing", "/lists/1/accounts", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodDelete, tc.target, strings.NewReader(tc.body)) //nolint:noctx
			if tc.ctype != "" {
				c.Request.Header.Set("Content-Type", tc.ctype)
			}
			if got := removeAccountIdParam(c); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
