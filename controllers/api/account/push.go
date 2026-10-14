package account

import (
	"github.com/kasuganosora/community/controllers"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
)

// PushSubscription represents a push notification subscription.
type PushSubscription struct {
	Id        uint64     `gorm:"primary_key;autoIncrement" json:"id"`
	AccountId uint64     `gorm:"not null;index"             json:"account_id"`
	Endpoint  string     `gorm:"type:text;not null"         json:"endpoint"`
	AuthKey   string     `gorm:"type:varchar(64)"           json:"auth_key"`
	P256dhKey string     `gorm:"type:varchar(128)"          json:"p256dh_key"`
	Alerts    string     `gorm:"type:json"                  json:"alerts"` // JSON encoded alerts config
	CreatedAt *time.Time `gorm:"not null;autoCreateTime"    json:"created_at"`
	UpdatedAt *time.Time `gorm:"not null;autoUpdateTime"    json:"updated_at"`
}

func (p *PushSubscription) TableName() string {
	return "push_subscriptions"
}

// CreatePushSubscriptionRequest represents the request for creating a push subscription.
type CreatePushSubscriptionRequest struct {
	Subscription struct {
		Endpoint string `binding:"required" json:"endpoint"`
		Keys     struct {
			P256dh string `binding:"required" json:"p256dh"`
			Auth   string `binding:"required" json:"auth"`
		} `json:"keys" binding:"required"`
	} `binding:"required" json:"subscription"`
	Data struct {
		Alerts struct {
			Follow        *bool `json:"follow"`
			FollowRequest *bool `json:"follow_request"`
			Favourite     *bool `json:"favourite"`
			Reblog        *bool `json:"reblog"`
			Mention       *bool `json:"mention"`
			Poll          *bool `json:"poll"`
			Status        *bool `json:"status"`
			Update        *bool `json:"update"`
		} `json:"alerts"`
		Policy *string `json:"policy"`
	} `json:"data"`
}

// CreatePushSubscription handles POST /api/v1/push/subscription.
func CreatePushSubscription(c *gin.Context) {
	var req CreatePushSubscriptionRequest
	err := controllers.BindBody(c, &req)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid request body"}) //nolint:goconst

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Delete existing subscription for this account
	db.GetDB().Where("account_id = ?", account.Id).Delete(&PushSubscription{})

	// Build alerts JSON
	alerts := buildAlertsJSON(&req.Data.Alerts)

	// Create new subscription
	now := time.Now()
	subscription := &PushSubscription{
		AccountId: account.Id,
		Endpoint:  req.Subscription.Endpoint,
		AuthKey:   req.Subscription.Keys.Auth,
		P256dhKey: req.Subscription.Keys.P256dh,
		Alerts:    alerts,
		CreatedAt: &now,
		UpdatedAt: &now,
	}

	err = db.GetDB().Create(subscription).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "create push subscription error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"}) //nolint:goconst

		return
	}

	c.JSON(200, buildPushSubscriptionResponse(subscription))
}

// GetPushSubscription handles GET /api/v1/push/subscription.
func GetPushSubscription(c *gin.Context) {
	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	subscription := &PushSubscription{}

	err := db.GetDB().Where("account_id = ?", account.Id).First(subscription).Error
	if err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "push subscription not found"})

		return
	}

	c.JSON(200, buildPushSubscriptionResponse(subscription))
}

// UpdatePushSubscriptionRequest represents the request for updating a push subscription.
type UpdatePushSubscriptionRequest struct {
	Data struct {
		Alerts struct {
			Follow        *bool `json:"follow"`
			FollowRequest *bool `json:"follow_request"`
			Favourite     *bool `json:"favourite"`
			Reblog        *bool `json:"reblog"`
			Mention       *bool `json:"mention"`
			Poll          *bool `json:"poll"`
			Status        *bool `json:"status"`
			Update        *bool `json:"update"`
		} `json:"alerts"`
		Policy *string `json:"policy"`
	} `json:"data"`
}

// UpdatePushSubscription handles PUT /api/v1/push/subscription.
func UpdatePushSubscription(c *gin.Context) {
	var req UpdatePushSubscriptionRequest
	if err := controllers.BindBody(c, &req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid request body"})

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	subscription := &PushSubscription{}

	err := db.GetDB().Where("account_id = ?", account.Id).First(subscription).Error
	if err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "push subscription not found"})

		return
	}

	// Update alerts
	alerts := buildAlertsJSON(&req.Data.Alerts)
	now := time.Now()

	if err := db.GetDB().Model(subscription).Updates(map[string]any{
		"alerts":     alerts,
		"updated_at": &now,
	}).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "update push subscription error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	c.JSON(200, buildPushSubscriptionResponse(subscription))
}

// DeletePushSubscription handles DELETE /api/v1/push/subscription.
func DeletePushSubscription(c *gin.Context) {
	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	db.GetDB().Where("account_id = ?", account.Id).Delete(&PushSubscription{})

	c.JSON(200, gin.H{})
}

// buildAlertsJSON builds alerts JSON from request.
func buildAlertsJSON(alerts *struct {
	Follow        *bool `json:"follow"`
	FollowRequest *bool `json:"follow_request"`
	Favourite     *bool `json:"favourite"`
	Reblog        *bool `json:"reblog"`
	Mention       *bool `json:"mention"`
	Poll          *bool `json:"poll"`
	Status        *bool `json:"status"`
	Update        *bool `json:"update"`
}) string {
	follow := true
	followRequest := true
	favourite := true
	reblog := true
	mention := true
	poll := true
	status := false
	update := false

	if alerts.Follow != nil {
		follow = *alerts.Follow
	}

	if alerts.FollowRequest != nil {
		followRequest = *alerts.FollowRequest
	}

	if alerts.Favourite != nil {
		favourite = *alerts.Favourite
	}

	if alerts.Reblog != nil {
		reblog = *alerts.Reblog
	}

	if alerts.Mention != nil {
		mention = *alerts.Mention
	}

	if alerts.Poll != nil {
		poll = *alerts.Poll
	}

	if alerts.Status != nil {
		status = *alerts.Status
	}

	if alerts.Update != nil {
		update = *alerts.Update
	}

	return `{"follow":` + strconv.FormatBool(follow) +
		`,"follow_request":` + strconv.FormatBool(followRequest) +
		`,"favourite":` + strconv.FormatBool(favourite) +
		`,"reblog":` + strconv.FormatBool(reblog) +
		`,"mention":` + strconv.FormatBool(mention) +
		`,"poll":` + strconv.FormatBool(poll) +
		`,"status":` + strconv.FormatBool(status) +
		`,"update":` + strconv.FormatBool(update) + `}`
}

// buildPushSubscriptionResponse builds the push subscription response.
func buildPushSubscriptionResponse(subscription *PushSubscription) gin.H {
	return gin.H{
		"id":       strconv.FormatUint(subscription.Id, 10),
		"endpoint": subscription.Endpoint,
		"alerts": gin.H{
			"follow":         true,
			"follow_request": true,
			"favourite":      true,
			"reblog":         true,
			"mention":        true,
			"poll":           true,
			"status":         false,
			"update":         false,
		},
		"server_key": "",
		"policy":     "all",
	}
}
