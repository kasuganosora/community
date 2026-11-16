package account

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

var errPersistentTokenConsumed = errors.New("persistent token already consumed")

type refreshPersistentRequest struct {
	PersistentToken string `binding:"required" form:"persistent_token" json:"persistent_token"`
}

type revokePersistentRequest struct {
	PersistentToken string `binding:"required" form:"persistent_token" json:"persistent_token"`
	ClearCookie     *bool  `form:"clear_cookie"                        json:"clear_cookie"`
}

// RefreshPersistentSession exchanges a valid server-encrypted persistent login
// credential for a new HttpOnly app-token session cookie and a newly rotated
// persistent login credential, immediately invalidating the used credential.
func RefreshPersistentSession(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !middleware.CookieOriginAllowed(c.Request) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "cross-origin cookie authentication is not allowed"})

		return
	}

	var req refreshPersistentRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "persistent_token is required"})

		return
	}

	rawPersistent := strings.TrimSpace(req.PersistentToken)
	if rawPersistent == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "persistent_token is required"})

		return
	}

	claims, err := models.ParsePersistentToken(rawPersistent, config.C.EncryptedKey)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid persistent token"})

		return
	}

	conn := db.GetDB().WithContext(c.Request.Context())
	tokenHash := models.PersistentTokenHash(rawPersistent)
	deviceClient := models.PersistentDeviceClient(claims.DeviceId)
	now := time.Now()

	var stored models.AuthToken
	if err = conn.Where("token = ? AND type = ?", tokenHash, models.AuthTokenTypePersistent).First(&stored).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid persistent token"})

		return
	}

	if stored.UserId != claims.UserId || stored.ExternalClient != deviceClient {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid persistent token"})

		return
	}

	if stored.Revoke != nil || (stored.ExpiredAt != nil && stored.ExpiredAt.Before(now)) {
		if stored.Revoke != nil && stored.RevokeReason == models.RevokeReasonRotated {
			revokedHashes, rerr := revokeDeviceCredentialFamily(conn, stored.UserId, deviceClient, models.RevokeReasonReplay, now)
			if rerr != nil {
				log.Warn("failed to revoke replayed persistent device family for user %d: %v", stored.UserId, rerr)
			}
			for _, h := range revokedHashes {
				middleware.InvalidateTokenCacheHash(h)
			}
			clearCookieIfMatchingHash(c, revokedHashes)
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid persistent token"})

		return
	}

	user := &models.User{}
	if err = conn.Where("id = ?", claims.UserId).First(user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_, _ = revokeDeviceCredentialFamily(conn, claims.UserId, deviceClient, "", now)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid persistent token"})

			return
		}
		log.ErrorV(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	if _, err = user.GetAccountBase(conn); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_, _ = revokeDeviceCredentialFamily(conn, claims.UserId, deviceClient, "", now)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid persistent token"})

			return
		}
		log.ErrorV(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	if user.Status != models.UserStatusNormal {
		_, _ = revokeDeviceCredentialFamily(conn, user.Id, deviceClient, "", now)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user status abnormal"})

		return
	}

	if systemsettings.GetBool(systemsettings.KeyRegistrationCheckEmail, false) && !user.IsMailVerified {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "email not verified"})

		return
	}

	sessionExpire := SessionExpireSeconds()
	permissions, err := models.GetUserPermissions(conn, user.Id)
	if err != nil {
		log.ErrorV(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	newSessionRow, newSessionToken := models.NewAuthToken(user.Id, models.AuthTokenTypeWeb, 0, permissions, nil, sessionExpire, config.C.EncryptedKey)
	if newSessionToken == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	newPersistentRow, newPersistentToken, err := models.NewPersistentToken(user.Id, claims.DeviceId, newSessionRow.Token, config.C.EncryptedKey)
	if err != nil {
		log.ErrorV(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	newSessionRow.PairHash = newPersistentRow.Token
	newSessionRow.ExternalClient = deviceClient

	prevCookie, _ := c.Cookie(middleware.AppTokenCookie)

	err = conn.Transaction(func(tx *gorm.DB) error {
		if err := lockPersistentCredentialFamilies(tx, stored.UserId); err != nil {
			return err
		}
		res := tx.Model(&models.AuthToken{}).
			Where("token = ? AND type = ? AND revoke IS NULL", stored.Token, models.AuthTokenTypePersistent).
			Updates(map[string]any{
				"revoke":        now,
				"revoke_reason": models.RevokeReasonRotated,
				"last_used_at":  now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errPersistentTokenConsumed
		}

		if stored.PairHash != "" {
			if err := tx.Model(&models.AuthToken{}).
				Where("token = ? AND type = ? AND revoke IS NULL", stored.PairHash, models.AuthTokenTypeWeb).
				Update("revoke", now).Error; err != nil {
				return err
			}
		}

		if prevCookie != "" {
			if err := models.RevokeSingleTokenByTokenStr(tx, prevCookie); err != nil {
				return err
			}
		}

		if err := tx.Create(newSessionRow).Error; err != nil {
			return err
		}

		return tx.Create(newPersistentRow).Error
	})
	if err != nil {
		if errors.Is(err, errPersistentTokenConsumed) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid persistent token"})

			return
		}
		log.ErrorV(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	if stored.PairHash != "" {
		middleware.InvalidateTokenCacheHash(stored.PairHash)
	}
	if prevCookie != "" {
		middleware.InvalidateTokenCache(prevCookie)
	}

	middleware.SetAppTokenCookie(c, newSessionToken, sessionExpire)
	c.JSON(http.StatusOK, gin.H{
		"message":          "login success", //nolint:goconst
		"token":            newSessionToken,
		"persistent_token": newPersistentToken,
	})
}

// RevokePersistentSession revokes a persistent login credential and its paired
// device session without disturbing other devices or another active account's cookie.
func RevokePersistentSession(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !middleware.CookieOriginAllowed(c.Request) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "cross-origin cookie authentication is not allowed"})

		return
	}

	var req revokePersistentRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "persistent_token is required"})

		return
	}

	rawPersistent := strings.TrimSpace(req.PersistentToken)
	if rawPersistent == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "persistent_token is required"})

		return
	}

	claims, err := models.ParsePersistentToken(rawPersistent, config.C.EncryptedKey)
	if err != nil {
		if req.ClearCookie != nil && *req.ClearCookie {
			middleware.SetAppTokenCookie(c, "", -1)
		}
		c.JSON(http.StatusOK, gin.H{"message": "ok"}) //nolint:goconst

		return
	}

	conn := db.GetDB().WithContext(c.Request.Context())
	deviceClient := models.PersistentDeviceClient(claims.DeviceId)
	revokedHashes, err := revokeDeviceCredentialFamily(conn, claims.UserId, deviceClient, "", time.Now())
	if err != nil {
		log.ErrorV(errors.Wrap(err, "[RevokePersistentSession] revoke device family error"))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal Server Error"})

		return
	}

	for _, h := range revokedHashes {
		middleware.InvalidateTokenCacheHash(h)
	}

	switch {
	case req.ClearCookie != nil && *req.ClearCookie:
		middleware.SetAppTokenCookie(c, "", -1)
	case req.ClearCookie == nil:
		clearCookieIfMatchingHash(c, revokedHashes)
	}

	c.JSON(http.StatusOK, gin.H{"message": "ok"}) //nolint:goconst
}

func revokeDeviceCredentialFamily(conn *gorm.DB, userID uint64, deviceClient string, reason string, now time.Time) ([]string, error) {
	if userID == 0 || !strings.HasPrefix(deviceClient, models.PersistentDevicePrefix) {
		return nil, nil
	}

	var hashes []string
	err := conn.Transaction(func(tx *gorm.DB) error {
		if err := lockPersistentCredentialFamilies(tx, userID); err != nil {
			return err
		}
		if err := tx.Model(&models.AuthToken{}).
			Where("user_id = ? AND external_client = ? AND revoke IS NULL", userID, deviceClient).
			Pluck("token", &hashes).Error; err != nil {
			return err
		}
		if len(hashes) == 0 {
			return nil
		}

		updates := map[string]any{"revoke": now}
		if reason != "" {
			updates["revoke_reason"] = reason
		}
		return tx.Model(&models.AuthToken{}).
			Where("token IN ? AND revoke IS NULL", hashes).
			Updates(updates).Error
	})
	return hashes, err
}

// lockPersistentCredentialFamilies serializes rotation and family revocation
// across application processes. Both must acquire this lock before touching
// auth_tokens and hold it until commit. A no-op UPDATE takes a write lock on
// PostgreSQL/MySQL as well as SQLite (where SELECT FOR UPDATE is unsupported).
// Using the existing user row avoids a schema migration and deliberately
// serializes different devices of one user without revoking those devices.
func lockPersistentCredentialFamilies(tx *gorm.DB, userID uint64) error {
	return tx.Unscoped().Model(&models.User{}).Where("id = ?", userID).
		UpdateColumn("id", gorm.Expr("id")).Error
}

func clearCookieIfMatchingHash(c *gin.Context, hashes []string) {
	if len(hashes) == 0 {
		return
	}
	curCookie, err := c.Cookie(middleware.AppTokenCookie)
	if err != nil || curCookie == "" {
		return
	}
	sum := sha256.Sum256([]byte(curCookie))
	curHash := hex.EncodeToString(sum[:])
	for _, h := range hashes {
		if h == curHash {
			middleware.SetAppTokenCookie(c, "", -1)

			return
		}
	}
}
