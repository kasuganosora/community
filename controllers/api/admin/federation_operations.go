package admin

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func operationsAuthorized(c *gin.Context, permission uint64) bool {
	a := controllers.GetAccount(c)
	if a == nil || a.UserId == 0 {
		c.AbortWithStatusJSON(401, gin.H{"error": "authentication required"})
		return false
	}
	var u models.User
	if db.GetDB().WithContext(c.Request.Context()).First(&u, a.UserId).Error != nil || u.IsBan || u.Status != models.UserStatusNormal {
		c.AbortWithStatusJSON(403, gin.H{"error": "account unavailable"})
		return false
	}
	if u.IsAdmin {
		return true
	}
	p, e := models.GetUserPermissions(db.GetDB(), u.Id)
	if e != nil || (p&permission == 0 && p&models.PermissionAdministrator == 0) {
		c.AbortWithStatusJSON(403, gin.H{"error": "permission required"})
		return false
	}
	return true
}
func operationsPage(c *gin.Context) (int, int, bool) {
	page, limit := 1, 30
	var e error
	if v := c.Query("page"); v != "" {
		page, e = strconv.Atoi(v)
		if e != nil || page < 1 || page > 100000 {
			c.JSON(422, gin.H{"error": "invalid page"})
			return 0, 0, false
		}
	}
	if v := c.Query("limit"); v != "" {
		limit, e = strconv.Atoi(v)
		if e != nil || limit < 1 || limit > 50 {
			c.JSON(422, gin.H{"error": "limit must be 1–50"})
			return 0, 0, false
		}
	}
	return page, limit, true
}
func operationsID(c *gin.Context) (uint64, bool) {
	id, e := strconv.ParseUint(c.Param("id"), 10, 64)
	if e != nil || id == 0 {
		c.JSON(404, gin.H{"error": "not found"})
		return 0, false
	}
	return id, true
}
func operationError(c *gin.Context, e error) bool {
	if e == nil {
		return false
	}
	c.JSON(500, gin.H{"error": "operation could not be completed"})
	return true
}
func FederationInstances(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionManageFederation) {
		return
	}
	page, limit, ok := operationsPage(c)
	if !ok {
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	q := conn.Model(&models.Account{}).Where("domain IS NOT NULL AND domain <> ''")
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		if len(search) > 255 {
			c.JSON(422, gin.H{"error": "search too long"})
			return
		}
		q = q.Where("domain "+dialect.ILike(q)+" ?", "%"+search+"%")
	}
	var total int64
	if operationError(c, q.Distinct("domain").Count(&total).Error) {
		return
	}
	type instance struct {
		Domain            string     `json:"domain"`
		Accounts          int64      `json:"accounts"`
		SuspendedAccounts int64      `json:"suspended_accounts"`
		LastActivity      *time.Time `json:"last_activity"`
		Following         int64      `json:"following"`
		Followers         int64      `json:"followers"`
	}
	var rows []instance
	if operationError(c, q.Select("domain, COUNT(*) AS accounts, SUM(CASE WHEN is_ban = ? THEN 1 ELSE 0 END) AS suspended_accounts, MAX(last_activity_at) AS last_activity", true).Group("domain").Order("domain").Offset((page-1)*limit).Limit(limit).Scan(&rows).Error) {
		return
	}
	for i := range rows {
		r := &rows[i]
		if operationError(c, conn.Model(&models.Follow{}).Where("status = ? AND follow_id IN (SELECT id FROM accounts WHERE domain = ?) AND account_id IN (SELECT id FROM accounts WHERE domain IS NULL)", models.FollowStatusFollowed, r.Domain).Count(&r.Following).Error) {
			return
		}
		if operationError(c, conn.Model(&models.Follow{}).Where("status = ? AND account_id IN (SELECT id FROM accounts WHERE domain = ?) AND follow_id IN (SELECT id FROM accounts WHERE domain IS NULL)", models.FollowStatusFollowed, r.Domain).Count(&r.Followers).Error) {
			return
		}
	}
	c.JSON(200, gin.H{"items": rows, "total": total, "page": page, "limit": limit, "domain_blocking_supported": false})
}

var operationURL = regexp.MustCompile(`https?://[^\s]+`)

func safeJobError(value string) string {
	value = operationURL.ReplaceAllString(value, "[remote URL]")
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	if len(value) > 400 {
		value = value[:400] + "…"
	}
	return value
}
func safeInbox(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
func FederationJobs(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionDevops) {
		return
	}
	page, limit, ok := operationsPage(c)
	if !ok {
		return
	}
	kind := c.DefaultQuery("kind", "delivery")
	state := c.Query("state")
	conn := db.GetDB().WithContext(c.Request.Context())
	items := []gin.H{}
	var total int64
	switch kind {
	case "matrix-direct":
		DirectDeliveryJobs(c)
		return
	case "feed-event", "feed-batch":
		feedJobs(c, conn, kind, state, page, limit)
		return
	case "delivery":
		q := conn.Model(&activitypub.Delivery{})
		if state != "" {
			q = q.Where("status = ?", state)
		}
		if operationError(c, q.Count(&total).Error) {
			return
		}
		var rows []activitypub.Delivery
		if operationError(c, q.Order("id DESC").Offset((page-1)*limit).Limit(limit).Find(&rows).Error) {
			return
		}
		for _, r := range rows {
			items = append(items, gin.H{"id": fmt.Sprint(r.ID), "kind": kind, "state": r.Status, "activity_id": r.ActivityID, "destination": safeInbox(r.TargetInbox), "attempts": r.Retries, "last_error": safeJobError(r.LastError), "next_attempt_at": r.NextRetryAt, "created_at": r.CreatedAt})
		}
	case "outbox":
		q := conn.Model(&models.FederationOutbox{})
		if state != "" {
			q = q.Where("state = ?", state)
		}
		if operationError(c, q.Count(&total).Error) {
			return
		}
		var rows []models.FederationOutbox
		if operationError(c, q.Order("id DESC").Offset((page-1)*limit).Limit(limit).Find(&rows).Error) {
			return
		}
		for _, r := range rows {
			items = append(items, gin.H{"id": fmt.Sprint(r.ID), "kind": kind, "state": r.State, "event": r.Kind, "object_id": fmt.Sprint(r.ObjectID), "account_id": fmt.Sprint(r.AccountID), "attempts": r.Attempts, "last_error": safeJobError(r.LastError), "next_attempt_at": r.NextAttemptAt, "created_at": r.CreatedAt})
		}
	default:
		c.JSON(422, gin.H{"error": "invalid queue kind"})
		return
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": page, "limit": limit})
}
func FederationJobAction(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionDevops) {
		return
	}
	id, ok := operationsID(c)
	if !ok {
		return
	}
	var body struct {
		Action  string `json:"action"`
		Confirm bool   `json:"confirm"`
	}
	if c.ShouldBindJSON(&body) != nil || (body.Action != "retry" && body.Action != "cancel") || (body.Action == "cancel" && !body.Confirm) {
		c.JSON(422, gin.H{"error": "retry or confirmed cancellation required"})
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	var q *gorm.DB
	updates := map[string]any{}
	switch c.Param("kind") {
	case "feed-event", "feed-batch":
		feedJobAction(c, conn, c.Param("kind"), id, body.Action)
		return
	case "delivery":
		q = conn.Model(&activitypub.Delivery{}).Where("id = ? AND status IN ?", id, []string{"failed", "pending"})
		updates["status"] = "pending"
		updates["next_retry_at"] = time.Now()
		if body.Action == "cancel" {
			updates["status"] = "cancelled"
		}
	case "outbox":
		q = conn.Model(&models.FederationOutbox{}).Where("id = ? AND state IN ?", id, []string{"failed", "pending"})
		updates["state"] = "pending"
		updates["next_attempt_at"] = time.Now()
		if body.Action == "cancel" {
			updates["state"] = "cancelled"
		}
	default:
		c.JSON(422, gin.H{"error": "invalid queue kind"})
		return
	}
	r := q.Updates(updates)
	if operationError(c, r.Error) {
		return
	}
	if r.RowsAffected != 1 {
		c.JSON(409, gin.H{"error": "job is running, completed or changed; refresh before retrying"})
		return
	}
	c.JSON(200, gin.H{"updated": true})
}
