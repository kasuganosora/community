package admin

import (
	"crypto/rand"
	"encoding/base64"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func adminInviteJSON(inv *models.InviteCode) gin.H {
	return gin.H{"id": strconv.FormatInt(inv.Id, 10), "code": inv.Code, "account_id": inv.AccountId, "uses": inv.UseCount, "max_uses": inv.MaxUseCount, "expires_at": inv.ExpireAt, "created_at": inv.CreatedAt, "disabled": inv.Disabled, "available": inv.IsAvailable()}
}
func ListAdminInvites(c *gin.Context) {
	conn := db.GetDB().WithContext(c.Request.Context()).Session(&gorm.Session{Logger: logger.Discard})
	if _, ok := requireAdminCapability(c, conn, models.PermissionManageInvites); !ok {
		return
	}
	limit, offset := adminPage(c)
	query := conn.Model(&models.InviteCode{})
	if state := c.Query("state"); state != "" {
		now := time.Now()
		switch state {
		case "active":
			query = query.Where("disabled = ? AND (expire_at IS NULL OR expire_at > ?) AND (max_use_count = 0 OR use_count < max_use_count)", false, now)
		case "disabled":
			query = query.Where("disabled = ?", true)
		case "expired":
			query = query.Where("expire_at <= ?", now)
		case "exhausted":
			query = query.Where("max_use_count > 0 AND use_count >= max_use_count")
		default:
			c.JSON(422, gin.H{"error": "invalid invite state"})
			return
		}
	}
	var total int64
	var invites []models.InviteCode
	if query.Count(&total).Error != nil || query.Order("id DESC").Limit(limit).Offset(offset).Find(&invites).Error != nil {
		c.JSON(500, gin.H{"error": "could not load invitations"})
		return
	}
	rows := make([]gin.H, 0, len(invites))
	for n := range invites {
		rows = append(rows, adminInviteJSON(&invites[n]))
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"items": rows, "total": total})
}
func CreateAdminInvites(c *gin.Context) {
	conn := db.GetDB().WithContext(c.Request.Context()).Session(&gorm.Session{Logger: logger.Discard})
	user, ok := requireAdminCapability(c, conn, models.PermissionManageInvites)
	if !ok {
		return
	}
	var body struct {
		Count     int   `json:"count"`
		MaxUses   int   `json:"max_uses"`
		ExpiresIn int64 `json:"expires_in"`
	}
	if c.ShouldBindJSON(&body) != nil {
		c.JSON(400, gin.H{"error": "invalid invitation request"})
		return
	}
	if body.Count < 1 || body.Count > 100 || body.MaxUses < 0 || body.MaxUses > 10000 || body.ExpiresIn < 0 || body.ExpiresIn > 31536000 {
		c.JSON(422, gin.H{"error": "count must be 1–100, max_uses 0–10000, expires_in 0–31536000 seconds"})
		return
	}
	var account models.Account
	if conn.Where("user_id = ? AND domain IS NULL", user.Id).First(&account).Error != nil {
		c.JSON(422, gin.H{"error": "a local account is required"})
		return
	}
	rows := make([]models.InviteCode, 0, body.Count)
	now := time.Now()
	var expiry *time.Time
	if body.ExpiresIn > 0 {
		until := now.Add(time.Duration(body.ExpiresIn) * time.Second)
		expiry = &until
	}
	for n := 0; n < body.Count; n++ {
		var bytes [24]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			c.JSON(500, gin.H{"error": "could not generate invitation"})
			return
		}
		rows = append(rows, models.InviteCode{Code: base64.RawURLEncoding.EncodeToString(bytes[:]), UserId: user.Id, AccountId: strconv.FormatUint(account.Id, 10), MaxUseCount: body.MaxUses, ExpireAt: expiry, AfterRegisterActionParam: map[string]any{}})
	}
	if err := conn.Transaction(func(tx *gorm.DB) error { return tx.Create(&rows).Error }); err != nil {
		c.JSON(500, gin.H{"error": "could not create invitations"})
		return
	}
	result := make([]gin.H, 0, len(rows))
	for n := range rows {
		result = append(result, adminInviteJSON(&rows[n]))
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(201, gin.H{"items": result})
}
func UpdateAdminInvite(c *gin.Context) {
	conn := db.GetDB().WithContext(c.Request.Context()).Session(&gorm.Session{Logger: logger.Discard})
	if _, ok := requireAdminCapability(c, conn, models.PermissionManageInvites); !ok {
		return
	}
	id, ok := adminAccountID(c)
	if !ok {
		return
	}
	var body struct {
		Disabled *bool `json:"disabled"`
	}
	if c.ShouldBindJSON(&body) != nil || body.Disabled == nil {
		c.JSON(422, gin.H{"error": "disabled boolean is required"})
		return
	}
	var invite models.InviteCode
	if conn.First(&invite, id).Error != nil {
		c.JSON(404, gin.H{"error": "invitation not found"})
		return
	}
	if conn.Model(&invite).Update("disabled", *body.Disabled).Error != nil {
		c.JSON(500, gin.H{"error": "could not update invitation"})
		return
	}
	invite.Disabled = *body.Disabled
	c.Header("Cache-Control", "no-store")
	c.JSON(200, adminInviteJSON(&invite))
}
