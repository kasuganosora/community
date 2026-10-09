package middleware

import (
	"context"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/log"
)

// MatrixSyncHandlerTimeout leaves query and serialization time around a bounded
// 30-second Matrix /sync long poll. It does not depend on client input.
const MatrixSyncHandlerTimeout = 60 * time.Second

const DataTransferHandlerTimeout = 5 * time.Minute

func isDataTransferRequest(request *http.Request) bool {
	return (request.Method == http.MethodPost && (request.URL.Path == "/api/v1/data/imports" || request.URL.Path == "/api/v1/admin/data/imports")) ||
		(request.Method == http.MethodGet && request.URL.Path == "/api/v1/data/export")
}

func requestHandlerTimeout(request *http.Request, fallback time.Duration) time.Duration {
	if isDataTransferRequest(request) {
		return DataTransferHandlerTimeout
	}
	if request.Method == http.MethodGet {
		switch request.URL.Path {
		case "/_matrix/client/v3/sync", "/_matrix/client/r0/sync":
			return MatrixSyncHandlerTimeout
		}
	}
	return fallback
}

// timeoutGrace is how long, after the timeout response has been sent, the
// middleware waits for the handler goroutine to notice the cancelled context
// and return. gin recycles a Context once the request ends, so a goroutine
// that is still running on it would corrupt the next request.
const timeoutGrace = 10 * time.Second

type handlerPanic struct {
	value any
	stack []byte
}

// TimeoutMiddleware 请求超时中间件
//
// The handler chain runs in a goroutine so a timeout response can be sent
// while it is still busy. On timeout the 504 is written straight to the real
// connection; from then on the handler's own writes are discarded (the
// handler keeps seeing the wrapper as c.Writer, never the real writer, so
// nothing it writes later can reach the finished response). The middleware
// then waits for the goroutine before returning, so the gin Context is never
// used after it has been recycled. A panic in the handler is re-raised on the
// request goroutine, with the original stack logged, for gin.Recovery.
func TimeoutMiddleware(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if isDataTransferRequest(c.Request) {
			// Socket deadlines otherwise remain the normal 10s read / 65s
			// write budgets, which would truncate legitimate large backups.
			// Gin exposes Unwrap to the controller; net/http resets deadlines
			// for the next request on this connection.
			controller := http.NewResponseController(c.Writer)
			deadline := time.Now().Add(DataTransferHandlerTimeout + 5*time.Second)
			_ = controller.SetReadDeadline(deadline)
			_ = controller.SetWriteDeadline(deadline)
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), requestHandlerTimeout(c.Request, timeout))
		defer cancel()

		original := c.Writer
		wrapper := &timeoutResponseWriter{ResponseWriter: original}
		c.Writer = wrapper
		c.Request = c.Request.WithContext(ctx)

		finished := make(chan struct{})
		panicChan := make(chan handlerPanic, 1)

		go func() {
			defer func() {
				if p := recover(); p != nil {
					panicChan <- handlerPanic{value: p, stack: debug.Stack()}
				}
			}()

			c.Next()

			close(finished)
		}()

		select {
		case <-finished:
			c.Writer = original
		case p := <-panicChan:
			c.Writer = original
			log.Error("panic in handler: %v\n%s", p.value, p.stack)
			panic(p.value)
		case <-ctx.Done():
			wrapper.expire()

			grace := time.NewTimer(timeoutGrace)
			defer grace.Stop()
			select {
			case <-finished:
			case p := <-panicChan:
				log.Error("panic in handler after timeout: %v\n%s", p.value, p.stack)
			case <-grace.C:
				// Last resort: the chain index must still be closed so the
				// engine does not run the remaining handlers a second time.
				log.Warn("handler still running %s after its timeout: %s %s", timeoutGrace, c.Request.Method, c.Request.URL.Path)
				c.Abort()

				return
			}
			c.Abort()
			c.Writer = original
		}
	}
}

// timeoutResponseWriter is the c.Writer of the handler chain. Writes go
// straight through until the timeout; expire then sends the 504 itself (if the
// handler had not started answering) and turns every later write into a no-op.
type timeoutResponseWriter struct {
	gin.ResponseWriter

	mu       sync.Mutex
	timedOut bool
	written  bool
	scratch  http.Header
}

// expire marks the request as timed out and answers 504 unless the handler
// already began its own response.
func (w *timeoutResponseWriter) expire() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.timedOut = true
	if w.written {
		return
	}
	h := w.ResponseWriter.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Del("Content-Encoding")
	body := []byte(`{"error":"request timeout","message":"The request took too long to process"}`)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.ResponseWriter.WriteHeader(http.StatusGatewayTimeout)
	_, _ = w.ResponseWriter.Write(body)
	w.ResponseWriter.WriteHeaderNow()
	w.ResponseWriter.Flush()
}

// Header hands the handler a throw-away map once the real one belongs to the
// finished response.
func (w *timeoutResponseWriter) Header() http.Header {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut {
		if w.scratch == nil {
			w.scratch = http.Header{}
		}

		return w.scratch
	}

	return w.ResponseWriter.Header()
}

func (w *timeoutResponseWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut {
		return len(data), nil
	}
	w.written = true

	return w.ResponseWriter.Write(data)
}

func (w *timeoutResponseWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *timeoutResponseWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut {
		return
	}
	w.written = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *timeoutResponseWriter) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut {
		return
	}
	w.written = true
	w.ResponseWriter.WriteHeaderNow()
}

func (w *timeoutResponseWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut {
		return
	}
	w.written = true
	w.ResponseWriter.Flush()
}

// TimeoutConfig 超时配置.
type TimeoutConfig struct {
	ReadTimeout    time.Duration // 读取请求超时
	WriteTimeout   time.Duration // 写入响应超时
	IdleTimeout    time.Duration // 连接空闲超时
	HandlerTimeout time.Duration // 处理程序超时
}

// DefaultTimeoutConfig 默认超时配置.
func DefaultTimeoutConfig() TimeoutConfig {
	return TimeoutConfig{
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   30 * time.Second,
		IdleTimeout:    120 * time.Second,
		HandlerTimeout: 30 * time.Second,
	}
}

// contextKey is a custom type for context keys to avoid collisions.
type contextKey string

const dbTimeoutKey contextKey = "db_timeout"

// DatabaseTimeoutMiddleware 数据库查询超时中间件.
func DatabaseTimeoutMiddleware(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 将超时设置存入 context，供数据库操作使用
		ctx := context.WithValue(c.Request.Context(), dbTimeoutKey, timeout)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
