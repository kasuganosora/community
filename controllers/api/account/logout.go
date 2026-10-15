package account

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
)

// Logout handles GET/POST /api/v1/accounts/logout. It revokes the token the
// caller authenticated with (web session or OAuth access token, plus its
// paired refresh token), purges the auth cache and clears the cookie.
func Logout(c *gin.Context) {
	if _, exists := c.Get("user"); !exists {
		c.JSON(401, gin.H{"error": "Unauthorized"}) //nolint:goconst

		return
	}

	tokenStr := c.GetHeader("Authorization")
	if tokenStr == "" {
		tokenStr, _ = c.Cookie("app-token")
	}

	tokenStr = strings.TrimSpace(strings.TrimPrefix(tokenStr, "Bearer "))
	if tokenStr == "" {
		c.JSON(401, gin.H{"error": "Unauthorized"})

		return
	}

	middleware.InvalidateTokenCache(tokenStr)

	pairHash, err := models.RevokeTokenByTokenStr(db.GetDB(), tokenStr)
	if err != nil {
		log.ErrorV(errors.Wrap(err, "[Logout] revoke token error"))
		c.JSON(500, gin.H{"error": "Internal Server Error"})

		return
	}

	middleware.InvalidateTokenCacheHash(pairHash)
	middleware.InvalidateTokenCache(tokenStr)

	// SameSite=Lax matches the login cookie.
	secure := c.Request.TLS != nil
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("app-token", "", -1, "/", "", secure, true)
	c.JSON(200, gin.H{"message": "ok"}) //nolint:goconst
}
