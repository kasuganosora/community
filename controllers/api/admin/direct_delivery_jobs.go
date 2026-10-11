package admin

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/matrix/homeserver"
	"github.com/kasuganosora/community/models"
)

// DirectDeliveryJobs intentionally selects metadata only. Administrators do
// not gain access to private room membership, message bodies or raw errors.
func DirectDeliveryJobs(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionDevops) {
		return
	}
	page, limit, ok := operationsPage(c)
	if !ok {
		return
	}
	q := db.GetDB().WithContext(c.Request.Context()).Model(&homeserver.MatrixDMOutbound{})
	if state := c.Query("state"); state != "" {
		switch state {
		case "pending", "processing", "delivered", "failed", "skipped":
		default:
			c.JSON(422, gin.H{"error": "invalid state"})
			return
		}
		q = q.Where("state = ?", state)
	}
	var total int64
	if operationError(c, q.Count(&total).Error) {
		return
	}
	var rows []homeserver.MatrixDMOutbound
	if operationError(c, q.Select("event_id", "state", "status_id", "attempts", "last_error", "next_attempt_at", "created_at", "updated_at").Order("created_at DESC,event_id DESC").Offset((page-1)*limit).Limit(limit).Find(&rows).Error) {
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		code := ""
		if row.LastError != "" {
			code = "direct_bridge_unavailable"
		}
		items = append(items, gin.H{"id": row.EventID, "kind": "matrix-direct", "state": row.State, "status_id": row.StatusID, "attempts": row.Attempts, "last_error": code, "next_attempt_at": row.NextAttemptAt, "created_at": row.CreatedAt, "updated_at": row.UpdatedAt, "transport_state_separate": true})
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": page, "limit": limit})
}
