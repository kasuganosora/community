package appeal

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/newacctmod"
	"gorm.io/gorm"
)

// List returns the signed-in account's suspension appeals.
func List(c *gin.Context) {
	account := requireLocal(c)
	if account == nil {
		return
	}
	rows, err := newacctmod.ListAccountAppeals(db.GetDB().WithContext(c.Request.Context()), account.Id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list appeals"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, appealJSON(&rows[i]))
	}
	// Keep the array response for existing API clients. The web page also
	// needs the current case so it never offers a second or ineligible appeal.
	if c.Query("include_context") == "true" {
		var suspension gin.H
		suspended := account.IsBan
		if value, ok := c.Get("user"); ok {
			if user, ok := value.(*models.User); ok && user.IsBan {
				suspended = true
			}
		}
		if account.IsBan {
			conn := db.GetDB().WithContext(c.Request.Context())
			job, err := newacctmod.SuspensionJob(conn, account.Id, 0)
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load suspension"})
				return
			}
			if err == nil {
				suspension = gin.H{"status_id": strconv.FormatUint(job.StatusID, 10), "can_appeal": true}
				var count int64
				if err := conn.Model(&newacctmod.Appeal{}).Where("account_id = ? AND suspension_key = ?", account.Id, newacctmod.SuspensionKeyFor(account.Id, job.StatusID)).Count(&count).Error; err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list appeals"})
					return
				}
				suspension["can_appeal"] = count == 0
			}
		}
		c.JSON(http.StatusOK, gin.H{"items": out, "suspended": suspended, "suspension": suspension})
		return
	}
	c.JSON(http.StatusOK, out)
}

// Create files the one appeal allowed for an automatic suspension.
func Create(c *gin.Context) {
	account := requireLocal(c)
	if account == nil {
		return
	}
	if !account.IsBan {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "account is not suspended"})
		return
	}
	var req struct {
		Text     string `json:"text"`
		StatusID string `json:"status_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "appeal body is required"})
		return
	}
	var statusID uint64
	if req.StatusID != "" {
		parsed, err := strconv.ParseUint(req.StatusID, 10, 64)
		if err != nil || parsed == 0 {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid status id"})
			return
		}
		statusID = parsed
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	job, err := newacctmod.SuspensionJob(conn, account.Id, statusID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "no suspension to appeal"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load suspension"})
		return
	}
	row, err := newacctmod.CreateAppeal(conn, account.Id, job.StatusID, req.Text)
	if err != nil {
		switch {
		case errors.Is(err, newacctmod.ErrEmptyAppeal):
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "appeal body is required"})
		case errors.Is(err, newacctmod.ErrAppealExists):
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "appeal already submitted for this case"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create appeal"})
		}
		return
	}
	c.JSON(http.StatusOK, appealJSON(row))
}

// AdminList lists appeals for moderators.
func AdminList(c *gin.Context) {
	limit := 40
	if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 && n <= 80 {
		limit = n
	}
	offset := 0
	if n, err := strconv.Atoi(c.Query("offset")); err == nil && n > 0 {
		offset = n
	}
	q := db.GetDB().WithContext(c.Request.Context()).Model(&newacctmod.Appeal{}).Order("id DESC").Limit(limit).Offset(offset)
	if state := c.Query("state"); state != "" {
		q = q.Where("state = ?", state)
	}
	var rows []newacctmod.Appeal
	if err := q.Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list appeals"})
		return
	}
	ids := make([]uint64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.AccountID)
	}
	var accounts []models.Account
	if len(ids) > 0 {
		if err := db.GetDB().WithContext(c.Request.Context()).Select("id", "username", "domain").Where("id IN ?", ids).Find(&accounts).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load appeal accounts"})
			return
		}
	}
	accts := make(map[uint64]string)
	for _, a := range accounts {
		accts[a.Id] = publicAcct(&a)
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		item := appealJSON(&rows[i])
		item["account_acct"] = accts[rows[i].AccountID]
		out = append(out, item)
	}
	c.JSON(http.StatusOK, out)
}

// Resolve marks an open appeal accepted or rejected.
// Restoring login stays on the existing admin unsuspend action.
func Resolve(c *gin.Context) {
	admin := controllers.GetAccount(c)
	if admin == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "must login"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid id"})
		return
	}
	var req struct {
		Accept *bool  `json:"accept"`
		Note   string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Accept == nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "accept is required"})
		return
	}
	row, err := newacctmod.ResolveAppeal(db.GetDB().WithContext(c.Request.Context()), id, admin.Id, *req.Accept, req.Note)
	if err != nil {
		switch {
		case errors.Is(err, newacctmod.ErrAppealNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "appeal not found"})
		case errors.Is(err, newacctmod.ErrAppealClosed):
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "appeal already resolved"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve appeal"})
		}
		return
	}
	c.JSON(http.StatusOK, appealJSON(row))
}

func requireLocal(c *gin.Context) *models.Account {
	account := controllers.GetAccount(c)
	if account == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "must login"})
		return nil
	}
	if !account.IsLocal() {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "appeal requires a local account"})
		return nil
	}
	return account
}

// publicAcct is the handle people see. The accounts.acct column stores
// local users as username@__LOCAL__, which is not a profile URL.
func publicAcct(a *models.Account) string {
	if a == nil {
		return ""
	}
	if a.Domain != nil && *a.Domain != "" {
		return a.Username + "@" + *a.Domain
	}
	return a.Username
}

func appealJSON(a *newacctmod.Appeal) gin.H {
	out := gin.H{
		"id":         strconv.FormatUint(a.ID, 10),
		"account_id": strconv.FormatUint(a.AccountID, 10),
		"status_id":  strconv.FormatUint(a.StatusID, 10),
		"text":       a.Body,
		"state":      a.State,
		"created_at": a.CreatedAt.UTC().Format(time.RFC3339),
	}
	if a.ResolvedAt != nil {
		out["resolved_at"] = a.ResolvedAt.UTC().Format(time.RFC3339)
	}
	if a.AdminNote != "" {
		out["admin_note"] = a.AdminNote
	}
	return out
}
