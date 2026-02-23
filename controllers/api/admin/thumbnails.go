package admin

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/thumbnail"
)

// GetThumbnailStats 获取缩略图统计
// GET /api/v1/admin/thumbnails/stats
func GetThumbnailStats(c *gin.Context) {
	svc := thumbnail.GetService()
	if svc != nil {
		stats, err := svc.GetThumbnailStats()
		if err == nil {
			c.JSON(200, stats)
			return
		}
		log.Warn("Failed to get thumbnail stats: %v", err)
	}

	// 直接查询数据库作为 fallback
	var totalCount, completedCount, failedCount, pendingCount int64
	var totalStorage int64

	db.GetDB().Model(&models.Thumbnail{}).Count(&totalCount)
	db.GetDB().Model(&models.Thumbnail{}).Where("status = ?", models.ThumbnailStatusCompleted).Count(&completedCount)
	db.GetDB().Model(&models.Thumbnail{}).Where("status = ?", models.ThumbnailStatusFailed).Count(&failedCount)
	db.GetDB().Model(&models.Thumbnail{}).Where("status IN ?", []string{models.ThumbnailStatusPending, models.ThumbnailStatusProcessing}).Count(&pendingCount)
	db.GetDB().Model(&models.Thumbnail{}).Select("COALESCE(SUM(thumbnail_size), 0)").Scan(&totalStorage)

	// 获取今日统计
	today := time.Now().Truncate(24 * time.Hour)
	var todayGenerated, todayFailed int64
	db.GetDB().Model(&models.Thumbnail{}).Where("status = ? AND updated_at >= ?", models.ThumbnailStatusCompleted, today).Count(&todayGenerated)
	db.GetDB().Model(&models.Thumbnail{}).Where("status = ? AND updated_at >= ?", models.ThumbnailStatusFailed, today).Count(&todayFailed)

	c.JSON(200, gin.H{
		"total":           totalCount,
		"completed":       completedCount,
		"failed":          failedCount,
		"pending":         pendingCount,
		"storage_bytes":   totalStorage,
		"today_generated": todayGenerated,
		"today_failed":    todayFailed,
	})
}

// GetThumbnailConfig 获取缩略图配置
// GET /api/v1/admin/thumbnails/config
func GetThumbnailConfig(c *gin.Context) {
	cfg, err := thumbnail.LoadConfig()
	if err != nil {
		log.Warn("Failed to load thumbnail config: %v", err)
		c.JSON(200, thumbnail.DefaultConfig())
		return
	}

	// 清除敏感信息
	cfg.Storage.AccessKey = ""
	cfg.Storage.SecretKey = ""

	c.JSON(200, cfg)
}

// UpdateThumbnailConfig 更新缩略图配置
// PUT /api/v1/admin/thumbnails/config
func UpdateThumbnailConfig(c *gin.Context) {
	var req struct {
		Enabled        bool                       `json:"enabled"`
		AccessMode     string                     `json:"access_mode"`
		RewriteEnabled bool                       `json:"rewrite_enabled"`
		Storage        models.ThumbnailStorageConfig `json:"storage"`
		Rules          []models.ThumbnailRule     `json:"rules"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid request"})
		return
	}

	// 获取现有配置以保留凭证
	oldCfg, _ := thumbnail.LoadConfig()
	if oldCfg != nil {
		// 如果请求中没有提供凭证，保留旧的
		if req.Storage.AccessKey == "" {
			req.Storage.AccessKey = oldCfg.Storage.AccessKey
		}
		if req.Storage.SecretKey == "" {
			req.Storage.SecretKey = oldCfg.Storage.SecretKey
		}
	}

	cfg := &thumbnail.Config{
		Enabled:        req.Enabled,
		AccessMode:     thumbnail.AccessMode(req.AccessMode),
		RewriteEnabled: req.RewriteEnabled,
		Storage: thumbnail.StorageConfig{
			Type:       thumbnail.StorageType(req.Storage.Type),
			Bucket:     req.Storage.Bucket,
			Region:     req.Storage.Region,
			Endpoint:   req.Storage.Endpoint,
			PathPrefix: req.Storage.PathPrefix,
			LocalPath:  req.Storage.LocalPath,
			PublicURL:  req.Storage.PublicURL,
			AccessKey:  req.Storage.AccessKey,
			SecretKey:  req.Storage.SecretKey,
		},
		Rules: convertRules(req.Rules),
	}

	if err := thumbnail.SaveConfig(cfg); err != nil {
		log.Error("Failed to save thumbnail config: %v", err)
		c.JSON(500, gin.H{"error": "failed to save config"})
		return
	}

	// 清除敏感信息后返回
	cfg.Storage.AccessKey = ""
	cfg.Storage.SecretKey = ""
	c.JSON(200, cfg)
}

// ListThumbnails 获取缩略图列表
// GET /api/v1/admin/thumbnails
func ListThumbnails(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	status := c.Query("status")

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	var thumbnails []models.Thumbnail
	query := db.GetDB().Model(&models.Thumbnail{}).Order("created_at DESC")

	if status != "" {
		query = query.Where("status = ?", status)
	}

	var total int64
	query.Count(&total)

	offset := (page - 1) * limit
	query.Offset(offset).Limit(limit).Find(&thumbnails)

	c.JSON(200, gin.H{
		"data":  thumbnails,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// GetThumbnail 获取单个缩略图详情
// GET /api/v1/admin/thumbnails/:id
func GetThumbnail(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid id"})
		return
	}

	var thumb models.Thumbnail
	if err := db.GetDB().First(&thumb, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}

	c.JSON(200, thumb)
}

// DeleteThumbnail 删除缩略图
// DELETE /api/v1/admin/thumbnails/:id
func DeleteThumbnail(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid id"})
		return
	}

	svc := thumbnail.GetService()
	if svc != nil {
		if err := svc.DeleteThumbnail(c, id); err != nil {
			log.Error("Failed to delete thumbnail: %v", err)
			c.JSON(500, gin.H{"error": "failed to delete"})
			return
		}
	} else {
		// 直接删除数据库记录
		var thumb models.Thumbnail
		if err := db.GetDB().First(&thumb, id).Error; err != nil {
			c.JSON(404, gin.H{"error": "not found"})
			return
		}
		if err := db.GetDB().Delete(&thumb).Error; err != nil {
			c.JSON(500, gin.H{"error": "failed to delete"})
			return
		}
	}

	c.JSON(200, gin.H{"success": true})
}

// RegenerateThumbnail 重新生成缩略图
// POST /api/v1/admin/thumbnails/:id/regenerate
func RegenerateThumbnail(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid id"})
		return
	}

	svc := thumbnail.GetService()
	if svc != nil {
		if err := svc.RegenerateThumbnail(c, id); err != nil {
			log.Error("Failed to regenerate thumbnail: %v", err)
			c.JSON(500, gin.H{"error": "failed to regenerate"})
			return
		}
	} else {
		// 直接重置状态
		var thumb models.Thumbnail
		if err := db.GetDB().First(&thumb, id).Error; err != nil {
			c.JSON(404, gin.H{"error": "not found"})
			return
		}
		thumb.Status = models.ThumbnailStatusPending
		thumb.ErrorMessage = ""
		if err := db.GetDB().Save(&thumb).Error; err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}
	}

	c.JSON(200, gin.H{"success": true})
}

// PurgeThumbnails 清理缩略图
// POST /api/v1/admin/thumbnails/purge
func PurgeThumbnails(c *gin.Context) {
	var req struct {
		OlderThanDays int    `json:"older_than_days"`
		Status        string `json:"status"` // failed, completed, all
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid request"})
		return
	}

	status := req.Status
	if status == "all" {
		status = ""
	}

	svc := thumbnail.GetService()
	if svc != nil {
		count, err := svc.CleanupThumbnails(c, req.OlderThanDays, status)
		if err != nil {
			log.Error("Failed to cleanup thumbnails: %v", err)
			c.JSON(500, gin.H{"error": "failed to cleanup"})
			return
		}

		c.JSON(200, gin.H{
			"success":     true,
			"deleted":     count,
			"older_days":  req.OlderThanDays,
			"status":      req.Status,
		})
	} else {
		// 直接删除数据库记录
		query := db.GetDB().Model(&models.Thumbnail{})

		if req.OlderThanDays > 0 {
			cutoff := time.Now().AddDate(0, 0, -req.OlderThanDays)
			query = query.Where("updated_at < ?", cutoff)
		}

		if status != "" {
			query = query.Where("status = ?", status)
		}

		var count int64
		query.Count(&count)

		if err := query.Delete(&models.Thumbnail{}).Error; err != nil {
			c.JSON(500, gin.H{"error": "failed to delete"})
			return
		}

		c.JSON(200, gin.H{
			"success":     true,
			"deleted":     count,
			"older_days":  req.OlderThanDays,
			"status":      req.Status,
		})
	}
}

// convertRules 将 models.ThumbnailRule 转换为 thumbnail.ThumbnailRule
func convertRules(rules []models.ThumbnailRule) []thumbnail.ThumbnailRule {
	result := make([]thumbnail.ThumbnailRule, len(rules))
	for i, r := range rules {
		result[i] = thumbnail.ThumbnailRule{
			ContentTypes:      r.ContentTypes,
			Extensions:        r.Extensions,
			MaxOriginalSize:   r.MaxOriginalSize,
			MaxWidth:          r.MaxWidth,
			MaxHeight:         r.MaxHeight,
			Quality:           r.Quality,
			Format:            r.Format,
			Enabled:           r.Enabled,
			ExcludeAnimations: r.ExcludeAnimations,
			MediaType:         r.MediaType,
		}
	}
	return result
}
