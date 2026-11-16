package account

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

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

	conn := db.GetDB().WithContext(c.Request.Context())
	middleware.InvalidateTokenCache(tokenStr)

	sum := sha256.Sum256([]byte(tokenStr))
	tokenHash := hex.EncodeToString(sum[:])
	var currentRow models.AuthToken
	_ = conn.Select("user_id", "external_client").Where("token = ?", tokenHash).First(&currentRow).Error

	pairHash, err := models.RevokeTokenByTokenStr(conn, tokenStr)
	if err != nil {
		log.ErrorV(errors.Wrap(err, "[Logout] revoke token error"))
		c.JSON(500, gin.H{"error": "Internal Server Error"})

		return
	}

	if strings.HasPrefix(currentRow.ExternalClient, models.PersistentDevicePrefix) {
		if deviceHashes, derr := revokeDeviceCredentialFamily(conn, currentRow.UserId, currentRow.ExternalClient, "", time.Now()); derr == nil {
			for _, h := range deviceHashes {
				middleware.InvalidateTokenCacheHash(h)
			}
		}
	}

	middleware.InvalidateTokenCacheHash(pairHash)
	middleware.InvalidateTokenCache(tokenStr)

	middleware.SetAppTokenCookie(c, "", -1)
	c.JSON(200, gin.H{"message": "ok"}) //nolint:goconst
}
