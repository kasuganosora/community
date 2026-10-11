package middleware

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// A handler that ignores its context and outlives timeoutGrace used to be
// abandoned: gin recycled the Context, and the stale goroutine's late c.JSON
// landed on whichever request reused it. Against a real net/http server that
// shows up as "wrote more than the declared Content-Length" (the 504 declares
// a length) or as a foreign body on an unrelated endpoint.
func TestStaleHandlerCannotWriteIntoReusedContext(t *testing.T) {
	oldGrace, oldHard := timeoutGrace, timeoutHardLimit
	timeoutGrace, timeoutHardLimit = 80*time.Millisecond, 5*time.Second
	defer func() { timeoutGrace, timeoutHardLimit = oldGrace, oldHard }()

	gin.SetMode(gin.TestMode)
	var lateWriteDone atomic.Bool
	var returnedBeforeLateWrite atomic.Bool
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Next()
		if c.Request.URL.Path == "/slow" && !lateWriteDone.Load() {
			returnedBeforeLateWrite.Store(true)
		}
	})
	r.Use(TimeoutMiddleware(30 * time.Millisecond))
	r.POST("/slow", func(c *gin.Context) {
		time.Sleep(300 * time.Millisecond) // ignores c.Request.Context()
		c.JSON(200, gin.H{"room_id": "!late:example.org", "pad": strings.Repeat("x", 4096)})
		lateWriteDone.Store(true)
	})
	r.POST("/inbox", func(c *gin.Context) { c.JSON(202, gin.H{"status": "ok"}) })

	var logBuf bytes.Buffer
	var logMu sync.Mutex
	srv := httptest.NewUnstartedServer(r)
	srv.Config.ErrorLog = log.New(writerFunc(func(p []byte) (int, error) {
		logMu.Lock()
		defer logMu.Unlock()
		return logBuf.Write(p)
	}), "", 0)
	srv.Start()
	defer srv.Close()

	client := srv.Client()
	resp, err := client.Post(srv.URL+"/slow", "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusGatewayTimeout, resp.StatusCode)
	require.Contains(t, string(body), "request timeout")
	require.NotContains(t, string(body), "late")

	// The middleware only returned after the stale handler was done with the Context.
	// (The 504 is flushed first; the handler finishes behind it.)
	require.Eventually(t, lateWriteDone.Load, 3*time.Second, 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	require.False(t, returnedBeforeLateWrite.Load(), "middleware must wait for the handler before the Context is recycled")

	for i := 0; i < 20; i++ {
		resp, err = client.Post(srv.URL+"/inbox", "application/json", strings.NewReader("{}"))
		require.NoError(t, err)
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		require.Equal(t, 202, resp.StatusCode)
		require.JSONEq(t, `{"status":"ok"}`, string(body))
	}
	logMu.Lock()
	defer logMu.Unlock()
	require.NotContains(t, logBuf.String(), "Content-Length")
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
