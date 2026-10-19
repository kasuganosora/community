package middleware

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/kasuganosora/community/config"
)

// AppTokenCookie is the browser session cookie set at login.
const AppTokenCookie = "app-token"

// AuthViaCookieKey is set on the gin context when the request was
// authenticated only by the app-token cookie.
const AuthViaCookieKey = "auth_via_cookie"

// SetAppTokenCookie writes (or, with maxAge < 0, clears) the session cookie
// with HttpOnly, SameSite=Lax and Secure whenever the site is served over
// HTTPS (directly, via a proxy, or per APP_BASE_URL).
func SetAppTokenCookie(c *gin.Context, value string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(AppTokenCookie, value, maxAge, "/", "", requestIsSecure(c), true)
}

func requestIsSecure(c *gin.Context) bool {
	return c.Request.TLS != nil ||
		strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") ||
		strings.HasPrefix(strings.ToLower(config.C.AppBaseURL), "https://")
}

// cookieOriginAllowed reports whether a cookie-authenticated request may
// proceed. A browser always sends Origin on WebSocket handshakes and on
// cross-origin or state-changing fetches, so a foreign Origin means another
// site is riding the victim's cookie (CSRF / cross-site WebSocket
// hijacking). Requests without Origin (same-origin GETs, navigations,
// native clients) are allowed; bearer-token requests never reach this check.
func cookieOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false // includes the opaque "null" origin
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if base, err := url.Parse(config.C.AppBaseURL); err == nil && base.Host != "" &&
		strings.EqualFold(base.Scheme, u.Scheme) && strings.EqualFold(base.Host, u.Host) {
		return true
	}
	for _, allowed := range strings.Split(config.C.WebSocketAllowedOrigins, ",") {
		if a := strings.TrimRight(strings.TrimSpace(allowed), "/"); a != "" && strings.EqualFold(a, strings.TrimRight(origin, "/")) {
			return true
		}
	}
	return false
}
