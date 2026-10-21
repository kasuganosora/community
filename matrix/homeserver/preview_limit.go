package homeserver

import (
	"errors"
	"net/url"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/middleware"
)

var (
	previewURLLimitOnce sync.Once
	previewURLLimit     gin.HandlerFunc
)

// previewURLRateLimit limits URL previews (outbound fetches) to 30/min per
// client (user when known, otherwise real client IP). All preview_url
// routes share one limiter.
func previewURLRateLimit() gin.HandlerFunc {
	previewURLLimitOnce.Do(func() {
		previewURLLimit = middleware.APIRateLimitMiddleware(middleware.GetRedisClient(), "", 30)
	})

	return previewURLLimit
}

// validatePusherURL requires an https push gateway URL whose host resolves
// only to public addresses.
func validatePusherURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return errors.New("url must use https")
	}

	return validateURLForFetch(raw)
}
