package account

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/email"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
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

	// Generate confirmation token
	token := generateEmailConfirmationToken()

	// Store token in user_meta
	userMeta := models.UserMeta{
		UserId: user.Id,
		Key:    "email_verification_token",
		Value:  token,
	}

	// Delete any existing token for this user
	db.GetDB().Where("user_id = ? AND key = ?", user.Id, "email_verification_token").Delete(&models.UserMeta{})

	if err := db.GetDB().Create(&userMeta).Error; err != nil {
		c.JSON(500, gin.H{"error": "failed to generate confirmation token"})
		return
	}

	// Load user's email from UserSecret
	var userSecret models.UserSecretData
	if err := db.GetDB().Where("user_id = ?", user.Id).First(&userSecret).Error; err != nil {
		c.JSON(500, gin.H{"error": "failed to load user email"})
		return
	}

	// Send confirmation email
	if err := sendConfirmationEmail(userSecret.Email, token, account.Username); err != nil {
		log.Error("Failed to send confirmation email: %v", err)
		c.JSON(500, gin.H{"error": "failed to send confirmation email"})
		return
	}

	c.JSON(200, EmailConfirmationResponse{
		Email: userSecret.Email,
		Sent:  true,
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

	// Find user by token
	var userMeta models.UserMeta
	if err := db.GetDB().Where("key = ? AND value = ?", "email_verification_token", req.Token).First(&userMeta).Error; err != nil {
		c.JSON(400, gin.H{"error": "invalid or expired token"})
		return
	}

	// Update user's email verification status
	if err := db.GetDB().Model(&models.User{}).Where("id = ?", userMeta.UserId).Update("is_mail_verified", true).Error; err != nil {
		c.JSON(500, gin.H{"error": "failed to verify email"})
		return
	}

	// Delete the used token
	db.GetDB().Delete(&userMeta)

	c.JSON(200, gin.H{"success": true})
}

// generateEmailConfirmationToken generates a random token for email confirmation
func generateEmailConfirmationToken() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return ""
	}
	return hex.EncodeToString(bytes)
}

// sendConfirmationEmail sends the email confirmation email
func sendConfirmationEmail(userEmail, token, username string) error {
	siteName := systemsettings.GetString(systemsettings.KeyTitle, "Community")
	baseURL := systemsettings.GetString(systemsettings.KeyDomain, "")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}

	confirmationURL := baseURL + "/api/v1/emails/confirmations/verify?token=" + token

	data := map[string]any{
		"SiteName":        siteName,
		"Username":        username,
		"ConfirmationURL": confirmationURL,
		"Token":           token,
	}

	subject := siteName + " - Email Confirmation"
	textBody := "Please confirm your email by clicking this link: " + confirmationURL

	return email.SendHtmlTemplate(userEmail, "", "", subject, textBody, "email_confirmation", data)
}
