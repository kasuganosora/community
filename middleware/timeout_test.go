package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func timeoutEngine(timeout time.Duration, h gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery(), TimeoutMiddleware(timeout))
	r.POST("/inbox", h)
	r.GET("/ok", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	return r
}

func TestTimeoutPassesNormalResponses(t *testing.T) {
	r := timeoutEngine(time.Second, func(c *gin.Context) { c.JSON(202, gin.H{"status": "ok"}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/ok", nil))
	require.Equal(t, 200, w.Code)
	require.JSONEq(t, `{"ok":true}`, w.Body.String())
}

func TestHandlerPanicBecomes500WithoutHanging(t *testing.T) {
	r := timeoutEngine(time.Second, func(c *gin.Context) {
		var m map[string]int
		m["x"] = 1 // nil map write
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/inbox", nil))
	require.Equal(t, 500, w.Code)

	// The engine keeps serving afterwards (the Context was not corrupted).
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/ok", nil))
	require.Equal(t, 200, w.Code)
}

// The handler ignores the deadline, then answers: the client must still get
// exactly one clean 504, with a Content-Length that matches its body.
func TestLateHandlerWriteDoesNotReachTheFinishedResponse(t *testing.T) {
	var after atomic.Int32
	var finishedAt atomic.Int64
	r := timeoutEngine(50*time.Millisecond, func(c *gin.Context) {
		<-c.Request.Context().Done()
		time.Sleep(100 * time.Millisecond)
		c.Header("X-Late", "1")
		c.JSON(202, gin.H{"status": "a much longer body than the timeout response, written too late"})
		c.Writer.Flush()
		after.Add(1)
		finishedAt.Store(time.Now().UnixNano())
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/inbox", "application/activity+json", nil)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err, "no 'wrote more than the declared Content-Length' truncation")
	require.Equal(t, 504, resp.StatusCode)
	require.JSONEq(t, `{"error":"request timeout","message":"The request took too long to process"}`, string(body))
	require.Empty(t, resp.Header.Get("X-Late"))
	require.EqualValues(t, len(body), resp.ContentLength)

	// The handler finished before the middleware returned, so the Context
	// was never reused while it was still running.
	require.Eventually(t, func() bool { return after.Load() == 1 }, 2*time.Second, 10*time.Millisecond)

	// The connection is still good for the next request.
	resp, err = http.Get(srv.URL + "/ok")
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
}

func TestTimeoutAfterTheResponseStartedDoesNotAppend504(t *testing.T) {
	r := timeoutEngine(50*time.Millisecond, func(c *gin.Context) {
		c.Status(202)
		_, _ = c.Writer.Write([]byte("partial"))
		c.Writer.Flush()
		<-c.Request.Context().Done()
		time.Sleep(50 * time.Millisecond) // after the middleware has acted on the timeout
		_, _ = c.Writer.Write([]byte("-late"))
	})
	srv := httptest.NewServer(r)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/inbox", "text/plain", nil)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, 202, resp.StatusCode)
	require.Equal(t, "partial", string(body))
}

func TestPanicAfterTimeoutIsContained(t *testing.T) {
	r := timeoutEngine(30*time.Millisecond, func(c *gin.Context) {
		<-c.Request.Context().Done()
		var p *struct{ x int }
		_ = p.x
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/inbox", nil))
	require.Equal(t, 504, w.Code)
	require.Contains(t, w.Body.String(), "request timeout")
}

func TestStillRunningHandlerWriterIsNeverTheRealOne(t *testing.T) {
	release := make(chan struct{})
	var wasWrapped atomic.Bool
	r := timeoutEngine(30*time.Millisecond, func(c *gin.Context) {
		<-c.Request.Context().Done()
		_, ok := c.Writer.(*timeoutResponseWriter)
		wasWrapped.Store(ok)
		close(release)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/inbox", nil))
	<-release
	require.True(t, wasWrapped.Load())
	require.Equal(t, 504, w.Code)
}
