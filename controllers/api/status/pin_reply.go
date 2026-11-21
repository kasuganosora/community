package status

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

type pinReplyRequest struct {
	ReplyId string `json:"reply_id"`
}

// PinReply handles POST /api/v1/statuses/:id/pin_reply.
// Only the author of the parent status can pin a visible reply belonging to its thread
// (inspired by openBshi/app/interface/main/reply).
func PinReply(c *gin.Context) {
	account := controllers.MustGetAccount(c)
	parent := controllers.GetIdFromContext(&models.Status{}, c, db.GetDB(), models.StatusLoadAllQuery)

	if parent.AccountId != account.Id {
		c.JSON(http.StatusForbidden, gin.H{"error": "only the status author can pin a reply"})
		return
	}

	var req pinReplyRequest
	if err := controllers.BindBody(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	replyID, err := strconv.ParseUint(strings.TrimSpace(req.ReplyId), 10, 64)
	if err != nil || replyID == 0 || replyID == parent.Id {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid reply_id"})
		return
	}

	conn := db.GetDB().WithContext(c.Request.Context())
	var reply models.Status
	if err := models.StatusLoadAllQuery(conn).First(&reply, replyID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "reply not found"})
			return
		}
		controllers.AbortIfError(c, err)
		return
	}

	// Verify reply belongs to parent's descendant chain (up to depth 20).
	if !isReplyDescendant(conn, parent.Id, &reply, 20) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "status is not a reply to this post"})
		return
	}
	allowed, err := reply.CanViewEx(conn, account)
	if err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	if !allowed {
		c.JSON(http.StatusNotFound, gin.H{"error": "reply not found"})
		return
	}
	if reply.Visibility == models.StatusVisibilityDirect ||
		((parent.Visibility == models.StatusVisibilityPublic || parent.Visibility == models.StatusVisibilityUnlisted) && reply.Visibility == models.StatusVisibilityPrivate) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "cannot pin a private or direct reply"})
		return
	}

	if err := conn.Model(&models.Status{}).Where("id = ?", parent.Id).UpdateColumn("pinned_reply_id", reply.Id).Error; err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status_id":       strconv.FormatUint(parent.Id, 10),
		"pinned_reply_id": strconv.FormatUint(reply.Id, 10),
	})
}

// UnpinReply handles DELETE /api/v1/statuses/:id/pin_reply.
func UnpinReply(c *gin.Context) {
	account := controllers.MustGetAccount(c)
	parent := controllers.GetIdFromContext(&models.Status{}, c, db.GetDB(), models.StatusLoadAllQuery)

	if parent.AccountId != account.Id {
		c.JSON(http.StatusForbidden, gin.H{"error": "only the status author can unpin a reply"})
		return
	}

	conn := db.GetDB().WithContext(c.Request.Context())
	if err := conn.Model(&models.Status{}).Where("id = ?", parent.Id).UpdateColumn("pinned_reply_id", nil).Error; err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status_id":       strconv.FormatUint(parent.Id, 10),
		"pinned_reply_id": nil,
	})
}

func isReplyDescendant(conn *gorm.DB, rootID uint64, candidate *models.Status, maxDepth int) bool {
	cur := candidate
	visited := map[uint64]bool{candidate.Id: true}
	for range maxDepth {
		if cur.InReplyToId == nil || *cur.InReplyToId == 0 {
			return false
		}
		if *cur.InReplyToId == rootID {
			return true
		}
		nextID := *cur.InReplyToId
		if visited[nextID] {
			return false
		}
		visited[nextID] = true
		var parent models.Status
		if err := conn.Select("id", "in_reply_to_id").First(&parent, nextID).Error; err != nil {
			return false
		}
		cur = &parent
	}
	return false
}
