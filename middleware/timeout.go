package middleware

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
	"time"
)

// TimeoutMiddleware 请求超时中间件
func TimeoutMiddleware(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 为请求创建带超时的 context
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()

		// 更新请求的 context
		c.Request = c.Request.WithContext(ctx)

		// 使用 channel 来处理响应
		finished := make(chan struct{})

		go func() {
			c.Next()
			close(finished)
		}()

		select {
		case <-finished:
			// 请求正常完成
			return
		case <-ctx.Done():
			// 请求超时
			c.AbortWithStatusJSON(http.StatusGatewayTimeout, gin.H{
				"error":   "request timeout",
				"message": "The request took too long to process",
			})
			return
		}
	}
}

// TimeoutConfig 超时配置
type TimeoutConfig struct {
	ReadTimeout    time.Duration // 读取请求超时
	WriteTimeout   time.Duration // 写入响应超时
	IdleTimeout    time.Duration // 连接空闲超时
	HandlerTimeout time.Duration // 处理程序超时
}

// DefaultTimeoutConfig 默认超时配置
func DefaultTimeoutConfig() TimeoutConfig {
	return TimeoutConfig{
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   30 * time.Second,
		IdleTimeout:    120 * time.Second,
		HandlerTimeout: 30 * time.Second,
	}
}

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

const dbTimeoutKey contextKey = "db_timeout"

// DatabaseTimeoutMiddleware 数据库查询超时中间件
func DatabaseTimeoutMiddleware(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 将超时设置存入 context，供数据库操作使用
		ctx := context.WithValue(c.Request.Context(), dbTimeoutKey, timeout)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
