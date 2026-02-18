package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// EmailConfirmationResponse represents the email confirmation response
type EmailConfirmationResponse struct {
	Email string `json:"email,omitempty"`
	Sent  bool   `json:"sent"`
}

// SendEmailConfirmation handles POST /api/v1/emails/confirmations
// Sends a confirmation email to the user
func SendEmailConfirmation(c *gin.Context) {
	account := c.MustGet("account").(*models.Account)

	// Check if user exists
	var user models.User
	if err := db.GetDB().Where("id = ?", account.UserId).First(&user).Error; err != nil {
		c.JSON(404, gin.H{"error": "user not found"})
		return
	}

	// Check if already confirmed
	if user.IsMailVerified {
		c.JSON(400, gin.H{"error": "email already confirmed"})
		return
	}

	// TODO: Send actual email with confirmation link
	// For now, just return success

	c.JSON(200, EmailConfirmationResponse{
		Sent: true,
	})
}

// VerifyEmailConfirmation handles POST /api/v1/emails/confirmations/verify
// Verifies an email confirmation token
func VerifyEmailConfirmation(c *gin.Context) {
	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "token required"})
		return
	}

	// TODO: Verify the token and update user's is_mail_verified
	// For now, just return success

	db.GetDB().Model(&models.User{}).
		Where("id IN (SELECT user_id FROM user_meta WHERE meta_key = 'email_verification_token' AND meta_value = ?)", req.Token).
		Updates(map[string]interface{}{
			"is_mail_verified": true,
		})

	c.JSON(200, gin.H{"success": true})
}
