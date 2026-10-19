package secretredact

import (
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCollectAndApply(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/streaming?access_token=query-token-123456&stream=user", nil)
	r.Header.Set("Authorization", "Bearer header-token-123456")
	r.Header.Set("Sec-WebSocket-Protocol", "proto-token-1234567")
	got := Collect(r)
	require.Contains(t, got, "header-token-123456")
	require.Contains(t, got, "query-token-123456")
	require.Contains(t, got, "proto-token-1234567")
	out := Apply("a header-token-123456 b query-token-123456 c", got)
	require.Equal(t, "a [REDACTED] b [REDACTED] c", out)
	require.Empty(t, Collect(httptest.NewRequest("GET", "/?access_token=short", nil)))
}

func TestDecodeBearerSubprotocol(t *testing.T) {
	token, ok := DecodeBearerSubprotocol("community.bearer.YStiL2M9")
	if !ok || token != "a+b/c=" {
		t.Fatalf("got %q %v", token, ok)
	}
	for _, v := range []string{"community.v1", "community.bearer.", "community.bearer.***", "YStiL2M9"} {
		if _, ok := DecodeBearerSubprotocol(v); ok {
			t.Fatalf("%q should not decode", v)
		}
	}
}

func TestMiddlewareScrubsResponseHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	token := "response-header-secret-token"
	encoded := BearerSubprotocolPrefix + base64.RawURLEncoding.EncodeToString([]byte(token))
	r := gin.New()
	r.Use(Middleware())
	r.GET("/ok", func(c *gin.Context) {
		c.Header("X-Debug", "auth="+token)
		c.Header("Sec-WebSocket-Protocol", encoded)
		c.Header("X-Public", PublicSubprotocol)
		c.SetCookie("app-token", token, 60, "/", "", true, true)
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("Sec-WebSocket-Protocol", PublicSubprotocol+", "+encoded)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	for name, values := range w.Header() {
		for _, v := range values {
			if name == "Set-Cookie" {
				continue
			}
			if strings.Contains(v, token) || strings.Contains(v, encoded) {
				t.Fatalf("secret leaked in %s: %q", name, v)
			}
		}
	}
	if got := w.Header().Get("X-Public"); got != PublicSubprotocol {
		t.Fatalf("public subprotocol was redacted: %q", got)
	}
	if !strings.Contains(w.Header().Get("Set-Cookie"), token) {
		t.Fatal("Set-Cookie must be left intact")
	}
}
