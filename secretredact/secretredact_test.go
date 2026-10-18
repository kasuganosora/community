package secretredact

import (
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
