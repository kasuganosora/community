package admin

import (
	"strconv"
	"strings"
	"time"

	"errors"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")

	return s
}

// ListAccounts returns bounded, stable offset pages of administrative accounts.
func ListAccounts(c *gin.Context) {
	limit, offset := 40, 0
	if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 && n <= 80 {
		limit = n
	}
	if n, err := strconv.Atoi(c.Query("offset")); err == nil && n >= 0 {
		offset = n
	}
	query := db.GetDB().WithContext(c.Request.Context()).Model(&models.Account{}).Preload("Extra").Preload("User")
	userQuery := db.GetDB().WithContext(c.Request.Context()).Model(&models.User{}).Select("id")
	switch c.Query("status") {
	case "":
	case "active":
		query = query.Where("accounts.is_ban = ?", false).Where("accounts.domain IS NOT NULL OR accounts.user_id IN (?)", userQuery.Where("status = ? AND is_ban = ?", models.UserStatusNormal, false))
	case "pending":
		query = query.Where("accounts.domain IS NULL AND accounts.user_id IN (?)", userQuery.Where("status = ?", models.UserStatusUnapproved))
	case "disabled":
		query = query.Where("accounts.domain IS NULL AND accounts.user_id IN (?)", userQuery.Where("status = ?", models.UserStatusRejected))
	case "suspended":
		query = query.Where("accounts.is_ban = ? OR accounts.user_id IN (?)", true, userQuery.Where("is_ban = ?", true))
	default:
		c.JSON(422, gin.H{"error": "unsupported account status"})
		return
	}
	switch c.Query("origin") {
	case "":
	case "local":
		query = query.Where("accounts.domain IS NULL")
	case "remote":
		query = query.Where("accounts.domain IS NOT NULL")
	default:
		c.JSON(422, gin.H{"error": "invalid origin"})
		return
	}
	if v := c.Query("username"); v != "" {
		query = query.Where("accounts.username LIKE ?", "%"+escapeLike(v)+"%")
	}
	if v := c.Query("display_name"); v != "" {
		query = query.Where("accounts.display_name LIKE ?", "%"+escapeLike(v)+"%")
	}
	if v := c.Query("email"); v != "" {
		query = query.Where("accounts.user_id IN (?)", db.GetDB().Model(&models.User{}).Select("id").Where("email_hash = ?", models.GetEmailHash(v)))
	}
	if c.Query("ip") != "" {
		c.JSON(422, gin.H{"error": "IP search is not supported"})
		return
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	accounts := []*models.Account{}
	if err := query.Order("accounts.id DESC").Limit(limit).Offset(offset).Find(&accounts).Error; err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	result := make([]gin.H, 0, len(accounts))
	for _, a := range accounts {
		result = append(result, serializeAdminAccount(a))
	}
	c.Header("X-Total-Count", strconv.FormatInt(total, 10))
	c.JSON(200, result)
}
func adminAccountID(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(400, gin.H{"error": "invalid account id"})
		return 0, false
	}
	return id, true
}
func GetAccount(c *gin.Context) {
	id, ok := adminAccountID(c)
	if !ok {
		return
	}
	account := &models.Account{}
	err := db.GetDB().WithContext(c.Request.Context()).Preload("Extra").Preload("User").First(account, id).Error
	if err != nil {
		adminAccountError(c, err)
		return
	}
	c.JSON(200, serializeAdminAccount(account))
}
func adminAccountError(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(404, gin.H{"error": "account not found"})
	} else {
		c.JSON(500, gin.H{"error": "internal error"})
	}
}

type AccountActionRequest struct {
	Type                  string `json:"type"`
	ReportId              string `json:"report_id"`
	WarningPresetId       string `json:"warning_preset_id"`
	Text                  string `json:"text"`
	SendEmailNotification bool   `json:"send_email_notification"`
}

func PerformAccountAction(c *gin.Context) {
	var req AccountActionRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(400, gin.H{"error": "invalid request"})
		return
	}
	if req.ReportId != "" || req.WarningPresetId != "" || req.Text != "" || req.SendEmailNotification {
		c.JSON(422, gin.H{"error": "warning and report side effects are not supported by this action"})
		return
	}
	performAdminAccountAction(c, req.Type)
}
func EnableAccount(c *gin.Context)    { performAdminAccountAction(c, "enable") }
func ApproveAccount(c *gin.Context)   { performAdminAccountAction(c, "approve") }
func RejectAccount(c *gin.Context)    { performAdminAccountAction(c, "reject") }
func UnsilenceAccount(c *gin.Context) { performAdminAccountAction(c, "unsilence") }
func UnsuspendAccount(c *gin.Context) { performAdminAccountAction(c, "unsuspend") }
func performAdminAccountAction(c *gin.Context, action string) {
	id, ok := adminAccountID(c)
	if !ok {
		return
	}
	account := &models.Account{}
	code, message := 0, ""
	err := db.GetDB().WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("User").Preload("Extra").First(account, id).Error; err != nil {
			return err
		}
		switch action {
		case "suspend", "unsuspend", "enable", "disable", "approve", "reject":
		default:
			code, message = 422, "unsupported account action"
			return nil
		}
		if action == "suspend" || action == "disable" || action == "reject" {
			current, _ := c.Get("user")
			if user, ok := current.(*models.User); ok && user != nil && account.UserId == user.Id {
				code, message = 422, "cannot disable your own account"
				return nil
			}
			currentAccount, _ := c.Get("account")
			if own, ok := currentAccount.(*models.Account); ok && own != nil && own.Id == id {
				code, message = 422, "cannot disable your own account"
				return nil
			}
		}
		if action == "suspend" || action == "unsuspend" {
			banned := action == "suspend"
			if err := tx.Model(account).UpdateColumn("is_ban", banned).Error; err != nil {
				return err
			}
			account.IsBan = banned
			if account.User != nil {
				if err := tx.Model(account.User).UpdateColumn("is_ban", banned).Error; err != nil {
					return err
				}
				account.User.IsBan = banned
			}
			return nil
		}
		if account.Domain != nil || account.User == nil {
			code, message = 422, "action requires a local login account"
			return nil
		}
		state := account.User.Status
		switch action {
		case "approve", "reject":
			if state != models.UserStatusUnapproved {
				code, message = 422, "account is not awaiting approval"
				return nil
			}
			if action == "approve" {
				state = models.UserStatusNormal
			} else {
				state = models.UserStatusRejected
			}
		case "disable":
			state = models.UserStatusRejected
		case "enable":
			if state != models.UserStatusRejected && state != models.UserStatusNormal {
				code, message = 422, "account requires approval or email verification"
				return nil
			}
			state = models.UserStatusNormal
		}
		if err := tx.Model(account.User).UpdateColumn("status", state).Error; err != nil {
			return err
		}
		account.User.Status = state
		return nil
	})
	if err != nil {
		adminAccountError(c, err)
		return
	}
	if code != 0 {
		c.JSON(code, gin.H{"error": message})
		return
	}
	c.JSON(200, serializeAdminAccount(account))
}

func serializeAdminAccount(account *models.Account) gin.H {
	var domain any
	if account.Domain != nil {
		domain = *account.Domain
	}

	var createdAt, lastStatusAt, lastActivityAt any
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
	silenced := false // Follow approval (Locked) is not moderation silencing.

	result := gin.H{
		"id":               strconv.FormatUint(account.Id, 10),
		"username":         account.Username,
		"domain":           domain,              //nolint:goconst
		"display_name":     account.DisplayName, //nolint:goconst
		"avatar":           account.Avatar,
		"avatar_static":    account.AvatarStatic,
		"header":           account.Header,
		"header_static":    account.HeaderStatic,
		"locked":           account.Locked,
		"bot":              account.Bot,
		"discoverable":     account.Discoverable,
		"note":             account.Note,
		"created_at":       createdAt, //nolint:goconst
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
		result["disabled"] = account.User.Status == models.UserStatusRejected
		result["approved"] = account.User.Status == models.UserStatusNormal
		result["suspended"] = account.IsBan || account.User.IsBan
	}

	return result
}
