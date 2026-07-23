package log

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// StatsHandler 返回日志统计信息的 HTTP handler.
func StatsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		stats := GetCurrentLogStats()
		c.JSON(http.StatusOK, stats)
	}
}

// HealthHandler 返回日志健康状况的 HTTP handler
// 当日志磁盘使用超过 90% 时返回警告状态.
func HealthHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		stats := GetCurrentLogStats()

		status := "healthy"
		message := "Log system is healthy"

		if nearLimit, ok := stats["is_near_limit"].(bool); ok && nearLimit {
			status = "warning"
			message = "Log disk usage is above 80%, cleanup recommended"
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  status,
			"message": message,
			"stats":   stats,
		})
	}
}
