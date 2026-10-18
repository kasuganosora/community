package middleware

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// A client that hangs up early is not a server timeout: record 499, not 504.
func TestClientDisconnectIsNotReportedAsGatewayTimeout(t *testing.T) {
	r := timeoutEngine(10*time.Second, func(c *gin.Context) {
		<-c.Request.Context().Done()
	})
	var status int
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "/inbox", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	start := time.Now()
	r.ServeHTTP(w, req)
	status = w.Code
	require.Less(t, time.Since(start), 5*time.Second)
	require.Equal(t, StatusClientClosedRequest, status)
}

// A handler still setting headers when the client hangs up must not race the
// middleware's own bookkeeping (a concurrent map write crashes the process).
func TestClientDisconnectDoesNotRaceHandlerHeaders(t *testing.T) {
	r := timeoutEngine(10*time.Second, func(c *gin.Context) {
		h := c.Writer.Header()
		deadline := time.Now().Add(80 * time.Millisecond)
		for i := 0; time.Now().Before(deadline); i++ {
			h.Set("X-Progress", time.Now().String())
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/v1/timelines/home", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	r.ServeHTTP(w, req) // run with -race: must not report a data race or crash
}
