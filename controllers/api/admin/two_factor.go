package admin

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/twofactor"
)

func adminTwoFactorTarget(c *gin.Context) (*models.Account, bool) {
	id, ok := adminAccountID(c)
	if !ok {
		return nil, false
	}

	account := &models.Account{}
	if err := db.GetDB().WithContext(c.Request.Context()).Preload("User").First(account, id).Error; err != nil || !account.IsLocal() || account.UserId == 0 {
		c.JSON(404, gin.H{"error": "local account not found"})

		return nil, false
	}

	return account, true
}

// GetAccountTwoFactor GET /admin/accounts/:id/two_factor.
func GetAccountTwoFactor(c *gin.Context) {
	account, ok := adminTwoFactorTarget(c)
	if !ok {
		return
	}

	row, err := twofactor.Load(db.GetDB(), account.UserId)
	if err != nil {
		c.JSON(500, gin.H{"error": "internal server error"})

		return
	}

	resp := gin.H{"enabled": row.Enabled(), "enabled_at": nil, "backup_codes_remaining": 0, "security_pin_set": twofactor.SecurityPINSet(row)}
	if row.Enabled() {
		n, _ := twofactor.RemainingBackupCodes(db.GetDB(), account.UserId)
		resp["enabled_at"], resp["backup_codes_remaining"] = row.EnabledAt, n
	}

	c.JSON(200, resp)
}

// ResetAccountTwoFactor POST /admin/accounts/:id/two_factor/reset disables
// TOTP and deletes backup codes so the user can sign in with the password.
func ResetAccountTwoFactor(c *gin.Context) {
	account, ok := adminTwoFactorTarget(c)
	if !ok {
		return
	}

	if account.User != nil && account.User.IsAdmin {
		current, _ := c.Get("user")
		actor, _ := current.(*models.User)
		if actor == nil || !actor.IsAdmin {
			c.JSON(403, gin.H{"error": "only root administrators may manage root accounts"})

			return
		}
	}

	if err := twofactor.AdminReset(db.GetDB(), account.UserId); err != nil {
		log.Error("admin two-factor reset for account %d: %v", account.Id, err)
		c.JSON(500, gin.H{"error": "internal server error"})

		return
	}

	log.Info("admin reset two-factor authentication for account %d", account.Id)
	c.JSON(200, gin.H{"enabled": false})
}
