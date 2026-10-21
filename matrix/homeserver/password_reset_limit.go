package homeserver

import (
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/middleware"
	"github.com/redis/go-redis/v9"
)

var (
	passwordResetIPLimitOnce sync.Once
	passwordResetIPLimit     gin.HandlerFunc

	passwordResetEmailLimitOnce sync.Once
	passwordResetEmailLimiter   middleware.RateLimiter
)

// passwordResetIPRateLimit limits unauthenticated password-reset token
// requests per client IP: burst of 6, refilling at 6/min.
// (APIRateLimitMiddleware derives burst as rpm/6, which would be 1 here.)
func passwordResetIPRateLimit() gin.HandlerFunc {
	passwordResetIPLimitOnce.Do(func() {
		passwordResetIPLimit = newPasswordResetIPLimit(middleware.GetRedisClient())
	})

	return passwordResetIPLimit
}

func newPasswordResetIPLimit(rc *redis.Client) gin.HandlerFunc {
	cfg := middleware.RateLimiterConfig{GlobalRPS: 6.0 / 60, GlobalBurst: 6}

	var limiter middleware.RateLimiter
	if rc != nil {
		limiter = middleware.NewRedisRateLimiter(rc, cfg)
	} else {
		limiter = middleware.NewMemoryRateLimiter(cfg)
	}

	return func(c *gin.Context) {
		if !limiter.Allow("pwreset:ip:" + c.ClientIP()) {
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"errcode":        "M_LIMIT_EXCEEDED",
				"error":          "Too many requests",
				"retry_after_ms": 60000,
			})

			return
		}

		c.Next()
	}
}

// allowPasswordResetForEmail limits reset requests per target address
// (burst 3, then ~1/min; the shared Redis bucket TTL is 60s) so a single mailbox cannot be flooded.
func allowPasswordResetForEmail(email string) bool {
	passwordResetEmailLimitOnce.Do(func() {
		cfg := middleware.RateLimiterConfig{GlobalRPS: 1.0 / 60, GlobalBurst: 3}
		if rc := middleware.GetRedisClient(); rc != nil {
			passwordResetEmailLimiter = middleware.NewRedisRateLimiter(rc, cfg)
		} else {
			passwordResetEmailLimiter = middleware.NewMemoryRateLimiter(cfg)
		}
	})

	return passwordResetEmailLimiter.Allow("pwreset:email:" + strings.ToLower(strings.TrimSpace(email)))
}
