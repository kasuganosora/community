package list

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRemoveAccountIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name, target, body, ctype string
		want                      []string
	}{
		{"query array", "/lists/1/accounts?account_ids[]=7&account_ids[]=8", "", "", []string{"7", "8"}},
		{"query plain", "/lists/1/accounts?account_ids=8", "", "", []string{"8"}},
		{"json", "/lists/1/accounts", `{"account_ids":["10","12"]}`, "application/json", []string{"10", "12"}},
		{"form", "/lists/1/accounts", "account_ids[]=13&account_ids[]=17", "application/x-www-form-urlencoded", []string{"13", "17"}},
		{"missing", "/lists/1/accounts", "", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodDelete, tc.target, strings.NewReader(tc.body)) //nolint:noctx
			if tc.ctype != "" {
				c.Request.Header.Set("Content-Type", tc.ctype)
			}
			if got := removeAccountIDs(c); !slices.Equal(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
