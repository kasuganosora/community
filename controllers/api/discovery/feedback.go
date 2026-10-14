package discovery

import (
	"context"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	service "github.com/kasuganosora/community/discovery"
	"github.com/kasuganosora/community/models"
)

type feedbackInput struct {
	Action     string `json:"action"`
	TargetKind string `json:"target_kind"`
	TargetKey  string `json:"target_key"`
}

func ListFeedback(c *gin.Context) {
	account := feedViewer(c)
	if account == nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
	defer cancel()
	feedback, err := models.ListRecommendationFeedback(db.GetDB().WithContext(ctx), account.Id)
	if err != nil {
		feedAPIError(c, err)
		return
	}
	c.JSON(200, feedback)
}

func CreateFeedback(c *gin.Context) { writeFeedFeedback(c, false) }
func DeleteFeedback(c *gin.Context) { writeFeedFeedback(c, true) }

func writeFeedFeedback(c *gin.Context, remove bool) {
	account := feedViewer(c)
	if account == nil {
		return
	}
	var input feedbackInput
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
	defer cancel()
	conn := db.GetDB().WithContext(ctx)
	if remove && c.Param("id") != "" {
		id, parseErr := strconv.ParseUint(c.Param("id"), 10, 64)
		if parseErr != nil || id == 0 {
			feedAPIError(c, service.ErrInvalidRequest)
			return
		}
		var row models.RecommendationFeedback
		result := conn.Where("id = ? AND owner_id = ? AND scope = ?", id, account.Id, "user").Limit(1).Find(&row)
		if result.Error != nil {
			feedAPIError(c, result.Error)
			return
		}
		if result.RowsAffected == 0 {
			c.JSON(404, gin.H{"error": "feedback not found"})
			return
		}
		input = feedbackInput{Action: row.Action, TargetKind: row.TargetKind, TargetKey: row.TargetKey}
	} else if err := c.ShouldBindJSON(&input); err != nil {
		feedAPIError(c, service.ErrInvalidRequest)
		return
	}
	var feedback *models.RecommendationFeedback
	var err error
	// Scope/owner are always fixed by the server, never copied from JSON.
	if remove {
		err = models.DeleteRecommendationFeedback(conn, account.Id, input.Action, input.TargetKind, input.TargetKey)
	} else {
		feedback, err = models.PutRecommendationFeedback(conn, account.Id, input.Action, input.TargetKind, input.TargetKey)
	}
	if err != nil {
		feedAPIError(c, err)
		return
	}
	preference, err := models.GetRecommendationPreference(conn, account.UserId)
	if err != nil {
		feedAPIError(c, err)
		return
	}
	c.JSON(200, gin.H{"feedback": feedback, "preference": preference.Preference, "revision": preference.Revision})
}
