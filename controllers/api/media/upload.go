package media

import (
	"crypto/rand"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/h2non/filetype"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/emojiasset"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/media"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/systemsettings"
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
	// MaxImageSize is the default image size limit (10 MB); the effective
	// limit is systemsettings.MediaImageSizeLimit().
	MaxImageSize = systemsettings.DefaultMediaImageSizeLimit
	// MaxVideoSize is the default video/audio size limit (40 MB); the
	// effective limit is systemsettings.MediaVideoSizeLimit().
	MaxVideoSize = systemsettings.DefaultMediaVideoSizeLimit
	// AllowedImageTypes 允许的图片类型.
	AllowedImageTypes = "image/jpeg,image/png,image/gif,image/webp"
	// AllowedVideoTypes 允许的视频类型.
	AllowedVideoTypes = "video/mp4,video/webm,video/quicktime"
	// AllowedAudioTypes 允许的音频类型.
	AllowedAudioTypes = "audio/mpeg,audio/ogg,audio/wav"
	// MaxAsyncUploads 限制并发异步上传处理数量，防止内存耗尽.
	MaxAsyncUploads = 32
)

// asyncUploadSem 限制并发异步上传处理的数量.
// 每个异步上传会将文件内容暂存在内存中，因此需要限制并发量.
var asyncUploadSem = make(chan struct{}, MaxAsyncUploads)

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

	// 计算时间戳（一次计算，统一使用，避免跨日期边界时路径不一致）
	now := time.Now()
	dateDir := now.Format("2006/01/02")

	// 创建媒体目录
	mediaDir := filepath.Join("uploads", "media", dateDir)
	if err := os.MkdirAll(mediaDir, 0755); err != nil { //nolint:gosec // G301: reviewed and safe
		err = errors.Wrap(err, "Failed to create media directory")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})

		return
	}

	// 生成文件名（从已验证的 Content-Type 推导扩展名，使用随机字节防冲突）
	ext := extensionForContentType(validatedContentType)
	fileName := generateUniqueFileName(now, ext)
	filePath := filepath.Join(mediaDir, fileName)

	// 创建目标文件
	dst, err := os.Create(filePath) //nolint:gosec // G304: reviewed and safe
	if err != nil {
		err = errors.Wrap(err, "Failed to create file")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})

		return
	}

	// 复制文件内容（流式处理，避免大文件占用内存）
	written, copyErr := io.Copy(dst, file)
	closeErr := dst.Close()

	if copyErr != nil {
		// 写入失败，清理部分写入的文件
		_ = os.Remove(filePath)
		err = errors.Wrap(copyErr, "Failed to save file")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})

		return
	}

	if closeErr != nil {
		// 关闭失败（可能数据未完全刷盘），清理文件
		_ = os.Remove(filePath)
		err = errors.Wrap(closeErr, "Failed to close file")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})

		return
	}

	fileSize := written

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
		} else {
			log.Warn("Failed to process image: %v", procErr)
		}
	}

	// 构造存储相对路径
	recordFilePath := filepath.Join(dateDir, fileName)

	// 上传到云存储（如果启用）
	cloudStorage := storage.GetStorage()
	if cloudStorage.IsEnabled() {
		if cloudPath, uploadErr := cloudStorage.Upload(filePath, "media/"+recordFilePath); uploadErr == nil {
			recordFilePath = cloudPath
		} else {
			log.Warn("Failed to upload to cloud storage: %v", uploadErr)
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
		// 数据库写入失败，清理已保存的文件和缩略图
		cleanupUploadedFiles(mediaDir, fileName, thumbnailPath)
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

// normalizeContentType 规范化 Content-Type（去除参数、小写化）.
func normalizeContentType(ct string) string {
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		ct = ct[:idx]
	}

	return strings.ToLower(strings.TrimSpace(ct))
}

// normalizeDetectedMIME 规范化 filetype 库检测到的 MIME 类型.
func normalizeDetectedMIME(mime string) string {
	mime = strings.ToLower(strings.TrimSpace(mime))
	// filetype 库将 WAV 检测为 audio/x-wav，统一为 audio/wav
	if mime == "audio/x-wav" {
		return "audio/wav"
	}

	return mime
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
		return size <= systemsettings.MediaImageSizeLimit()
	case models.MediaTypeVideo, models.MediaTypeAudio:
		return size <= systemsettings.MediaVideoSizeLimit()
	default:
		return false
	}
}

// validateFileUpload 验证文件上传：检查 Content-Type、文件大小和 magic bytes.
func validateFileUpload(header *multipart.FileHeader) (string, error) {
	contentType := normalizeContentType(header.Header.Get("Content-Type"))

	// 检查 Content-Type 是否允许
	if !isAllowedContentType(contentType) {
		return "", errors.New("unsupported media type")
	}

	// 检查文件大小
	if !isValidFileSize(header.Size, contentType) {
		return "", errors.New("file size too large")
	}

	// Magic bytes 验证（针对所有媒体类型）
	if err := validateMagicBytes(header, contentType); err != nil {
		return "", err
	}

	if models.GetMediaType(contentType) == models.MediaTypeImage {
		file, err := header.Open()
		if err != nil {
			return "", errors.Wrap(err, "open image")
		}
		data, err := io.ReadAll(io.LimitReader(file, systemsettings.MediaImageSizeLimit()+1))
		_ = file.Close()
		if err != nil {
			return "", errors.Wrap(err, "read image")
		}
		if int64(len(data)) > systemsettings.MediaImageSizeLimit() {
			return "", errors.New("file size too large")
		}
		pixels := systemsettings.GetInt(systemsettings.KeyMediaImageMatrixLimit, 16*1024*1024)
		if pixels <= 0 {
			pixels = 16 * 1024 * 1024
		}
		// Independent work/memory limits keep animated images bounded while allowing
		// normal post images larger than the emoji endpoint's 1024px cap.
		pixels = min(pixels, 32*1024*1024)
		_, _, _, _, err = emojiasset.DecodeImage(data, contentType, emojiasset.DecodeLimits{MaxDimension: 16384, MaxPixels: pixels, MaxFrames: 256, MaxAnimationPixels: 64 * 1024 * 1024})
		if err != nil {
			return "", errors.Wrap(err, "invalid image")
		}
	}
	return contentType, nil
}

// validateMagicBytes 通过 magic bytes 验证文件内容与声明的 Content-Type 一致.
func validateMagicBytes(header *multipart.FileHeader, declaredContentType string) error {
	file, err := header.Open()
	if err != nil {
		return errors.Wrap(err, "failed to open file for validation")
	}
	defer func() { _ = file.Close() }()

	buf := make([]byte, 8192)
	n, err := io.ReadFull(file, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return errors.Wrap(err, "failed to read file for validation")
	}

	if n == 0 {
		return errors.New("file content does not match declared content type")
	}

	kind, _ := filetype.Match(buf[:n])
	if kind == filetype.Unknown || kind.MIME.Value == "" {
		return errors.New("file content does not match declared content type")
	}

	detectedMIME := normalizeDetectedMIME(kind.MIME.Value)
	if detectedMIME != declaredContentType {
		return errors.New("file content does not match declared content type")
	}

	return nil
}

// extensionForContentType 从 Content-Type 推导文件扩展名.
// preferredExtensions pins the extension for the media types we accept.
// mime.ExtensionsByType returns extensions sorted alphabetically and depends
// on the host's /etc/mime.types, so e.g. on Debian video/mp4 became ".f4v",
// image/jpeg ".jfif", video/quicktime ".moov" and video/webm ".bin" (no
// entry), which then got served with the wrong Content-Type.
var preferredExtensions = map[string]string{
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
	"image/gif":       ".gif",
	"image/webp":      ".webp",
	"image/avif":      ".avif",
	"image/heic":      ".heic",
	"image/heif":      ".heif",
	"video/mp4":       ".mp4",
	"video/webm":      ".webm",
	"video/quicktime": ".mov",
	"video/ogg":       ".ogv",
	"audio/mpeg":      ".mp3",
	"audio/mp3":       ".mp3",
	"audio/ogg":       ".ogg",
	"audio/wav":       ".wav",
	"audio/x-wav":     ".wav",
	"audio/webm":      ".weba",
	"audio/flac":      ".flac",
	"audio/aac":       ".aac",
	"audio/mp4":       ".m4a",
	"audio/x-m4a":     ".m4a",
}

func extensionForContentType(contentType string) string {
	if ext, ok := preferredExtensions[strings.ToLower(contentType)]; ok {
		return ext
	}

	exts, _ := mime.ExtensionsByType(contentType)
	ext := ".bin"
	if len(exts) > 0 {
		ext = exts[0]
	}

	return ext
}

// generateUniqueFileName 生成防冲突的文件名（时间戳 + 随机字节）.
func generateUniqueFileName(now time.Time, ext string) string {
	var b [8]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read 实际上不会失败

	return fmt.Sprintf("%d_%x%s", now.UnixNano(), b[:], ext)
}

// cleanupUploadedFiles 删除已上传的文件及其缩略图.
func cleanupUploadedFiles(mediaDir, fileName string, thumbnailPath *string) {
	if fileName != "" {
		filePath := filepath.Join(mediaDir, fileName)
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			log.Warn("Failed to cleanup uploaded file %s: %v", filePath, err)
		}
	}

	if thumbnailPath != nil && *thumbnailPath != "" {
		thumbPath := filepath.Join(mediaDir, *thumbnailPath)
		if err := os.Remove(thumbPath); err != nil && !os.IsNotExist(err) {
			log.Warn("Failed to cleanup thumbnail %s: %v", thumbPath, err)
		}
	}
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

	// 尝试获取并发处理槽位，防止内存耗尽（DoS 防护）
	select {
	case asyncUploadSem <- struct{}{}:
		// 获取到槽位，继续处理
	default:
		c.JSON(503, gin.H{"error": "Server is busy processing uploads, please retry later"})

		return
	}

	// Read file content into memory now, since multipart data may be cleaned up
	// after the request handler returns.
	fileData, err := readAllFromFileHeader(request.File)
	if err != nil {
		<-asyncUploadSem // 释放槽位
		_ = c.Error(errors.Wrap(err, "Failed to read file"))
		c.JSON(500, gin.H{"error": "Internal server error"})

		return
	}

	// 解析焦点位置（可选）
	var focusX, focusY float64

	if request.Focus != "" {
		parts := strings.Split(request.Focus, ",")
		if len(parts) == 2 {
			if x, errX := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64); errX == nil {
				focusX = x
			}

			if y, errY := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); errY == nil {
				focusY = y
			}
		}
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
		FocusX:      focusX,
		FocusY:      focusY,
	}

	if err := db.GetDB().Create(mediaRecord).Error; err != nil {
		<-asyncUploadSem // 释放槽位
		err = errors.Wrap(err, "Failed to create media record")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})

		return
	}

	// 异步处理文件上传
	go func() {
		defer func() { <-asyncUploadSem }()
		processUploadAsync(mediaRecord.Id, fileData, validatedContentType)
	}()

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
func processUploadAsync(mediaId uint64, fileData []byte, validatedContentType string) {
	// 1. 保存文件
	now := time.Now()
	dateDir := now.Format("2006/01/02")
	mediaDir := filepath.Join("uploads", "media", dateDir)

	if err := os.MkdirAll(mediaDir, 0755); err != nil { //nolint:gosec // G301: reviewed and safe
		log.Error("Failed to create directory for media %d: %v", mediaId, err)
		updateMediaError(mediaId, "", "", nil)

		return
	}

	// 从已验证的 Content-Type 推导扩展名
	ext := extensionForContentType(validatedContentType)
	fileName := generateUniqueFileName(now, ext)
	filePath := filepath.Join(mediaDir, fileName)

	dst, err := os.Create(filePath) //nolint:gosec // G304: reviewed and safe
	if err != nil {
		log.Error("Failed to create file for media %d: %v", mediaId, err)
		updateMediaError(mediaId, mediaDir, "", nil)

		return
	}

	written, writeErr := dst.Write(fileData)
	closeErr := dst.Close()

	if writeErr != nil {
		log.Error("Failed to save file for media %d: %v", mediaId, writeErr)
		_ = os.Remove(filePath)
		updateMediaError(mediaId, mediaDir, fileName, nil)

		return
	}

	if closeErr != nil {
		log.Error("Failed to close file for media %d: %v", mediaId, closeErr)
		_ = os.Remove(filePath)
		updateMediaError(mediaId, mediaDir, fileName, nil)

		return
	}

	fileSize := int64(written)

	// 2. 处理图片（缩略图、BlurHash、尺寸）
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
		} else {
			log.Warn("Failed to process image for media %d: %v", mediaId, procErr)
		}
	}

	// 3. 更新数据库记录
	recordFilePath := filepath.Join(dateDir, fileName)

	if err := db.GetDB().Model(&models.MediaAttachment{}).
		Where("id = ?", mediaId).
		Updates(map[string]any{
			"file_name":      fileName,
			"file_path":      recordFilePath,
			"file_size":      fileSize,
			"type":           mediaType,
			"width":          width,
			"height":         height,
			"thumbnail_path": thumbnailPath,
			"blurhash":       blurhash,
		}).Error; err != nil {
		log.Error("Failed to update media record %d: %v", mediaId, err)
		updateMediaError(mediaId, mediaDir, fileName, thumbnailPath)

		return
	}

	// 4. 上传到云存储（如果启用）
	cloudStorage := storage.GetStorage()
	if cloudStorage.IsEnabled() {
		if _, uploadErr := cloudStorage.Upload(filePath, "media/"+recordFilePath); uploadErr != nil {
			log.Warn("Failed to upload media %d to cloud storage: %v", mediaId, uploadErr)
		}
	}
}

// updateMediaError 更新媒体记录为错误状态并清理相关文件.
func updateMediaError(mediaId uint64, mediaDir, fileName string, thumbnailPath *string) {
	// 清理物理文件
	if mediaDir != "" {
		cleanupUploadedFiles(mediaDir, fileName, thumbnailPath)
	}

	// 删除失败的媒体记录
	if err := db.GetDB().Delete(&models.MediaAttachment{}, mediaId).Error; err != nil {
		log.Error("Failed to delete failed media record %d: %v", mediaId, err)
	}
}

// readAllFromFileHeader reads all content from a multipart file header.
func readAllFromFileHeader(header *multipart.FileHeader) ([]byte, error) {
	file, err := header.Open()
	if err != nil {
		return nil, errors.Wrap(err, "failed to open file header")
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read file content")
	}

	return data, nil
}

// SupportedMimeTypes is shared by upload validation and instance discovery.
func SupportedMimeTypes() []string {
	return strings.Split(AllowedImageTypes+","+AllowedVideoTypes+","+AllowedAudioTypes, ",")
}
