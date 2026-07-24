package admin

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
)

// GetThumbnailStats 获取缩略图统计
// GET /api/v1/admin/thumbnails/stats.
func GetThumbnailStats(c *gin.Context) {
	var (
		totalCount, completedCount, failedCount, pendingCount int64
		totalStorage                                          int64
	)

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
// GET /api/v1/admin/thumbnails/config.
func GetThumbnailConfig(c *gin.Context) {
	// 从数据库读取缩略图配置
	var configs []models.ThumbnailConfig

	err := db.GetDB().Find(&configs).Error
	if err != nil {
		log.Warn("Failed to load thumbnail configs: %v", err)
		c.JSON(200, gin.H{"enabled": false})

		return
	}

	configMap := make(map[string]string)
	for _, cfg := range configs {
		configMap[cfg.Key] = cfg.Value
	}

	c.JSON(200, configMap)
}

// UpdateThumbnailConfig 更新缩略图配置
// PUT /api/v1/admin/thumbnails/config.
func UpdateThumbnailConfig(c *gin.Context) {
	var req map[string]any

	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid request"})

		return
	}

	// 批量保存配置到数据库
	for key, value := range req {
		strVal, ok := value.(string)
		if !ok {
			continue
		}

		err := db.GetDB().Where(models.ThumbnailConfig{Key: key}).
			Assign(models.ThumbnailConfig{Value: strVal}).
			FirstOrCreate(&models.ThumbnailConfig{}).Error
		if err != nil {
			log.Error("Failed to save thumbnail config %s: %v", key, err)
		}
	}

	c.JSON(200, gin.H{"success": true})
}

// ListThumbnails 获取缩略图列表
// GET /api/v1/admin/thumbnails.
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
// GET /api/v1/admin/thumbnails/:id.
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
// DELETE /api/v1/admin/thumbnails/:id.
func DeleteThumbnail(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid id"})

		return
	}

	var thumb models.Thumbnail
	err = db.GetDB().First(&thumb, id).Error
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})

		return
	}

	err = db.GetDB().Delete(&thumb).Error
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to delete"})

		return
	}

	c.JSON(200, gin.H{"success": true})
}

// RegenerateThumbnail 重新生成缩略图
// POST /api/v1/admin/thumbnails/:id/regenerate.
func RegenerateThumbnail(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid id"})

		return
	}

	var thumb models.Thumbnail
	err = db.GetDB().First(&thumb, id).Error
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})

		return
	}

	thumb.Status = models.ThumbnailStatusPending
	thumb.ErrorMessage = ""
	err = db.GetDB().Save(&thumb).Error
	if err != nil {
		c.JSON(500, gin.H{"error": "internal error"})

		return
	}

	c.JSON(200, gin.H{"success": true})
}

// PurgeThumbnails 清理缩略图
// POST /api/v1/admin/thumbnails/purge.
func PurgeThumbnails(c *gin.Context) {
	var req struct {
		OlderThanDays int    `json:"older_than_days"`
		Status        string `json:"status"`
	}

	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid request"})

		return
	}

	status := req.Status
	if status == "all" {
		status = ""
	}

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

	err = query.Delete(&models.Thumbnail{}).Error
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to delete"})

		return
	}

	c.JSON(200, gin.H{
		"success":    true,
		"deleted":    count,
		"older_days": req.OlderThanDays,
		"status":     req.Status,
	})
}
