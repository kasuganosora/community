package timelines // "error" and similar strings are standard JSON keys, not worth constants

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
)

// GetNotification handles GET /api/v1/notifications/:id.
func GetNotification(c *gin.Context) {
	notificationId := c.Param("id")
	if notificationId == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "notification id required"})

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Query notification
	notification := &models.Notification{}

	err := db.GetDB().Where("id = ? AND account_id = ?", notificationId, account.Id).
		Preload("Account").
		Preload("Account.Emojis").
		Preload("Account.Extra").
		Preload("Status").
		Preload("Status.Account").
		First(notification).Error
	if err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "notification not found"})

		return
	}

	visible, err := serializer.NotificationVisible(notification, account)
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "failed to read notification"})
		return
	}
	if !visible {
		c.AbortWithStatusJSON(404, gin.H{"error": "notification not found"})
		return
	}
	// Build response
	c.JSON(200, buildNotificationResponse(notification, c))
}

// DismissNotification handles POST /api/v1/notifications/:id/dismiss.
func DismissNotification(c *gin.Context) {
	notificationId := c.Param("id")
	if notificationId == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "notification id required"})

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Mark notification as dismissed/read
	result := db.GetDB().Model(&models.Notification{}).
		Where("id = ? AND account_id = ?", notificationId, account.Id).
		Update("is_read", true)

	if result.Error != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	if result.RowsAffected == 0 {
		c.AbortWithStatusJSON(404, gin.H{"error": "notification not found"})

		return
	}

	c.JSON(200, gin.H{})
}

// ClearNotifications handles POST /api/v1/notifications/clear.
func ClearNotifications(c *gin.Context) {
	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Mark all notifications as read
	err := db.GetDB().Model(&models.Notification{}).
		Where("account_id = ?", account.Id).
		Update("is_read", true).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "clear notifications error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	c.JSON(200, gin.H{})
}

// NotificationPolicy represents notification policy settings.
type NotificationPolicy struct {
	FilterNotFollowing    bool `json:"filter_not_following"`
	FilterNotFollowers    bool `json:"filter_not_followers"`
	FilterNewAccounts     bool `json:"filter_new_accounts"`
	FilterPrivateMentions bool `json:"filter_private_mentions"`
}

// GetNotificationPolicy handles GET /api/v1/notifications/policy.
func GetNotificationPolicy(c *gin.Context) {
	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Get notification policy from user settings
	policy := NotificationPolicy{
		FilterNotFollowing:    false,
		FilterNotFollowers:    false,
		FilterNewAccounts:     false,
		FilterPrivateMentions: false,
	}

	// Try to get from user settings
	if account.User != nil && account.User.Settings != nil && account.User.Settings.Data != nil {
		if v, ok := account.User.Settings.Data["notification_filter_not_following"].(bool); ok {
			policy.FilterNotFollowing = v
		}

		if v, ok := account.User.Settings.Data["notification_filter_not_followers"].(bool); ok {
			policy.FilterNotFollowers = v
		}

		if v, ok := account.User.Settings.Data["notification_filter_new_accounts"].(bool); ok {
			policy.FilterNewAccounts = v
		}

		if v, ok := account.User.Settings.Data["notification_filter_private_mentions"].(bool); ok {
			policy.FilterPrivateMentions = v
		}
	}

	c.JSON(200, gin.H{
		"filter_not_following":    policy.FilterNotFollowing,
		"filter_not_followers":    policy.FilterNotFollowers,
		"filter_new_accounts":     policy.FilterNewAccounts,
		"filter_private_mentions": policy.FilterPrivateMentions,
		"summary": gin.H{
			"pending_requests_count":      0,
			"pending_notifications_count": 0,
		},
	})
}

// UpdateNotificationPolicyRequest represents the request for updating notification policy.
type UpdateNotificationPolicyRequest struct {
	FilterNotFollowing    *bool `json:"filter_not_following"`
	FilterNotFollowers    *bool `json:"filter_not_followers"`
	FilterNewAccounts     *bool `json:"filter_new_accounts"`
	FilterPrivateMentions *bool `json:"filter_private_mentions"`
}

// UpdateNotificationPolicy handles POST /api/v1/notifications/policy.
func UpdateNotificationPolicy(c *gin.Context) {
	var req UpdateNotificationPolicyRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid request body"})

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Update user settings
	if account.User != nil && account.User.Settings != nil {
		if account.User.Settings.Data == nil {
			account.User.Settings.Data = make(map[string]any)
		}

		if req.FilterNotFollowing != nil {
			account.User.Settings.Data["notification_filter_not_following"] = *req.FilterNotFollowing
		}

		if req.FilterNotFollowers != nil {
			account.User.Settings.Data["notification_filter_not_followers"] = *req.FilterNotFollowers
		}

		if req.FilterNewAccounts != nil {
			account.User.Settings.Data["notification_filter_new_accounts"] = *req.FilterNewAccounts
		}

		if req.FilterPrivateMentions != nil {
			account.User.Settings.Data["notification_filter_private_mentions"] = *req.FilterPrivateMentions
		}

		// Save settings
		err := db.GetDB().Save(account.User.Settings).Error
		if err != nil {
			_ = c.Error(errors.Wrap(err, "save notification policy error"))
			c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

			return
		}
	}

	c.JSON(200, gin.H{
		"filter_not_following":    req.FilterNotFollowing != nil && *req.FilterNotFollowing,
		"filter_not_followers":    req.FilterNotFollowers != nil && *req.FilterNotFollowers,
		"filter_new_accounts":     req.FilterNewAccounts != nil && *req.FilterNewAccounts,
		"filter_private_mentions": req.FilterPrivateMentions != nil && *req.FilterPrivateMentions,
		"summary": gin.H{
			"pending_requests_count":      0,
			"pending_notifications_count": 0,
		},
	})
}

// buildNotificationResponse builds a notification response.
func buildNotificationResponse(notification *models.Notification, c *gin.Context) gin.H {
	response := gin.H{
		"id":         strconv.FormatUint(notification.Id, 10),
		"type":       notification.Type,
		"created_at": notification.CreatedAt,
		"account":    nil,
		"status":     nil,
	}

	if notification.Account != nil {
		response["account"] = gin.H{
			"id":            strconv.FormatUint(notification.Account.Id, 10),
			"username":      notification.Account.Username,
			"display_name":  notification.Account.DisplayName,
			"avatar":        notification.Account.Avatar,
			"avatar_static": notification.Account.Avatar,
			"url":           "https://" + c.Request.Host + "/@" + notification.Account.Username,
		}
	}

	return response
}
