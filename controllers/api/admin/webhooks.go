package admin

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/adminwebhook"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"strings"
	"time"
)

func Webhooks(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionManageWebhooks) {
		return
	}
	page, limit, ok := operationsPage(c)
	if !ok {
		return
	}
	q := db.GetDB().WithContext(c.Request.Context()).Model(&adminwebhook.Subscription{})
	var total int64
	var rows []adminwebhook.Subscription
	if operationError(c, q.Count(&total).Error) || operationError(c, q.Order("id DESC").Offset((page-1)*limit).Limit(limit).Find(&rows).Error) {
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"items": rows, "total": total})
}
func CreateWebhook(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionManageWebhooks) {
		return
	}
	var req struct {
		URL         string   `json:"url"`
		Description string   `json:"description"`
		Events      []string `json:"events"`
		Enabled     bool     `json:"enabled"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(422, gin.H{"error": "invalid subscription"})
		return
	}
	s := &adminwebhook.Subscription{URL: strings.TrimSpace(req.URL), Description: req.Description, Events: req.Events, Enabled: req.Enabled}
	secret, e := adminwebhook.Create(db.GetDB().WithContext(c.Request.Context()), s)
	if e != nil {
		c.JSON(422, gin.H{"error": "subscription requires a valid HTTPS destination and supported events"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(201, gin.H{"subscription": s, "secret": secret})
}
func UpdateWebhook(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionManageWebhooks) {
		return
	}
	id, ok := operationsID(c)
	if !ok {
		return
	}
	var req struct {
		Enabled     *bool     `json:"enabled"`
		Description *string   `json:"description"`
		Events      *[]string `json:"events"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(422, gin.H{"error": "invalid update"})
		return
	}
	updates := map[string]any{}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.Description != nil {
		if len(*req.Description) > 200 {
			c.JSON(422, gin.H{"error": "description too long"})
			return
		}
		updates["description"] = *req.Description
	}
	if req.Events != nil {
		if adminwebhook.ValidateEvents(*req.Events) != nil {
			c.JSON(422, gin.H{"error": "invalid events"})
			return
		}
		updates["events"] = jsonValue(*req.Events)
	}
	if len(updates) == 0 {
		c.JSON(422, gin.H{"error": "no supported changes"})
		return
	}
	r := db.GetDB().WithContext(c.Request.Context()).Model(&adminwebhook.Subscription{}).Where("id = ?", id).Updates(updates)
	if operationError(c, r.Error) {
		return
	}
	if r.RowsAffected == 0 {
		c.JSON(404, gin.H{"error": "subscription not found"})
		return
	}
	c.JSON(200, gin.H{"updated": true})
}
func WebhookAction(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionManageWebhooks) {
		return
	}
	id, ok := operationsID(c)
	if !ok {
		return
	}
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if c.ShouldBindJSON(&req) != nil || !req.Confirm {
		c.JSON(422, gin.H{"error": "explicit confirmation required"})
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	switch c.Param("action") {
	case "rotate":
		secret, e := adminwebhook.Rotate(conn, id)
		if e != nil {
			c.JSON(404, gin.H{"error": "subscription unavailable"})
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(200, gin.H{"secret": secret})
	case "test":
		d, e := adminwebhook.QueueTest(conn, id)
		if e != nil {
			c.JSON(409, gin.H{"error": "enabled subscription required"})
			return
		}
		c.JSON(202, d)
	default:
		c.JSON(422, gin.H{"error": "invalid action"})
	}
}
func WebhookDeliveries(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionManageWebhooks) {
		return
	}
	page, limit, ok := operationsPage(c)
	if !ok {
		return
	}
	q := db.GetDB().WithContext(c.Request.Context()).Model(&adminwebhook.Delivery{})
	if id := c.Query("subscription_id"); id != "" {
		q = q.Where("subscription_id = ?", id)
	}
	if state := c.Query("state"); state != "" {
		q = q.Where("state = ?", state)
	}
	var total int64
	var rows []adminwebhook.Delivery
	if operationError(c, q.Count(&total).Error) || operationError(c, q.Order("created_at DESC").Offset((page-1)*limit).Limit(limit).Find(&rows).Error) {
		return
	}
	c.JSON(200, gin.H{"items": rows, "total": total})
}
func WebhookDeliveryAction(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionManageWebhooks) {
		return
	}
	id := c.Param("id")
	if _, e := uuid.Parse(id); e != nil {
		c.JSON(404, gin.H{"error": "delivery not found"})
		return
	}
	var req struct {
		Action  string `json:"action"`
		Confirm bool   `json:"confirm"`
	}
	if c.ShouldBindJSON(&req) != nil || (req.Action != "retry" && req.Action != "cancel") || (req.Action == "cancel" && !req.Confirm) {
		c.JSON(422, gin.H{"error": "retry or confirmed cancellation required"})
		return
	}
	state := "pending"
	if req.Action == "cancel" {
		state = "cancelled"
	}
	r := db.GetDB().WithContext(c.Request.Context()).Model(&adminwebhook.Delivery{}).Where("id = ? AND state IN ?", id, []string{"failed", "pending"}).Updates(map[string]any{"state": state, "next_attempt_at": time.Now()})
	if operationError(c, r.Error) {
		return
	}
	if r.RowsAffected != 1 {
		c.JSON(409, gin.H{"error": "delivery is running, completed or changed"})
		return
	}
	c.JSON(200, gin.H{"updated": true})
}

func jsonValue(v any) string { b, _ := json.Marshal(v); return string(b) }
