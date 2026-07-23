package media

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/h2non/filetype"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/media"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/storage"
	"github.com/pkg/errors"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxImageSize 图片最大大小 10MB.
	MaxImageSize = 10 * 1024 * 1024
	// MaxVideoSize 视频/音频最大大小 40MB.
	MaxVideoSize = 40 * 1024 * 1024
	// AllowedImageTypes 允许的图片类型.
	AllowedImageTypes = "image/jpeg,image/png,image/gif,image/webp"
	// AllowedVideoTypes 允许的视频类型.
	AllowedVideoTypes = "video/mp4,video/webm,video/quicktime"
	// AllowedAudioTypes 允许的音频类型.
	AllowedAudioTypes = "audio/mpeg,audio/ogg,audio/wav"
)

// Upload 上传媒体文件.
func Upload(c *gin.Context) {
	// 获取当前用户
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// 获取上传的文件
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(400, gin.H{"error": "No file provided"}) //nolint:goconst

		return
	}
	defer func() { _ = file.Close() }()

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

	// 验证文件上传（类型、大小、magic bytes）
	validatedContentType, err := validateFileUpload(header)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})

		return
	}

	// 创建媒体目录
	mediaDir := filepath.Join("uploads", "media", time.Now().Format("2006/01/02"))
	if err := os.MkdirAll(mediaDir, 0755); err != nil { //nolint:gosec // G301: reviewed and safe
		err = errors.Wrap(err, "Failed to create media directory")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})

		return
	}

	// 生成文件名（从已验证的 Content-Type 推导扩展名）
	exts, _ := mime.ExtensionsByType(validatedContentType)

	ext := ".bin"
	if len(exts) > 0 {
		ext = exts[0]
	}

	fileName := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	filePath := filepath.Join(mediaDir, fileName)

	// 创建目标文件
	dst, err := os.Create(filePath) //nolint:gosec // G304: reviewed and safe
	if err != nil {
		err = errors.Wrap(err, "Failed to create file")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})

		return
	}
	defer func() { _ = dst.Close() }()

	// 复制文件内容
	if _, err := io.Copy(dst, file); err != nil {
		err = errors.Wrap(err, "Failed to save file")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})

		return
	}

	// 获取文件大小
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		err = errors.Wrap(err, "Failed to get file info")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})

		return
	}

	fileSize := fileInfo.Size()

	// 创建缩略图、获取尺寸和生成 BlurHash（如果是图片）
	var (
		thumbnailPath *string
		blurhash      string
		width, height *int
	)

	mediaType := models.GetMediaType(validatedContentType)

	if mediaType == models.MediaTypeImage {
		processor := media.NewImageProcessor()

		thumbName, bh, w, h, procErr := processor.ProcessImage(filePath)
		if procErr == nil {
			thumbnailPath = &thumbName
			blurhash = bh
			width = &w
			height = &h
		}
	}

	// 上传到云存储（如果启用）
	recordFilePath := filepath.Join(time.Now().Format("2006/01/02"), fileName)

	cloudStorage := storage.GetStorage()
	if cloudStorage.IsEnabled() {
		if cloudPath, uploadErr := cloudStorage.Upload(filePath, "media/"+recordFilePath); uploadErr == nil {
			recordFilePath = cloudPath
		}
	}

	// 创建数据库记录
	mediaRecord := &models.MediaAttachment{
		AccountId:     account.Id,
		FileName:      fileName,
		FilePath:      recordFilePath,
		FileSize:      fileSize,
		ContentType:   validatedContentType,
		Type:          mediaType,
		Width:         width,
		Height:        height,
		ThumbnailPath: thumbnailPath,
		Blurhash:      blurhash,
		Description:   description,
		FocusX:        focusX,
		FocusY:        focusY,
	}

	if err := db.GetDB().Create(mediaRecord).Error; err != nil {
		err = errors.Wrap(err, "Failed to create media record")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})

		return
	}

	// 序列化响应
	response, err := serializer.ToMediaAttachment(mediaRecord)
	if err != nil {
		err = errors.Wrap(err, "Failed to serialize media")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})

		return
	}

	c.JSON(200, response)
}

// isAllowedContentType 检查是否为允许的内容类型.
func isAllowedContentType(contentType string) bool {
	allowedTypes := strings.SplitSeq(fmt.Sprintf("%s,%s,%s", AllowedImageTypes, AllowedVideoTypes, AllowedAudioTypes), ",")
	for t := range allowedTypes {
		if strings.TrimSpace(t) == contentType {
			return true
		}
	}

	return false
}

// isValidFileSize 检查文件大小是否有效.
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

// validateFileUpload 验证文件上传：检查 Content-Type、文件大小和 magic bytes.
func validateFileUpload(header *multipart.FileHeader) (string, error) {
	contentType := header.Header.Get("Content-Type")

	// 检查 Content-Type 是否允许
	if !isAllowedContentType(contentType) {
		return "", errors.New("unsupported media type")
	}

	// 检查文件大小
	if !isValidFileSize(header.Size, contentType) {
		return "", errors.New("file size too large")
	}

	// Magic bytes 验证（针对 image/* 类型）
	if strings.HasPrefix(contentType, "image/") {
		file, err := header.Open()
		if err != nil {
			return "", errors.Wrap(err, "failed to open file for validation")
		}
		defer func() { _ = file.Close() }()

		buf := make([]byte, 512)

		n, err := file.Read(buf)
		if err != nil && !errors.Is(err, io.EOF) {
			return "", errors.Wrap(err, "failed to read file for validation")
		}

		kind, _ := filetype.Match(buf[:n])
		detectedMIME := kind.MIME.Value

		// 如果声明的 Content-Type 是 image/*，但 magic bytes 不匹配，拒绝上传
		if detectedMIME == "" || !strings.HasPrefix(detectedMIME, "image/") {
			return "", errors.New("file content does not match declared content type")
		}
	}

	return contentType, nil
}

// UploadAsync 异步上传媒体文件（返回 202 Accepted）.
func UploadAsync(c *gin.Context) {
	// 获取当前用户
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// 解析请求
	var request struct {
		File        *multipart.FileHeader `binding:"required" form:"file"`
		Description string                `form:"description"`
		Focus       string                `form:"focus"`
	}

	if err := c.ShouldBind(&request); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request"})

		return
	}

	// 验证文件上传（类型、大小、magic bytes）
	validatedContentType, err := validateFileUpload(request.File)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})

		return
	}

	// 创建临时媒体记录（processing 状态）
	mediaRecord := &models.MediaAttachment{
		AccountId:   account.Id,
		FileName:    "processing",
		FilePath:    "",
		FileSize:    0,
		ContentType: validatedContentType,
		Type:        models.MediaTypeUnknown,
		Description: request.Description,
	}

	if err := db.GetDB().Create(mediaRecord).Error; err != nil {
		err = errors.Wrap(err, "Failed to create media record")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})

		return
	}

	// 异步处理文件上传
	go processUploadAsync(mediaRecord.Id, account.Id, request.File, request.Description, request.Focus, validatedContentType)

	// 返回 202 Accepted
	c.JSON(202, gin.H{
		"id":          strconv.FormatUint(mediaRecord.Id, 10),
		"type":        "unknown",
		"url":         "",
		"preview_url": "",
		"description": request.Description,
		"blurhash":    "",
	})
}

// processUploadAsync 异步处理文件上传.
func processUploadAsync(mediaId uint64, accountId uint64, fileHeader *multipart.FileHeader, description string, focus string, validatedContentType string) { //nolint:unparam // param reserved for future use
	// 1. 打开文件
	file, err := fileHeader.Open()
	if err != nil {
		updateMediaError(mediaId, "Failed to open file")

		return
	}
	defer func() { _ = file.Close() }()

	// 2. 保存文件
	mediaDir := filepath.Join("uploads", "media", time.Now().Format("2006/01/02"))
	if err := os.MkdirAll(mediaDir, 0755); err != nil { //nolint:gosec // G301: reviewed and safe
		updateMediaError(mediaId, "Failed to create directory")

		return
	}

	// 从已验证的 Content-Type 推导扩展名
	exts, _ := mime.ExtensionsByType(validatedContentType)

	ext := ".bin"
	if len(exts) > 0 {
		ext = exts[0]
	}

	fileName := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	filePath := filepath.Join(mediaDir, fileName)

	dst, err := os.Create(filePath) //nolint:gosec // G304: reviewed and safe
	if err != nil {
		updateMediaError(mediaId, "Failed to create file")

		return
	}
	defer func() { _ = dst.Close() }()

	if _, err := io.Copy(dst, file); err != nil {
		updateMediaError(mediaId, "Failed to save file")

		return
	}

	// 3. 处理图片（缩略图、BlurHash、尺寸）
	var (
		thumbnailPath *string
		blurhash      string
		width, height *int
	)

	mediaType := models.GetMediaType(validatedContentType)

	if mediaType == models.MediaTypeImage {
		processor := media.NewImageProcessor()

		thumbName, bh, w, h, procErr := processor.ProcessImage(filePath)
		if procErr == nil {
			thumbnailPath = &thumbName
			blurhash = bh
			width = &w
			height = &h
		}
	}

	// 4. 获取文件大小
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		updateMediaError(mediaId, "Failed to get file info")

		return
	}

	fileSize := fileInfo.Size()

	// 5. 更新数据库记录
	if err := db.GetDB().Model(&models.MediaAttachment{}).
		Where("id = ?", mediaId).
		Updates(map[string]any{
			"file_name":      fileName,
			"file_path":      filepath.Join(time.Now().Format("2006/01/02"), fileName),
			"file_size":      fileSize,
			"type":           mediaType,
			"width":          width,
			"height":         height,
			"thumbnail_path": thumbnailPath,
			"blurhash":       blurhash,
			"description":    description,
		}).Error; err != nil {
		updateMediaError(mediaId, "Failed to update media record")

		return
	}

	// 6. 上传到云存储（如果启用）
	cloudStorage := storage.GetStorage()
	if cloudStorage.IsEnabled() {
		_, _ = cloudStorage.Upload(filePath, "media/"+filepath.Join(time.Now().Format("2006/01/02"), fileName))
	}
}

// updateMediaError 更新媒体记录为错误状态.
func updateMediaError(mediaId uint64, errorMsg string) { //nolint:unparam // param reserved for future use
	// 删除失败的媒体记录
	db.GetDB().Delete(&models.MediaAttachment{}, mediaId)
}
