package middleware

import (
	"github.com/kasuganosora/community/secretredact"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/reqid"
)

// RequestLogMiddleware 请求日志中间件.
func RequestLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// 生成请求 ID
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}

		c.Set("request_id", requestID)
		c.Request = c.Request.WithContext(reqid.With(c.Request.Context(), requestID))
		c.Header("X-Request-ID", requestID)

		// 记录请求开始
		entry := log.WithFields(map[string]any{
			"request_id": requestID,
			"method":     c.Request.Method,
			"path":       c.Request.URL.Path,
			"client_ip":  c.ClientIP(),
			"user_agent": secretredact.Scrub(c, c.Request.UserAgent()),
		})
		entry.Debug("Request started")

		c.Next()

		// 记录请求结束
		duration := time.Since(start)
		status := c.Writer.Status()

		fields := map[string]any{
			"request_id": requestID,
			"method":     c.Request.Method,
			"path":       c.FullPath(), // Route pattern only; omit private IDs and query values.
			"status":     status,       //nolint:goconst
			"duration":   duration.Milliseconds(),
			"size":       c.Writer.Size(),
		}

		// 如果有错误，记录错误信息
		if len(c.Errors) > 0 {
			fields["errors"] = secretredact.Scrub(c, c.Errors.String())
		}

		entry = log.WithFields(fields)

		// 根据状态码选择日志级别
		switch {
		case status >= 500:
			entry.Error("Request failed")
		case status >= 400:
			entry.Warn("Request warning")
		default:
			entry.Info("Request completed")
		}
	}
}

// PerformanceLogMiddleware 性能日志中间件（只记录慢请求）.
func PerformanceLogMiddleware(threshold time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		duration := time.Since(start)

		if duration > threshold {
			log.WithFields(map[string]any{
				"method":    c.Request.Method,
				"path":      c.Request.URL.Path,
				"duration":  duration.Milliseconds(),
				"threshold": threshold.Milliseconds(),
			}).Warn("Slow request detected")
		}
	}
}
