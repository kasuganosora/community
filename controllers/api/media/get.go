package media

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/storage"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// Get 获取媒体信息
func Get(c *gin.Context) {
	mediaId := c.Param("id")

	var media models.MediaAttachment
	if err := db.GetDB().Where("id = ?", mediaId).First(&media).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "Not found"})
			return
		}
		err = errors.Wrap(err, "Get media")
		_ = c.Error(err)
		return
	}

	response, err := serializer.ToMediaAttachment(&media)
	if err != nil {
		err = errors.Wrap(err, "Serialize media")
		_ = c.Error(err)
		return
	}

	c.JSON(200, response)
}

// Update 更新媒体信息
func Update(c *gin.Context) {
	// 获取当前用户
	account := c.MustGet("account").(*models.Account)
	mediaId := c.Param("id")

	var media models.MediaAttachment
	if err := db.GetDB().Where("id = ? AND account_id = ?", mediaId, account.Id).First(&media).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "Not found"})
			return
		}
		err = errors.Wrap(err, "Get media")
		_ = c.Error(err)
		return
	}

	// 解析请求
	var request struct {
		Description string `json:"description"`
		Focus       string `json:"focus"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request"})
		return
	}

	// 更新描述
	if request.Description != "" {
		media.Description = request.Description
	}

	// 更新焦点
	if request.Focus != "" {
		// 解析焦点位置 "x,y"
		parts := strings.Split(request.Focus, ",")
		if len(parts) == 2 {
			x, errX := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			y, errY := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			if errX == nil && errY == nil {
				// Focus coordinates should be in range [-1, 1]
				if x >= -1 && x <= 1 {
					media.FocusX = x
				}
				if y >= -1 && y <= 1 {
					media.FocusY = y
				}
			}
		}
	}

	// 保存更新
	if err := db.GetDB().Save(&media).Error; err != nil {
		err = errors.Wrap(err, "Update media")
		_ = c.Error(err)
		return
	}

	response, err := serializer.ToMediaAttachment(&media)
	if err != nil {
		err = errors.Wrap(err, "Serialize media")
		_ = c.Error(err)
		return
	}

	c.JSON(200, response)
}

// Delete 删除媒体
func Delete(c *gin.Context) {
	// 获取当前用户
	account := c.MustGet("account").(*models.Account)
	mediaId := c.Param("id")

	var media models.MediaAttachment
	if err := db.GetDB().Where("id = ? AND account_id = ?", mediaId, account.Id).First(&media).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "Not found"})
			return
		}
		err = errors.Wrap(err, "Get media")
		_ = c.Error(err)
		return
	}

	// 检查是否已被嘟文使用
	if media.StatusId != nil {
		c.JSON(422, gin.H{"error": "Cannot delete media that is attached to a status"})
		return
	}

	// 删除物理文件
	if media.FilePath != "" {
		_ = storage.Delete(media.FilePath)
	}

	// 删除缩略图
	if media.ThumbnailPath != nil && *media.ThumbnailPath != "" {
		_ = storage.Delete(*media.ThumbnailPath) // Log error but continue with database deletion
	}

	// 删除数据库记录
	if err := db.GetDB().Delete(&media).Error; err != nil {
		err = errors.Wrap(err, "Delete media")
		_ = c.Error(err)
		return
	}

	c.JSON(200, gin.H{})
}
