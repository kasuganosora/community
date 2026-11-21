package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/resilience"
)

// AdaptiveOverloadMiddleware protects the server from sudden traffic spikes or
// downstream slowdowns using a TCP-Vegas adaptive concurrency limiter
// (inspired by openBshi/library/rate/vegas).
// Health checks, streaming/WebSocket upgrades, and large media/archive transfers
// bypass fast-API RTT concurrency shedding.
func AdaptiveOverloadMiddleware(cfg resilience.VegasConfig) gin.HandlerFunc {
	limiter := resilience.NewVegasLimiter(cfg)
	return func(c *gin.Context) {
		if isOverloadExemptRequest(c) {
			c.Next()
			return
		}

		start := time.Now()
		done, allowed := limiter.Acquire()
		if !allowed {
			done(start, resilience.VegasIgnore)
			c.Header("Retry-After", "1")
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": "server temporarily overloaded, please retry shortly",
			})
			return
		}

		defer func() {
			status := c.Writer.Status()
			op := resilience.VegasSuccess
			if status >= http.StatusInternalServerError {
				op = resilience.VegasDrop
			}
			done(start, op)
		}()

		c.Next()
	}
}

func isOverloadExemptRequest(c *gin.Context) bool {
	if strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
		return true
	}
	path := c.Request.URL.Path
	return path == "/health" ||
		path == "/healthz" ||
		strings.HasPrefix(path, "/api/v1/streaming") ||
		strings.HasPrefix(path, "/api/v1/media") ||
		strings.HasPrefix(path, "/api/v2/media") ||
		strings.HasPrefix(path, "/api/v1/data/") ||
		strings.HasPrefix(path, "/api/v1/admin/data/")
}
