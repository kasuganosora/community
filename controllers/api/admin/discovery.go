package admin

import (
	"context"
	"errors"
	"github.com/kasuganosora/community/search"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/discovery"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

func DiscoveryStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	settings, err := systemsettings.ReadDiscoveryConfiguration(ctx, db.GetDB())
	if err != nil {
		discoveryAdminError(c, err)
		return
	}
	runtime, err := discovery.RuntimeStatus(ctx)
	if err != nil {
		discoveryAdminError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": settings.Enabled, "configured_mode": settings.Mode, "approved_version": settings.ApprovedVersion, "require_manual_approval": settings.RequireManualApproval, "trends_enabled": settings.TrendsEnabled, "algorithm_version": discovery.AlgorithmVersion, "index": discovery.CurrentMeiliStatus(), "runtime": runtime, "search_index": search.ReindexProgress()})
}

// currentDiscoveryIndexVersion is swapped in tests.
var currentDiscoveryIndexVersion = func() string { return discovery.CurrentMeiliStatus().Version }

// ApproveDiscoveryIndex records the active Meili index version as approved.
// The client sends the version it saw so an index rebuilt in between is never
// approved blindly.
func ApproveDiscoveryIndex(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	var input struct {
		Version string `json:"version"`
	}
	if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.Version) == "" || len(input.Version) > 128 {
		c.JSON(422, gin.H{"error": "Missing index version to approve"})
		return
	}
	active := currentDiscoveryIndexVersion()
	if active == "" {
		c.JSON(409, gin.H{"error": "The recommendation index has not finished building; there is no version to approve yet"})
		return
	}
	if strings.TrimSpace(input.Version) != active {
		c.JSON(409, gin.H{"error": "The index version has changed; refresh the page and confirm again", "version": active})
		return
	}
	if _, managed := config.C.Other[systemsettings.KeyRecommendationApprovedVersion]; managed {
		c.JSON(http.StatusConflict, gin.H{"error": "setting is managed by deployment"})
		return
	}
	if err := systemsettings.SaveChanges(map[string]any{systemsettings.KeyRecommendationApprovedVersion: active}); err != nil {
		discoveryAdminError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"approved_version": active})
}

func ListDiscoveryFeedback(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	rows, err := models.ListSiteRecommendationFeedback(db.GetDB().WithContext(ctx))
	if err != nil {
		discoveryAdminError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

func CreateDiscoveryFeedback(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	var input struct {
		TargetKind string `json:"target_kind"`
		TargetKey  string `json:"target_key"`
		Reason     string `json:"reason"`
	}
	if c.ShouldBindJSON(&input) != nil {
		c.JSON(422, gin.H{"error": "Invalid recommendation rule"})
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" || utf8.RuneCountInString(input.Reason) > 500 {
		c.JSON(422, gin.H{"error": "Enter a moderation reason of at most 500 characters"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	var row *models.RecommendationFeedback
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		row, err = models.PutSiteRecommendationFeedback(tx, input.TargetKind, input.TargetKey)
		if err != nil {
			return err
		}
		row.Reason = input.Reason
		if err = tx.Model(row).Update("reason", row.Reason).Error; err != nil {
			return err
		}
		return auditDiscoveryFeedback(c, tx, row)
	})
	if err != nil {
		discoveryAdminError(c, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

func DeleteDiscoveryFeedback(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(422, gin.H{"error": "Invalid rule ID"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	err = db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row models.RecommendationFeedback
		if err := tx.Where("id = ? AND scope = ? AND owner_id = ?", id, "site", 0).Take(&row).Error; err != nil {
			return err
		}
		if err := auditDiscoveryFeedback(c, tx, &row); err != nil {
			return err
		}
		return models.DeleteSiteRecommendationFeedback(tx, row.TargetKind, row.TargetKey)
	})
	if err != nil {
		discoveryAdminError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"removed": true})
}

// This update and the rule mutation share a transaction. Revoking a rule
// keeps its target and original reason in the restricted audit history.
func auditDiscoveryFeedback(c *gin.Context, tx *gorm.DB, row *models.RecommendationFeedback) error {
	id, _ := c.Get("admin_audit_id")
	auditID, ok := id.(uint64)
	if !ok || auditID == 0 {
		return errors.New("audit record unavailable")
	}
	entry := models.AdminAuditLog{Details: map[string]string{"target_kind": row.TargetKind, "target_key": row.TargetKey, "reason": row.Reason}}
	result := tx.Model(&models.AdminAuditLog{}).Where("id = ? AND outcome = ?", auditID, "pending").Select("Details").Updates(&entry)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("audit record unavailable")
	}
	return nil
}

func discoveryAdminError(c *gin.Context, err error) {
	_ = c.Error(err)
	if errors.Is(err, models.ErrInvalidRecommendationFeedback) {
		c.JSON(422, gin.H{"error": "Rule target does not exist or is malformed"})
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(404, gin.H{"error": "Rule not found"})
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Recommendation management is temporarily unavailable, please try again later"})
}
