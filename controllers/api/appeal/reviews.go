package appeal

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/llmsvc"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/newacctmod"
	"gorm.io/gorm"
)

// Reviews lists actionable automatic reviews, including jobs waiting for an
// LLM configuration and flagged posts that require a human decision.
func Reviews(c *gin.Context) {
	conn := db.GetDB().WithContext(c.Request.Context())
	var jobs []newacctmod.ReviewJob
	q := conn.Where("state <> ? OR verdict = ?", "done", "flag")
	if before, _ := strconv.ParseUint(c.Query("before"), 10, 64); before > 0 {
		q = q.Where("id < ?", before)
	}
	if err := q.Order("id DESC").Limit(41).Find(&jobs).Error; err != nil {
		c.JSON(500, gin.H{"error": "failed to load reviews"})
		return
	}
	hasMore := len(jobs) > 40
	if hasMore {
		jobs = jobs[:40]
	}
	accountIDs, statusIDs := make([]uint64, 0, len(jobs)), make([]uint64, 0, len(jobs))
	for _, job := range jobs {
		accountIDs = append(accountIDs, job.AccountID)
		statusIDs = append(statusIDs, job.StatusID)
	}
	var accounts []models.Account
	var statuses []models.Status
	if len(jobs) > 0 {
		if err := conn.Where("id IN ?", accountIDs).Find(&accounts).Error; err != nil {
			c.JSON(500, gin.H{"error": "failed to load review accounts"})
			return
		}
		if err := conn.Preload("Content").Where("id IN ?", statusIDs).Find(&statuses).Error; err != nil {
			c.JSON(500, gin.H{"error": "failed to load review posts"})
			return
		}
	}
	byAccount := make(map[uint64]models.Account)
	byStatus := make(map[uint64]models.Status)
	for _, a := range accounts {
		byAccount[a.Id] = a
	}
	for _, s := range statuses {
		byStatus[s.Id] = s
	}
	items := make([]gin.H, 0, len(jobs))
	for _, job := range jobs {
		a, accountExists := byAccount[job.AccountID]
		s, statusExists := byStatus[job.StatusID]
		items = append(items, gin.H{
			"id": strconv.FormatUint(job.ID, 10), "account_id": strconv.FormatUint(job.AccountID, 10),
			"status_id": strconv.FormatUint(job.StatusID, 10), "acct": a.Acct, "display_name": a.DisplayName,
			"text": s.Content.EditableText(), "held": s.LocalOnly, "state": job.State, "verdict": job.Verdict,
			"reason": job.Reason, "last_error": job.LastError, "attempts": job.Attempts, "created_at": job.CreatedAt,
			"actionable": accountExists && statusExists && a.IsLocal() && !a.IsBan && a.DeletionRequestedAt == nil,
		})
	}
	_, configured := llmsvc.ServiceProvider(llmsvc.ServiceNewAccountMod)
	if !configured {
		_, configured = llmsvc.ServiceProvider(llmsvc.ServiceTranslation)
	}
	c.JSON(200, gin.H{"items": items, "has_more": hasMore, "enabled": newacctmod.Enabled(), "configured": configured})
}

func Review(c *gin.Context) {
	actor := controllers.GetAccount(c)
	if actor == nil {
		c.Status(http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	var req struct {
		Action string `json:"action" binding:"required,oneof=approve retry"`
	}
	if err != nil || id == 0 || c.ShouldBindJSON(&req) != nil {
		c.JSON(422, gin.H{"error": "invalid review action"})
		return
	}
	err = newacctmod.ReviewManually(c.Request.Context(), db.GetDB(), id, actor.Id, req.Action)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(404, gin.H{"error": "review or post no longer exists"})
	case errors.Is(err, newacctmod.ErrReviewClosed):
		c.JSON(409, gin.H{"error": "review is no longer actionable"})
	case err != nil:
		c.JSON(500, gin.H{"error": "failed to update review"})
	default:
		c.Status(http.StatusNoContent)
	}
}
