package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/h2non/filetype"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/emojiasset"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/pkg/errors"
	"io"
	"mime"
	"mime/multipart"
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

	// Store the original (and thumbnail) through the storage backend; the
	// row keeps only the key below "media/", never a URL or host path.
	stored, err := storeMediaFile(c.Request.Context(), file, validatedContentType)
	if err != nil {
		err = errors.Wrap(err, "Failed to store media")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})

		return
	}

	mediaType := models.GetMediaType(validatedContentType)

	// 创建数据库记录
	mediaRecord := &models.MediaAttachment{
		AccountId:     account.Id,
		FileName:      stored.FileName,
		FilePath:      stored.FilePath,
		FileSize:      stored.Size,
		ContentType:   validatedContentType,
		Type:          mediaType,
		Width:         stored.Width,
		Height:        stored.Height,
		ThumbnailPath: stored.ThumbnailPath,
		Blurhash:      stored.Blurhash,
		Description:   description,
		FocusX:        focusX,
		FocusY:        focusY,
	}

	if err := db.GetDB().Create(mediaRecord).Error; err != nil {
		// 数据库写入失败，清理已保存的文件和缩略图
		stored.cleanup()
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	stored, err := storeMediaFile(ctx, bytes.NewReader(fileData), validatedContentType)
	if err != nil {
		log.Error("Failed to store media %d: %v", mediaId, err)
		updateMediaError(mediaId, nil)

		return
	}

	if err := db.GetDB().Model(&models.MediaAttachment{}).
		Where("id = ?", mediaId).
		Updates(map[string]any{
			"file_name":      stored.FileName,
			"file_path":      stored.FilePath,
			"file_size":      stored.Size,
			"type":           models.GetMediaType(validatedContentType),
			"width":          stored.Width,
			"height":         stored.Height,
			"thumbnail_path": stored.ThumbnailPath,
			"blurhash":       stored.Blurhash,
		}).Error; err != nil {
		log.Error("Failed to update media record %d: %v", mediaId, err)
		updateMediaError(mediaId, stored)

		return
	}
}

// updateMediaError deletes the failed media record and any stored objects.
func updateMediaError(mediaId uint64, stored *storedMedia) {
	if stored != nil {
		stored.cleanup()
	}

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
