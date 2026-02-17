package media

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxImageSize 图片最大大小 10MB
	MaxImageSize = 10 * 1024 * 1024
	// MaxVideoSize 视频/音频最大大小 40MB
	MaxVideoSize = 40 * 1024 * 1024
	// AllowedImageTypes 允许的图片类型
	AllowedImageTypes = "image/jpeg,image/png,image/gif,image/webp"
	// AllowedVideoTypes 允许的视频类型
	AllowedVideoTypes = "video/mp4,video/webm,video/quicktime"
	// AllowedAudioTypes 允许的音频类型
	AllowedAudioTypes = "audio/mpeg,audio/ogg,audio/wav"
)

// Upload 上传媒体文件
func Upload(c *gin.Context) {
	// 获取当前用户
	account := c.MustGet("account").(*models.Account)

	// 获取上传的文件
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(400, gin.H{"error": "No file provided"})
		return
	}
	defer file.Close()

	// 获取文件描述（可选）
	description := c.PostForm("description")

	// 获取焦点位置（可选）
	var focusX, focusY float64 = 0, 0
	if focus := c.PostForm("focus"); focus != "" {
		parts := strings.Split(focus, ",")
		if len(parts) == 2 {
			if x, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64); err == nil {
				focusX = x
			}
			if y, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
				focusY = y
			}
		}
	}

	// 验证文件类型
	contentType := header.Header.Get("Content-Type")
	if !isAllowedContentType(contentType) {
		c.JSON(415, gin.H{"error": "Unsupported media type"})
		return
	}

	// 验证文件大小
	if !isValidFileSize(header.Size, contentType) {
		c.JSON(413, gin.H{"error": "File size too large"})
		return
	}

	// 创建媒体目录
	mediaDir := filepath.Join("uploads", "media", time.Now().Format("2006/01/02"))
	if err := os.MkdirAll(mediaDir, 0755); err != nil {
		err = errors.Wrap(err, "Failed to create media directory")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})
		return
	}

	// 生成文件名
	ext := filepath.Ext(header.Filename)
	fileName := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	filePath := filepath.Join(mediaDir, fileName)

	// 创建目标文件
	dst, err := os.Create(filePath)
	if err != nil {
		err = errors.Wrap(err, "Failed to create file")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})
		return
	}
	defer dst.Close()

	// 复制文件内容
	if _, err := io.Copy(dst, file); err != nil {
		err = errors.Wrap(err, "Failed to save file")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})
		return
	}

	// 获取文件大小
	fileInfo, _ := os.Stat(filePath)
	fileSize := fileInfo.Size()

	// 创建缩略图（如果是图片）
	var thumbnailPath *string
	mediaType := models.GetMediaType(contentType)
	if mediaType == models.MediaTypeImage {
		thumbName := fmt.Sprintf("thumb_%s", fileName)
		thumbPath := filepath.Join(mediaDir, thumbName)
		if err := createThumbnail(filePath, thumbPath); err == nil {
			thumbnailPath = &thumbName
		}
	}

	// 获取图片尺寸
	var width, height *int
	if mediaType == models.MediaTypeImage {
		w, h, err := getImageDimensions(filePath)
		if err == nil {
			width = &w
			height = &h
		}
	}

	// 创建数据库记录
	media := &models.MediaAttachment{
		AccountId:     account.Id,
		FileName:      fileName,
		FilePath:      filepath.Join(time.Now().Format("2006/01/02"), fileName),
		FileSize:      fileSize,
		ContentType:   contentType,
		Type:          mediaType,
		Width:         width,
		Height:        height,
		ThumbnailPath: thumbnailPath,
		Description:   description,
		FocusX:        focusX,
		FocusY:        focusY,
	}

	if err := db.GetDB().Create(media).Error; err != nil {
		err = errors.Wrap(err, "Failed to create media record")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})
		return
	}

	// 序列化响应
	response, err := serializer.ToMediaAttachment(media)
	if err != nil {
		err = errors.Wrap(err, "Failed to serialize media")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})
		return
	}

	c.JSON(200, response)
}

// isAllowedContentType 检查是否为允许的内容类型
func isAllowedContentType(contentType string) bool {
	allowedTypes := strings.Split(fmt.Sprintf("%s,%s,%s", AllowedImageTypes, AllowedVideoTypes, AllowedAudioTypes), ",")
	for _, t := range allowedTypes {
		if strings.TrimSpace(t) == contentType {
			return true
		}
	}
	return false
}

// isValidFileSize 检查文件大小是否有效
func isValidFileSize(size int64, contentType string) bool {
	mediaType := models.GetMediaType(contentType)
	switch mediaType {
	case models.MediaTypeImage:
		return size <= MaxImageSize
	case models.MediaTypeVideo, models.MediaTypeAudio:
		return size <= MaxVideoSize
	default:
		return false
	}
}

// createThumbnail 创建缩略图（简化版本，实际应使用图片处理库）
func createThumbnail(srcPath, dstPath string) error {
	// TODO: 实现缩略图生成
	// 可以使用 github.com/disintegration/imaging 或 github.com/nfnt/resize
	// 这里先创建一个占位实现
	return os.Symlink(srcPath, dstPath)
}

// getImageDimensions 获取图片尺寸（简化版本）
func getImageDimensions(path string) (int, int, error) {
	// TODO: 实现图片尺寸获取
	// 可以使用 github.com/disintegration/imaging 或其他图片库
	// 这里返回默认值
	return 1920, 1080, nil
}

// UploadAsync 异步上传媒体文件（返回 202 Accepted）
func UploadAsync(c *gin.Context) {
	// 获取当前用户
	account := c.MustGet("account").(*models.Account)

	// 解析请求
	var request struct {
		File        *multipart.FileHeader `form:"file" binding:"required"`
		Description string                `form:"description"`
		Focus       string                `form:"focus"`
	}

	if err := c.ShouldBind(&request); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request"})
		return
	}

	// 创建临时媒体记录（processing 状态）
	media := &models.MediaAttachment{
		AccountId:   account.Id,
		FileName:    "processing",
		FilePath:    "",
		FileSize:    0,
		ContentType: request.File.Header.Get("Content-Type"),
		Type:        models.MediaTypeUnknown,
		Description: request.Description,
	}

	if err := db.GetDB().Create(media).Error; err != nil {
		err = errors.Wrap(err, "Failed to create media record")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})
		return
	}

	// 异步处理文件上传
	go processUploadAsync(media.Id, account.Id, request.File, request.Description, request.Focus)

	// 返回 202 Accepted
	c.JSON(202, gin.H{
		"id":          strconv.FormatUint(media.Id, 10),
		"type":        "unknown",
		"url":         "",
		"preview_url": "",
		"description": request.Description,
		"blurhash":    "",
	})
}

// processUploadAsync 异步处理文件上传
func processUploadAsync(mediaId uint64, accountId uint64, fileHeader *multipart.FileHeader, description string, focus string) {
	// TODO: 实现异步文件处理
	// 1. 打开文件
	// 2. 保存文件
	// 3. 生成缩略图
	// 4. 获取元数据
	// 5. 更新数据库记录
	// 6. 发送完成通知
}
