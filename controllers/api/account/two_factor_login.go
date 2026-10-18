package account

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/twofactor"
)

// PassSecondFactor enforces TOTP for a user whose password was just
// verified. With an empty code and issueChallenge set it answers 401
// two_factor_required plus a short-lived challenge for
// POST /accounts/login/two_factor. It writes the error response itself and
// returns false when the request must stop.
func PassSecondFactor(c *gin.Context, userID uint64, code string, issueChallenge bool) bool {
	enabled, err := twofactor.IsEnabled(db.GetDB(), userID)
	if err != nil {
		log.Error("two-factor lookup for user %d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return false
	}

	if !enabled {
		return true
	}

	if code == "" {
		body := gin.H{"error": "two_factor_required", "two_factor_required": true}
		if issueChallenge {
			body["challenge"] = twofactor.IssueChallenge(userID)
		}
		c.JSON(http.StatusUnauthorized, body)

		return false
	}

	return VerifySecondFactor(c, userID, code)
}

// VerifySecondFactor checks code and writes 401/429 on failure. The code
// itself is never logged or echoed.
func VerifySecondFactor(c *gin.Context, userID uint64, code string) bool {
	err := twofactor.Verify(db.GetDB(), userID, code)
	switch {
	case err == nil:
		return true
	case errors.Is(err, twofactor.ErrLocked):
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "two_factor_locked"})
	case errors.Is(err, twofactor.ErrInvalidCode):
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_two_factor_code"})
	default:
		log.Error("two-factor verify for user %d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}

	return false
}

type twoFactorLoginRequest struct {
	Challenge string `binding:"required" form:"challenge" json:"challenge"`
	Code      string `binding:"required" form:"code"      json:"code"`
}

// LoginTwoFactor completes a password login that answered two_factor_required.
func LoginTwoFactor(c *gin.Context) {
	req := &twoFactorLoginRequest{}
	if err := c.ShouldBind(req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "challenge and code are required"})

		return
	}

	uid, err := twofactor.ParseChallenge(req.Challenge)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_challenge"})

		return
	}

	user := &models.User{}
	if err = db.GetDB().Where("id = ?", uid).First(user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_challenge"})

		return
	}

	if user.IsBan || user.Status != models.UserStatusNormal {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user status abnormal"})

		return
	}

	if systemsettings.GetBool(systemsettings.KeyRegistrationCheckEmail, false) && !user.IsMailVerified {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "email not verified"})

		return
	}

	if !VerifySecondFactor(c, uid, req.Code) {
		return
	}

	finishLogin(c, user)
}

// RequireSecondFactorForSensitive is used by already-authenticated
// endpoints that re-confirm the password (change password, migrate,
// delete). It never answers 401 so clients do not treat it as a logout.
func RequireSecondFactorForSensitive(c *gin.Context, userID uint64, code string) bool {
	enabled, err := twofactor.IsEnabled(db.GetDB(), userID)
	if err != nil {
		log.Error("two-factor lookup for user %d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return false
	}

	if !enabled {
		return true
	}

	if code == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "two_factor_required", "two_factor_required": true})

		return false
	}

	err = twofactor.Verify(db.GetDB(), userID, code)
	switch {
	case err == nil:
		return true
	case errors.Is(err, twofactor.ErrLocked):
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "two_factor_locked"})
	case errors.Is(err, twofactor.ErrInvalidCode):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_two_factor_code"})
	default:
		log.Error("two-factor verify for user %d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}

	return false
}
