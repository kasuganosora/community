package middleware

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// TimeoutMiddleware 请求超时中间件
// 使用 goroutine + select 模式实现真正的超时控制：
// - 超时后立即返回 504 响应
// - 通过 context 取消通知下游处理器终止
// - 使用 responseWriterWrapper 防止超时后 goroutine 继续写入响应.
func TimeoutMiddleware(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()

		// 用 wrapper 包装 writer，超时后禁止写入
		wrapper := &timeoutResponseWriter{
			ResponseWriter: c.Writer,
			ctx:            ctx,
		}
		c.Writer = wrapper

		c.Request = c.Request.WithContext(ctx)

		finished := make(chan struct{})
		panicChan := make(chan any, 1)

		go func() {
			defer func() {
				if p := recover(); p != nil {
					panicChan <- p
				}
			}()

			c.Next()

			// 仅在未发生 panic 时关闭 finished，确保 panic 能被 select 正确处理
			close(finished)
		}()

		select {
		case <-finished:
			// 正常完成
		case p := <-panicChan:
			// handler 中发生 panic，立即重新抛出
			panic(p)
		case <-ctx.Done():
			// 超时：标记 writer 为已超时，阻止 goroutine 继续写入
			wrapper.timedOut.Store(1)

			if wrapper.written.Load() == 0 {
				// Restore the original writer so the timeout response isn't blocked
				// by the wrapper's timedOut check
				c.Writer = wrapper.ResponseWriter
				c.AbortWithStatusJSON(http.StatusGatewayTimeout, gin.H{
					"error":   "request timeout",
					"message": "The request took too long to process",
				})
			}
		}
	}
}

// timeoutResponseWriter 包装 gin ResponseWriter，超时后阻止写入.
type timeoutResponseWriter struct {
	gin.ResponseWriter

	//nolint:containedctx // legitimately needs context for timeout coordination
	ctx      context.Context
	timedOut atomic.Int32 // atomic: 0=正常, 1=已超时
	written  atomic.Int32 // atomic: 0=未写入, 1=已写入
}

func (w *timeoutResponseWriter) Write(data []byte) (int, error) {
	if w.timedOut.Load() == 1 {
		return 0, nil // 超时后丢弃写入
	}

	w.written.Store(1)

	return w.ResponseWriter.Write(data)
}

func (w *timeoutResponseWriter) WriteHeader(code int) {
	if w.timedOut.Load() == 1 {
		return
	}

	w.written.Store(1)
	w.ResponseWriter.WriteHeader(code)
}

func (w *timeoutResponseWriter) WriteHeaderNow() {
	if w.timedOut.Load() == 1 {
		return
	}

	w.written.Store(1)
	w.ResponseWriter.WriteHeaderNow()
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
