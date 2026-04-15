package admin

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
)

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

// ListAccounts handles GET /api/v1/admin/accounts
func ListAccounts(c *gin.Context) {
	limit := 40
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 80 {
			limit = parsed
		}
	}

	offset := 0
	if o := c.Query("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	// Build query
	query := db.GetDB().Model(&models.Account{}).Preload("Extra")

	// Filter by status
	if status := c.Query("status"); status != "" {
		switch status {
		case "active":
			query = query.Where("suspended = ? AND silenced = ? AND is_ban = ?", false, false, false)
		case "pending":
			query = query.Where("user_id IS NULL")
		case "disabled":
			query = query.Where("suspended = ?", true)
		case "silenced":
			query = query.Where("silenced = ?", true)
		case "suspended":
			query = query.Where("suspended = ?", true)
		}
	}

	// Filter by origin
	if origin := c.Query("origin"); origin != "" {
		switch origin {
		case "local":
			query = query.Where("domain IS NULL")
		case "remote":
			query = query.Where("domain IS NOT NULL")
		}
	}

	// Filter by username
	if username := c.Query("username"); username != "" {
		query = query.Where("username LIKE ?", "%"+escapeLike(username)+"%")
	}

	// Filter by display name
	if displayName := c.Query("display_name"); displayName != "" {
		query = query.Where("display_name LIKE ?", "%"+escapeLike(displayName)+"%")
	}

	// Filter by email (for local accounts)
	if email := c.Query("email"); email != "" {
		query = query.Joins("JOIN users ON users.id = accounts.user_id").
			Where("users.email LIKE ?", "%"+escapeLike(email)+"%")
	}

	// Filter by IP address
	if ip := c.Query("ip"); ip != "" {
		query = query.Joins("JOIN users ON users.id = accounts.user_id").
			Where("users.sign_up_ip = ?", ip)
	}

	// Order
	query = query.Order("accounts.created_at DESC")

	accounts := make([]*models.Account, 0)
	if err := query.Limit(limit).Offset(offset).Find(&accounts).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "list accounts error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	// Build response
	result := make([]gin.H, 0, len(accounts))
	for _, account := range accounts {
		result = append(result, serializeAdminAccount(account))
	}

	c.JSON(200, result)
}

// GetAccount handles GET /api/v1/admin/accounts/:id
func GetAccount(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})
		return
	}

	account := &models.Account{}
	if err := db.GetDB().Preload("Extra").Preload("User").First(account, id).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "account not found"})
		return
	}

	c.JSON(200, serializeAdminAccount(account))
}

// AccountActionRequest represents an account action request
type AccountActionRequest struct {
	Type                  string `json:"type"`
	ReportId              string `json:"report_id"`
	WarningPresetId       string `json:"warning_preset_id"`
	Text                  string `json:"text"`
	SendEmailNotification bool   `json:"send_email_notification"`
}

// PerformAccountAction handles POST /api/v1/admin/accounts/:id/action
func PerformAccountAction(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})
		return
	}

	var req AccountActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid request"})
		return
	}

	account := &models.Account{}
	if err := db.GetDB().Preload("Extra").First(account, id).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "account not found"})
		return
	}

	switch req.Type {
	case "enable":
		account.IsBan = false
		account.Locked = false
	case "silence":
		account.Locked = true
	case "unsilence":
		account.Locked = false
	case "suspend":
		account.IsBan = true
		account.Locked = true
	case "unsuspend":
		account.IsBan = false
		account.Locked = false
	case "sensitive", "unsensitive":
		// No-op for now
	}

	if err := db.GetDB().Save(account).Error; err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	c.JSON(200, serializeAdminAccount(account))
}

// EnableAccount handles POST /api/v1/admin/accounts/:id/enable
func EnableAccount(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})
		return
	}

	account := &models.Account{}
	if err := db.GetDB().Preload("Extra").First(account, id).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "account not found"})
		return
	}

	account.IsBan = false
	account.Locked = false
	if err := db.GetDB().Save(account).Error; err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	c.JSON(200, serializeAdminAccount(account))
}

// ApproveAccount handles POST /api/v1/admin/accounts/:id/approve
func ApproveAccount(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})
		return
	}

	account := &models.Account{}
	if err := db.GetDB().Preload("Extra").Preload("User").First(account, id).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "account not found"})
		return
	}

	// Update user status to approved
	if account.User != nil {
		account.User.Status = 0 // 0 = normal
		if err := db.GetDB().Save(account.User).Error; err != nil {
			c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
			return
		}
	}

	c.JSON(200, serializeAdminAccount(account))
}

// RejectAccount handles POST /api/v1/admin/accounts/:id/reject
func RejectAccount(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})
		return
	}

	account := &models.Account{}
	if err := db.GetDB().Preload("Extra").First(account, id).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "account not found"})
		return
	}

	// Delete account
	if err := db.GetDB().Delete(account).Error; err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	c.JSON(200, gin.H{})
}

// UnsilenceAccount handles POST /api/v1/admin/accounts/:id/unsilence
func UnsilenceAccount(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})
		return
	}

	account := &models.Account{}
	if err := db.GetDB().Preload("Extra").First(account, id).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "account not found"})
		return
	}

	account.Locked = false
	if err := db.GetDB().Save(account).Error; err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	c.JSON(200, serializeAdminAccount(account))
}

// UnsuspendAccount handles POST /api/v1/admin/accounts/:id/unsuspend
func UnsuspendAccount(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})
		return
	}

	account := &models.Account{}
	if err := db.GetDB().Preload("Extra").First(account, id).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "account not found"})
		return
	}

	account.IsBan = false
	account.Locked = false
	if err := db.GetDB().Save(account).Error; err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	c.JSON(200, serializeAdminAccount(account))
}

func serializeAdminAccount(account *models.Account) gin.H {
	var domain interface{}
	if account.Domain != nil {
		domain = *account.Domain
	}

	var createdAt, lastStatusAt, lastActivityAt interface{}
	if account.CreatedAt != nil {
		createdAt = account.CreatedAt.Format(time.RFC3339)
	}
	if account.LastStatusAt != nil {
		lastStatusAt = account.LastStatusAt.Format(time.RFC3339)
	}
	if account.LastActivityAt != nil {
		lastActivityAt = account.LastActivityAt.Format(time.RFC3339)
	}

	// Use IsBan and Locked as suspended/silenced equivalents
	suspended := account.IsBan
	silenced := account.Locked && !account.IsBan

	result := gin.H{
		"id":               strconv.FormatUint(account.Id, 10),
		"username":         account.Username,
		"domain":           domain,
		"display_name":     account.DisplayName,
		"avatar":           account.Avatar,
		"avatar_static":    account.AvatarStatic,
		"header":           account.Header,
		"header_static":    account.HeaderStatic,
		"locked":           account.Locked,
		"bot":              account.Bot,
		"discoverable":     account.Discoverable,
		"note":             account.Note,
		"created_at":       createdAt,
		"last_status_at":   lastStatusAt,
		"last_activity_at": lastActivityAt,
		"suspended":        suspended,
		"silenced":         silenced,
		"account":          serializer.ToAccount(account),
	}

	// Add user info for local accounts
	if account.User != nil {
		result["email_hash"] = account.User.EmailHash
		result["role"] = "user"
		if account.User.IsAdmin {
			result["role"] = "admin"
		}
		result["user_status"] = account.User.Status
	}

	return result
}
