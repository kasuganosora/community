package streaming

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/kasuganosora/community/feed/public"
	"github.com/kasuganosora/community/models"
)

// skipSSEPadding consumes the initial padding comment and its blank line.
func skipSSEPadding(t *testing.T, scanner *bufio.Scanner) {
	t.Helper()
	require.True(t, scanner.Scan())
	require.True(t, strings.HasPrefix(scanner.Text(), ":"), "expected padding comment, got %q", scanner.Text())
	require.True(t, scanner.Scan())
	require.Empty(t, scanner.Text())
}

func TestSSEPaddingAndHeartbeat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupRedisForTest(t)
	setupTestDB(t)
	previous := sseHeartbeatInterval
	sseHeartbeatInterval = 40 * time.Millisecond
	t.Cleanup(func() { sseHeartbeatInterval = previous })

	router := gin.New()
	router.GET("/stream", func(c *gin.Context) {
		account := &models.Account{Id: 1}
		startSseService(c, func(data any) (string, string, error) {
			return serializerStatusEvent(account, data.(string)) //nolint:forcetypeassert
		}, &public.PublicTimeline{})
	})
	server := httptest.NewServer(router)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/stream", nil)
	require.NoError(t, err)
	request.Header.Set("Accept-Encoding", "gzip")
	response, err := http.DefaultTransport.RoundTrip(request)
	require.NoError(t, err)
	defer response.Body.Close()

	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
	require.Equal(t, "no-cache, no-transform", response.Header.Get("Cache-Control"))
	require.Equal(t, "no", response.Header.Get("X-Accel-Buffering"))
	require.Empty(t, response.Header.Get("Content-Encoding"))

	// The padding must arrive before any event exists, i.e. it was flushed.
	scanner := bufio.NewScanner(response.Body)
	require.True(t, scanner.Scan())
	padding := scanner.Text()
	require.True(t, strings.HasPrefix(padding, ":"))
	require.GreaterOrEqual(t, len(padding), 2048)
	require.Empty(t, strings.TrimSpace(padding[1:]))
	require.True(t, scanner.Scan())
	require.Empty(t, scanner.Text())

	require.True(t, scanner.Scan())
	require.Equal(t, ":thump", scanner.Text())
}
