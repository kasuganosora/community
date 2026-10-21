package homeserver

import (
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/middleware"
)

var (
	passwordResetIPLimitOnce sync.Once
	passwordResetIPLimit     gin.HandlerFunc

	passwordResetEmailLimitOnce sync.Once
	passwordResetEmailLimiter   middleware.RateLimiter
)

// passwordResetIPRateLimit limits unauthenticated password-reset token
// requests to 6/min per client IP.
func passwordResetIPRateLimit() gin.HandlerFunc {
	passwordResetIPLimitOnce.Do(func() {
		passwordResetIPLimit = middleware.APIRateLimitMiddleware(middleware.GetRedisClient(), "/_matrix/client/v3/account/password/email", 6)
	})

	return passwordResetIPLimit
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
