package metrics

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// AccessGuard protects GET /metrics. When neither token nor cidrs is set it
// allows everything (backwards compatible). Otherwise a request passes if it
// carries "Authorization: Bearer <token>" matching token, or its client IP
// (gin's ClientIP, i.e. after trusted-proxy resolution) is inside cidrs.
func AccessGuard(token, cidrs string) gin.HandlerFunc {
	token = strings.TrimSpace(token)
	var nets []*net.IPNet
	for _, raw := range strings.Split(cidrs, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "/") {
			if ip := net.ParseIP(raw); ip != nil {
				if ip.To4() != nil {
					raw += "/32"
				} else {
					raw += "/128"
				}
			}
		}
		if _, n, err := net.ParseCIDR(raw); err == nil {
			nets = append(nets, n)
		}
	}
	configured := token != "" || strings.TrimSpace(cidrs) != ""

	return func(c *gin.Context) {
		if !configured {
			c.Next()
			return
		}
		if token != "" {
			auth := c.GetHeader("Authorization")
			if len(auth) > 7 && strings.EqualFold(auth[:7], "bearer ") &&
				subtle.ConstantTimeCompare([]byte(strings.TrimSpace(auth[7:])), []byte(token)) == 1 {
				c.Next()
				return
			}
		}
		if ip := net.ParseIP(c.ClientIP()); ip != nil {
			for _, n := range nets {
				if n.Contains(ip) {
					c.Next()
					return
				}
			}
		}
		c.AbortWithStatus(http.StatusNotFound)
	}
}
