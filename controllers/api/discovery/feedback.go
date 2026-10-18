package discovery

import (
	"context"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	service "github.com/kasuganosora/community/discovery"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
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
	conn := db.GetDB().WithContext(ctx)
	feedback, err := models.ListRecommendationFeedback(conn, account.Id)
	if err != nil {
		feedAPIError(c, err)
		return
	}
	rows, err := feedbackLabels(conn, feedback)
	if err != nil {
		feedAPIError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, rows)
}

type labeledFeedback struct {
	models.RecommendationFeedback
	TargetLabel string `json:"target_label,omitempty"`
}

// Resolve existing rules in bounded batches, without a remote lookup per row.
func feedbackLabels(conn *gorm.DB, feedback []models.RecommendationFeedback) ([]labeledFeedback, error) {
	ids := []uint64{}
	seen := map[uint64]bool{}
	for _, row := range feedback {
		if row.TargetKind != "account" {
			continue
		}
		id, err := strconv.ParseUint(row.TargetKey, 10, 64)
		if err == nil && id != 0 && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	labels := map[string]string{}
	for start := 0; start < len(ids); start += 500 {
		var accounts []models.Account
		if err := conn.Select("id", "username", "domain").Where("id IN ?", ids[start:min(start+500, len(ids))]).Find(&accounts).Error; err != nil {
			return nil, err
		}
		for _, account := range accounts {
			if account.Username == "" {
				continue
			}
			label := "@" + account.Username
			if account.Domain != nil && *account.Domain != "" {
				label += "@" + *account.Domain
			}
			labels[strconv.FormatUint(account.Id, 10)] = label
		}
	}
	rows := make([]labeledFeedback, 0, len(feedback))
	for _, row := range feedback {
		label := ""
		if row.TargetKind == "account" {
			label = labels[row.TargetKey]
		}
		rows = append(rows, labeledFeedback{RecommendationFeedback: row, TargetLabel: label})
	}
	return rows, nil
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
