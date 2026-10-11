package homeserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kasuganosora/community/defaultavatar"
	imagelib "image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/httpclient"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/media"
	"github.com/kasuganosora/community/storage"
)

// MatrixMedia Matrix 媒体记录.
type MatrixMedia struct { //nolint:recvcheck
	ID          uint64    `gorm:"primaryKey"                   json:"-"`
	MediaID     string    `gorm:"uniqueIndex;size:64;not null" json:"media_id"`
	ServerName  string    `gorm:"index;size:255;not null"      json:"server_name"`
	FileName    string    `gorm:"size:255"                     json:"file_name"`
	ContentType string    `gorm:"size:100"                     json:"content_type"`
	FileSize    int64     `json:"file_size"`
	Width       *int      `json:"width,omitempty"`
	Height      *int      `json:"height,omitempty"`
	FilePath    string    `gorm:"size:500"                     json:"-"`
	UploaderID  string    `gorm:"size:255"                     json:"uploader_id"`
	CreatedAt   time.Time `json:"created_at"`
}

// TableName 表名.
func (MatrixMedia) TableName() string {
	return "matrix_media"
}

// MatrixThumbnail Matrix 缩略图.
type MatrixThumbnail struct {
	ID           uint64    `gorm:"primaryKey"             json:"-"`
	MediaID      string    `gorm:"index;size:64;not null" json:"media_id"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	ResizeMethod string    `gorm:"size:20;default:'crop'" json:"resize_method"` // crop 或 scale
	FilePath     string    `gorm:"size:500"               json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// TableName 表名.
func (MatrixThumbnail) TableName() string {
	return "matrix_media_thumbnails"
}

// MediaService 媒体服务.
type MediaService struct {
	hs *HomeServer
}

// NewMediaService 创建媒体服务.
func NewMediaService(hs *HomeServer) *MediaService {
	return &MediaService{hs: hs}
}

// UploadMedia 上传媒体文件.
func (s *MediaService) UploadMedia(file io.Reader, header *multipart.FileHeader, contentType, uploaderID string) (*MatrixMedia, error) {
	// 生成 Media ID (MXC URI 格式的本地部分)
	mediaID := generateMediaID()

	// 获取文件扩展名
	ext := ""
	if header != nil {
		ext = filepath.Ext(header.Filename)
	} else {
		// 从 Content-Type 推断扩展名
		exts, _ := mime.ExtensionsByType(contentType)
		if len(exts) > 0 {
			ext = exts[0]
		}
	}

	// Stage the bytes in a private temp dir (the thumbnailer needs a file),
	// then write original and thumbnail through the storage backend. Rows
	// keep storage keys ("matrix/<date>/<file>"), never host paths or URLs.
	fileName := mediaID + ext
	key := "matrix/" + time.Now().Format("2006/01/02") + "/" + fileName

	tmpDir, err := os.MkdirTemp("", "community-matrix-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tmpPath := filepath.Join(tmpDir, fileName)

	dst, err := os.Create(tmpPath) //nolint:gosec // G304: private temp dir
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	fileSize, err := io.Copy(dst, file)
	closeErr := dst.Close()

	if err == nil {
		err = closeErr
	}

	if err != nil {
		return nil, fmt.Errorf("failed to save file: %w", err)
	}

	var (
		width, height *int
		thumbnailPath string
	)

	if strings.HasPrefix(contentType, "image/") {
		processor := media.NewImageProcessor()

		thumbName, w, h, err := processor.ProcessImageThumbnail(tmpPath)
		if err == nil {
			width = &w
			height = &h

			if thumbName != "" {
				thumbnailPath = path.Join(path.Dir(key), thumbName)
				if err := storage.PutFile(context.Background(), filepath.Join(tmpDir, thumbName), thumbnailPath, ""); err != nil {
					log.Warn("Failed to store matrix thumbnail: %v", err)
					thumbnailPath = ""
				}
			}
		}
	}

	if err := storage.PutFile(context.Background(), tmpPath, key, contentType); err != nil {
		if thumbnailPath != "" {
			_ = storage.Delete(thumbnailPath)
		}

		return nil, fmt.Errorf("failed to store file: %w", err)
	}

	// 创建数据库记录
	matrixMedia := &MatrixMedia{
		MediaID:     mediaID,
		ServerName:  s.hs.Config.ServerName,
		FileName:    originalMediaFilename(header, fileName),
		ContentType: contentType,
		FileSize:    fileSize,
		Width:       width,
		Height:      height,
		FilePath:    key,
		UploaderID:  uploaderID,
		CreatedAt:   time.Now(),
	}

	if err := db.GetDB().Create(matrixMedia).Error; err != nil {
		_ = storage.Delete(key)
		if thumbnailPath != "" {
			_ = storage.Delete(thumbnailPath)
		}

		return nil, fmt.Errorf("failed to create media record: %w", err)
	}

	// 创建缩略图记录
	if thumbnailPath != "" {
		thumbnail := &MatrixThumbnail{
			MediaID:      mediaID,
			Width:        320,
			Height:       240,
			ResizeMethod: "crop",
			FilePath:     thumbnailPath,
			CreatedAt:    time.Now(),
		}
		err := db.GetDB().Create(thumbnail).Error
		if err != nil {
			log.Error("Failed to create thumbnail: %v", err)
		}
	}

	return matrixMedia, nil
}

// GetMedia 获取媒体文件.
func (s *MediaService) GetMedia(serverName, mediaID string) (*MatrixMedia, io.ReadCloser, error) {
	if serverName == s.hs.Config.ServerName && strings.HasPrefix(mediaID, defaultavatar.MediaPrefix) {
		data, err := defaultavatar.PNG(strings.TrimPrefix(mediaID, defaultavatar.MediaPrefix))
		if err != nil {
			return nil, nil, err
		}
		size := defaultavatar.Size
		return &MatrixMedia{MediaID: mediaID, ServerName: serverName, FileName: "avatar.png", ContentType: "image/png", FileSize: int64(len(data)), Width: &size, Height: &size}, io.NopCloser(bytes.NewReader(data)), nil
	}

	var matrixMedia MatrixMedia
	if err := db.GetDB().Where("media_id = ? AND server_name = ?", mediaID, serverName).First(&matrixMedia).Error; err != nil {
		return nil, nil, errors.New("media not found")
	}

	reader, err := openMatrixFile(matrixMedia.FilePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open file: %w", err)
	}

	return &matrixMedia, reader, nil
}

// openMatrixFile reads a stored matrix media reference through the storage
// backend. Absolute host paths (written by very old releases and tests) are
// still read from disk.
func openMatrixFile(ref string) (io.ReadCloser, error) {
	if filepath.IsAbs(ref) {
		return os.Open(ref) //nolint:gosec // G304: legacy DB-provided path
	}

	key, _ := storage.NormalizeKey(ref)
	rc, _, err := storage.Default().Open(context.Background(), key)

	return rc, err
}

// deleteMatrixFile removes a stored matrix media reference.
func deleteMatrixFile(ref string) {
	if ref == "" {
		return
	}

	if filepath.IsAbs(ref) {
		_ = os.Remove(ref)

		return
	}

	key, _ := storage.NormalizeKey(ref)
	if err := storage.Delete(key); err != nil {
		log.Warn("Failed to delete matrix media %s: %v", key, err)
	}
}

// GetThumbnail 获取缩略图.
func (s *MediaService) GetThumbnail(serverName, mediaID string, width, height int, method string) (*MatrixThumbnail, io.ReadCloser, error) {
	// Thumbnail IDs are local storage identities, never remote MXC identities.
	if serverName != s.hs.Config.ServerName {
		return nil, nil, errors.New("remote thumbnail requires federation")
	}
	if strings.HasPrefix(mediaID, defaultavatar.MediaPrefix) {
		media, reader, err := s.GetMedia(serverName, mediaID)
		if err != nil {
			return nil, nil, err
		}
		return &MatrixThumbnail{MediaID: mediaID, Width: *media.Width, Height: *media.Height}, reader, nil
	}
	var thumbnail MatrixThumbnail

	// Try exact match first, then any available thumbnail for this media
	err := db.GetDB().Where("media_id = ?", mediaID).
		Order("width DESC").First(&thumbnail).Error
	if err != nil {
		// No thumbnail at all - try returning the original media as thumbnail
		media, reader, err2 := s.GetMedia(serverName, mediaID)
		if err2 != nil {
			return nil, nil, errors.New("thumbnail not found")
		}
		// Create a synthetic thumbnail from the original
		synth := &MatrixThumbnail{
			MediaID:  media.MediaID,
			FilePath: media.FilePath,
		}
		if media.Width != nil {
			synth.Width = *media.Width
		}

		if media.Height != nil {
			synth.Height = *media.Height
		}

		return synth, reader, nil
	}

	// Older records stored only the processor's returned basename.
	thumbPath := thumbnail.FilePath
	if !strings.Contains(thumbPath, "/") && !strings.Contains(thumbPath, string(filepath.Separator)) {
		var original MatrixMedia
		if err := db.GetDB().Where("media_id = ? AND server_name = ?", mediaID, serverName).First(&original).Error; err != nil {
			return nil, nil, errors.New("thumbnail media not found")
		}
		if filepath.IsAbs(original.FilePath) {
			thumbPath = filepath.Join(filepath.Dir(original.FilePath), thumbPath)
		} else {
			key, _ := storage.NormalizeKey(original.FilePath)
			thumbPath = path.Join(path.Dir(key), thumbPath)
		}
	}
	file, err := openMatrixFile(thumbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open thumbnail: %w", err)
	}

	return &thumbnail, file, nil
}

// GetMediaByMXC 通过 MXC URI 获取媒体.
func (s *MediaService) GetMediaByMXC(mxcURI string) (*MatrixMedia, io.ReadCloser, error) {
	// 解析 mxc://server_name/media_id
	if !strings.HasPrefix(mxcURI, "mxc://") {
		return nil, nil, errors.New("invalid MXC URI")
	}

	parts := strings.Split(strings.TrimPrefix(mxcURI, "mxc://"), "/")
	if len(parts) != 2 {
		return nil, nil, errors.New("invalid MXC URI format")
	}

	return s.GetMedia(parts[0], parts[1])
}

// DeleteMedia 删除媒体.
func (s *MediaService) DeleteMedia(serverName, mediaID string) error {
	var matrixMedia MatrixMedia
	err := db.GetDB().Where("media_id = ? AND server_name = ?", mediaID, serverName).First(&matrixMedia).Error
	if err != nil {
		return errors.New("media not found")
	}

	var thumbs []MatrixThumbnail
	db.GetDB().Where("media_id = ?", mediaID).Find(&thumbs)

	// 删除缩略图
	db.GetDB().Where("media_id = ?", mediaID).Delete(&MatrixThumbnail{})

	// 删除媒体记录
	err = db.GetDB().Delete(&matrixMedia).Error
	if err != nil {
		return err
	}

	// Delete the stored objects (local or S3).
	deleteMatrixFile(matrixMedia.FilePath)

	for _, t := range thumbs {
		if t.FilePath != "" && t.FilePath != matrixMedia.FilePath && strings.Contains(t.FilePath, "/") {
			deleteMatrixFile(t.FilePath)
		}
	}

	return nil
}

// generateMediaID 生成媒体 ID.
func generateMediaID() string {
	// 使用 UUID + 时间戳生成唯一 ID
	data := fmt.Sprintf("%s%d", uuid.New().String(), time.Now().UnixNano())
	hash := sha256.Sum256([]byte(data))

	return base64.RawURLEncoding.EncodeToString(hash[:16])
}

// ===== HTTP Handlers =====

// handleUploadMedia 处理媒体上传.
func handleUploadMedia(c *gin.Context) {
	hs := GetHomeServer()
	userID := getUserID(c)

	// Matrix spec: media upload uses raw request body
	// Content-Type header specifies media type, filename is query param
	contentType := c.GetHeader("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	// Strip parameters (e.g. "image/png; charset=utf-8" -> "image/png")
	if idx := strings.Index(contentType, ";"); idx >= 0 {
		contentType = strings.TrimSpace(contentType[:idx])
	}

	filename := c.Query("filename")

	// Build a synthetic FileHeader if filename provided
	var fakeHeader *multipart.FileHeader
	if filename != "" {
		fakeHeader = &multipart.FileHeader{Filename: filename}
	}

	mediaService := NewMediaService(hs)

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, matrixMediaLimit)
	matrixMedia, err := mediaService.UploadMedia(c.Request.Body, fakeHeader, contentType, userID)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.JSON(413, gin.H{"errcode": "M_TOO_LARGE", "error": "Media exceeds 50 MiB"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_UNKNOWN", //nolint:goconst
			"error":   err.Error(), //nolint:goconst
		})

		return
	}

	// 返回 MXC URI
	c.JSON(http.StatusOK, gin.H{
		"content_uri": fmt.Sprintf("mxc://%s/%s", hs.Config.ServerName, matrixMedia.MediaID),
	})
}

// handleDownloadMedia 处理媒体下载.
func handleDownloadMedia(c *gin.Context) {
	hs := GetHomeServer()
	serverName := c.Param("serverName")
	mediaID := c.Param("mediaId")

	// 如果是远程服务器的媒体，需要通过联邦获取
	if serverName != hs.Config.ServerName {
		handleRemoteMediaDownload(c, serverName, mediaID)

		return
	}

	// 获取本地媒体
	mediaService := NewMediaService(hs)

	matrixMedia, reader, err := mediaService.GetMedia(serverName, mediaID)
	if err != nil {
		// Check if this is a pending async upload
		var asyncMedia AsyncMediaInfo
		if db.GetDB().Where("media_id = ? AND status = ?", mediaID, "pending").First(&asyncMedia).Error == nil {
			c.JSON(http.StatusGatewayTimeout, gin.H{
				"errcode": "M_NOT_YET_UPLOADED",
				"error":   "Media has not been uploaded yet",
			})

			return
		}

		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND", //nolint:goconst
			"error":   "Media not found",
		})

		return
	}

	defer func() { _ = reader.Close() }()

	// 设置响应头
	c.Header("Content-Type", matrixMedia.ContentType)
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": matrixMedia.FileName}))
	c.Header("Content-Length", strconv.FormatInt(matrixMedia.FileSize, 10))

	// 流式传输文件
	if _, err := io.Copy(c.Writer, reader); err != nil {
		log.Error("Failed to stream media: %v", err)
	}
}

// handleRemoteMediaDownload 处理远程媒体下载.
func handleRemoteMediaDownload(c *gin.Context, serverName, mediaID string) {
	hs := GetHomeServer()

	media, err := hs.mediaFederation().fetchAuthenticatedMediaContext(c.Request.Context(), serverName, mediaID, "download", "")
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"errcode": "M_NOT_FOUND", "error": "Remote media not available"})
		return
	}
	c.Header("Content-Type", media.ContentType)
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": media.Filename}))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, media.ContentType, media.Data)
}

// handleThumbnailMedia 处理缩略图请求.
func handleThumbnailMedia(c *gin.Context) {
	hs := GetHomeServer()
	serverName := c.Param("serverName")
	mediaID := c.Param("mediaId")

	// 解析参数
	width := 0
	height := 0
	method := "crop"

	if w := c.Query("width"); w != "" {
		if _, err := fmt.Sscanf(w, "%d", &width); err != nil {
			width = 0
		}
	}

	if h := c.Query("height"); h != "" {
		if _, err := fmt.Sscanf(h, "%d", &height); err != nil {
			height = 0
		}
	}

	if m := c.Query("method"); m != "" {
		method = m
	}

	// 默认尺寸
	if width == 0 {
		width = 320
	}

	if height == 0 {
		height = 240
	}

	// 如果是远程服务器
	if serverName != hs.Config.ServerName {
		// 通过联邦获取远程缩略图
		fedClient := hs.mediaFederation()

		params := url.Values{"width": {strconv.Itoa(width)}, "height": {strconv.Itoa(height)}, "method": {method}}
		remote, err := fedClient.fetchAuthenticatedMediaContext(c.Request.Context(), serverName, mediaID, "thumbnail", "?"+params.Encode())
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{
				"errcode": "M_NOT_FOUND",
				"error":   "Remote thumbnail not available",
			})

			return
		}

		data, contentType := remote.Data, remote.ContentType
		// 返回缩略图
		c.Header("Content-Type", contentType)
		c.Header("Cache-Control", "private, max-age=31536000")
		c.Header("Content-Length", strconv.Itoa(len(data)))
		_, _ = io.Copy(c.Writer, bytes.NewReader(data))

		return
	}

	// 获取缩略图
	mediaService := NewMediaService(hs)

	thumbnail, reader, err := mediaService.GetThumbnail(serverName, mediaID, width, height, method)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Thumbnail not found",
		})

		return
	}

	defer func() { _ = reader.Close() }()

	// 设置响应头
	prefix := make([]byte, 512)
	n, _ := io.ReadFull(reader, prefix)
	prefix = prefix[:n]
	c.Header("Content-Type", http.DetectContentType(prefix))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, max-age=31536000")

	// 获取文件信息（安全类型断言）
	if f, ok := reader.(*os.File); ok {
		fileInfo, _ := f.Stat()
		if fileInfo != nil {
			c.Header("Content-Length", strconv.FormatInt(fileInfo.Size(), 10))
		}
	}

	if _, err := io.Copy(c.Writer, io.MultiReader(bytes.NewReader(prefix), reader)); err != nil {
		log.Warn("Failed to copy media response: %v", err)
	}

	_ = thumbnail
}

// extractMediaIDFromPath strips the leading slash and any mxc://serverName/ prefix from a wildcard path.
func extractMediaIDFromPath(serverName, mediaPath string) string {
	// Strip leading slash from wildcard param
	mediaID := strings.TrimPrefix(mediaPath, "/")
	// Strip mxc://serverName/ prefix if present (malformed URL case)
	prefix := "mxc://" + serverName + "/"
	if after, ok := strings.CutPrefix(mediaID, prefix); ok {
		mediaID = after
	} else if after, ok := strings.CutPrefix(mediaID, "mxc://"); ok {
		// Strip any mxc://otherServer/ prefix
		parts := strings.SplitN(after, "/", 2)
		if len(parts) == 2 {
			mediaID = parts[1]
		}
	}

	return mediaID
}

// handleDownloadMediaV1 handles authenticated /_matrix/client/v1/media/download with wildcard path.
// This handles malformed URLs where clients include mxc://server/ prefix in the path.
func handleDownloadMediaV1(c *gin.Context) {
	serverName := c.Param("serverName")
	mediaPath := c.Param("mediaPath")
	mediaID := extractMediaIDFromPath(serverName, mediaPath)
	// Override the mediaId param so handleDownloadMedia can use it
	c.Params = append(c.Params, gin.Param{Key: "mediaId", Value: mediaID})
	handleDownloadMedia(c)
}

// handleThumbnailMediaV1 handles authenticated /_matrix/client/v1/media/thumbnail with wildcard path.
func handleThumbnailMediaV1(c *gin.Context) {
	serverName := c.Param("serverName")
	mediaPath := c.Param("mediaPath")
	mediaID := extractMediaIDFromPath(serverName, mediaPath)
	c.Params = append(c.Params, gin.Param{Key: "mediaId", Value: mediaID})
	handleThumbnailMedia(c)
}

// handlePreviewURL 处理 URL 预览.
func handlePreviewURL(c *gin.Context) {
	previewURL := c.Query("url")
	if previewURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON", //nolint:goconst
			"error":   "Missing url parameter",
		})

		return
	}

	// 获取 URL 预览
	hs := GetHomeServer()
	preview := hs.GetURLPreview(previewURL)

	c.JSON(http.StatusOK, preview)
}

// URLPreview URL 预览结果.
type URLPreview struct {
	MatrixImageSize int64  `json:"matrix:image:size,omitempty"`
	OGImage         string `json:"og:image,omitempty"`
	OGImageHeight   int    `json:"og:image:height,omitempty"`
	OGImageWidth    int    `json:"og:image:width,omitempty"`
	OGImageType     string `json:"og:image:type,omitempty"`
	OGTitle         string `json:"og:title,omitempty"`
	OGType          string `json:"og:type,omitempty"`
	OGURL           string `json:"og:url,omitempty"`
	OGDescription   string `json:"og:description,omitempty"`
	OGSiteName      string `json:"og:site_name,omitempty"`
}

// isBlockedAddress reports whether an IP address is private, loopback,
// link-local, or otherwise unsuitable for outbound requests (SSRF protection).
func isBlockedAddress(ip net.IP) bool {
	if ip == nil {
		return true
	}

	// Block loopback (127.0.0.0/8, ::1).
	if ip.IsLoopback() {
		return true
	}

	// Block private ranges (10.x, 172.16-31.x, 192.168.x, fc00::/7).
	if ip.IsPrivate() {
		return true
	}

	// Block link-local (169.254.x.x, fe80::/10) — includes cloud metadata endpoints.
	if ip.IsLinkLocalUnicast() {
		return true
	}

	// Block unspecified (0.0.0.0).
	if ip.IsUnspecified() {
		return true
	}

	// Block multicast addresses.
	if ip.IsMulticast() {
		return true
	}

	return false
}

// validateURLForFetch validates a URL before making an outbound request.
// It enforces http/https schemes only and blocks private/internal IP
// addresses and suspicious hostnames to prevent SSRF attacks.
func validateURLForFetch(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Only allow http and https schemes.
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return errors.New("URL scheme must be http or https")
	}

	host := parsed.Hostname()
	if host == "" {
		return errors.New("URL host is empty")
	}

	// Resolve hostname and check all resolved IPs.
	// This catches DNS-based SSRF where a domain resolves to a private IP.
	resolver := net.Resolver{}
	ips, err := resolver.LookupIP(context.Background(), "ip", host) //nolint:noctx // validateURLForFetch 是无 context 的内部 API，SSRF 校验使用独立 context
	if err != nil {
		return fmt.Errorf("failed to resolve host %s: %w", host, err)
	}

	for _, ip := range ips {
		if isBlockedAddress(ip) {
			return fmt.Errorf("host %s resolves to a blocked address %s", host, ip.String())
		}
	}

	return nil
}

// GetURLPreview 获取 URL 预览.
func (hs *HomeServer) GetURLPreview(urlStr string) *URLPreview {
	// Validate URL to prevent SSRF attacks (e.g., accessing internal
	// services or cloud metadata endpoints).
	if err := validateURLForFetch(urlStr); err != nil {
		log.Warn("URL preview blocked: %v", err)

		return &URLPreview{
			OGTitle: urlStr,
			OGType:  "website",
			OGURL:   urlStr,
		}
	}

	// 使用 httpclient 获取页面
	client := httpclient.NewClient()
	req := client.NewRequest("GET", urlStr)
	req.SetTimeout(10)
	req.SetUserAgent("CommunityMatrixBot/1.0 (+https://github.com/matrix-org)")

	resp, err := req.Do()
	if err != nil {
		return &URLPreview{
			OGTitle: urlStr,
			OGType:  "website",
			OGURL:   urlStr,
		}
	}
	defer resp.Close()

	// 读取响应
	htmlContent, err := resp.String()
	if err != nil {
		return &URLPreview{
			OGTitle: urlStr,
			OGType:  "website",
			OGURL:   urlStr,
		}
	}

	// 解析 Open Graph 标签
	preview := parseOpenGraphTags(htmlContent, urlStr)

	// 下载 og:image 并上传为媒体
	if preview.OGImage != "" {
		hs.processPreviewImage(preview, urlStr)
	}

	return preview
}

// processPreviewImage 下载并处理预览图片.
func (hs *HomeServer) processPreviewImage(preview *URLPreview, baseURL string) {
	imageURL := preview.OGImage

	// 解析相对 URL
	if !strings.HasPrefix(imageURL, "http") {
		switch {
		case strings.HasPrefix(imageURL, "//"):
			imageURL = "https:" + imageURL
		case strings.HasPrefix(imageURL, "/"):
			if idx := strings.Index(baseURL, "://"); idx != -1 {
				endIdx := strings.Index(baseURL[idx+3:], "/")
				if endIdx != -1 {
					imageURL = baseURL[:idx+3+endIdx] + imageURL
				} else {
					imageURL = baseURL + imageURL
				}
			}
		default:
			// Relative path - resolve against base URL directory
			lastSlash := strings.LastIndex(baseURL, "/")
			if lastSlash != -1 {
				imageURL = baseURL[:lastSlash+1] + imageURL
			}
		}
	}

	// 下载图片
	imgClient := httpclient.NewClient()
	imgReq := imgClient.NewRequest("GET", imageURL)
	imgReq.SetTimeout(10)

	// Validate image URL to prevent SSRF (og:image can point anywhere).
	if err := validateURLForFetch(imageURL); err != nil {
		log.Warn("preview image URL blocked: %v", err)

		return
	}

	imgResp, err := imgReq.Do()
	if err != nil {
		return
	}
	defer imgResp.Close()

	imgBytes, err := imgResp.GetBodyData()
	if err != nil || len(imgBytes) == 0 {
		return
	}

	// Limit preview image size to prevent excessive memory usage (10 MB).
	const maxPreviewImageSize = 10 * 1024 * 1024
	if len(imgBytes) > maxPreviewImageSize {
		log.Warn("preview image too large: %d bytes", len(imgBytes))

		return
	}

	// 获取图片尺寸
	imgReader := bytes.NewReader(imgBytes)

	imgConfig, _, err := imagelib.DecodeConfig(imgReader)
	if err == nil {
		preview.OGImageWidth = imgConfig.Width
		preview.OGImageHeight = imgConfig.Height
	}

	preview.MatrixImageSize = int64(len(imgBytes))

	// 上传为媒体文件
	mediaService := NewMediaService(hs)
	contentType := http.DetectContentType(imgBytes)

	media, err := mediaService.UploadMedia(
		bytes.NewReader(imgBytes),
		nil,
		contentType,
		"",
	)
	if err != nil {
		return
	}

	// 设置 mxc:// URL
	preview.OGImage = fmt.Sprintf("mxc://%s/%s", hs.Config.ServerName, media.MediaID)
}

// parseOpenGraphTags 解析 Open Graph 标签.
func parseOpenGraphTags(html, baseURL string) *URLPreview {
	preview := &URLPreview{
		OGURL:  baseURL,
		OGType: "website",
	}

	// 简单解析 meta 标签
	// 查找 og:title
	if idx := strings.Index(html, `property="og:title"`); idx != -1 {
		preview.OGTitle = extractMetaContent(html[idx:])
	} else if idx := strings.Index(html, `<title>`); idx != -1 {
		end := strings.Index(html[idx:], `</title>`)
		if end != -1 {
			preview.OGTitle = strings.TrimSpace(html[idx+7 : idx+end])
		}
	}

	// 查找 og:description
	if idx := strings.Index(html, `property="og:description"`); idx != -1 {
		preview.OGDescription = extractMetaContent(html[idx:])
	} else if idx := strings.Index(html, `name="description"`); idx != -1 {
		preview.OGDescription = extractMetaContent(html[idx:])
	}

	// 查找 og:image
	if idx := strings.Index(html, `property="og:image"`); idx != -1 {
		preview.OGImage = extractMetaContent(html[idx:])
		// 处理相对路径
		if preview.OGImage != "" && !strings.HasPrefix(preview.OGImage, "http") {
			if strings.HasPrefix(preview.OGImage, "//") {
				preview.OGImage = "https:" + preview.OGImage
			} else if strings.HasPrefix(preview.OGImage, "/") {
				// 解析 base URL
				if idx := strings.Index(baseURL, "://"); idx != -1 {
					endIdx := strings.Index(baseURL[idx+3:], "/")
					if endIdx != -1 {
						preview.OGImage = baseURL[:idx+3+endIdx] + preview.OGImage
					} else {
						preview.OGImage = baseURL + preview.OGImage
					}
				}
			}
		}
	}

	// 查找 og:site_name
	if idx := strings.Index(html, `property="og:site_name"`); idx != -1 {
		preview.OGSiteName = extractMetaContent(html[idx:])
	}

	// 查找 og:type
	if idx := strings.Index(html, `property="og:type"`); idx != -1 {
		preview.OGType = extractMetaContent(html[idx:])
	}

	// 查找 og:url
	if idx := strings.Index(html, `property="og:url"`); idx != -1 {
		if ogURL := extractMetaContent(html[idx:]); ogURL != "" {
			preview.OGURL = ogURL
		}
	}

	// 如果没有标题，使用 URL
	if preview.OGTitle == "" {
		preview.OGTitle = baseURL
	}

	return preview
}

// extractMetaContent 提取 meta 标签的 content 属性.
func extractMetaContent(html string) string {
	// 查找 content="..."
	if _, after, ok := strings.Cut(html, `content="`); ok {
		rest := after
		if before, _, ok := strings.Cut(rest, `"`); ok {
			return strings.TrimSpace(before)
		}
	}
	// 查找 content='...'
	if _, after, ok := strings.Cut(html, `content='`); ok {
		rest := after
		if before, _, ok := strings.Cut(rest, `'`); ok {
			return strings.TrimSpace(before)
		}
	}

	return ""
}

// ===== 配置端点 =====

// handleGetMediaConfig 获取媒体配置.
func handleGetMediaConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"m.upload.size":        50 * 1024 * 1024, // 50MB
		"m.upload.size.images": 10 * 1024 * 1024, // 10MB for images
	})
}

// handleGetMediaConfigPerUser 获取用户媒体配置.
func handleGetMediaConfigPerUser(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"m.upload.size":        50 * 1024 * 1024,
		"m.upload.size.images": 10 * 1024 * 1024,
	})
}

// ===== 辅助函数 =====

// MXCToURL 将 MXC URI 转换为 HTTP URL.
func MXCToURL(mxcURI, homeserverURL string) string {
	if !strings.HasPrefix(mxcURI, "mxc://") {
		return ""
	}

	parts := strings.Split(strings.TrimPrefix(mxcURI, "mxc://"), "/")
	if len(parts) != 2 {
		return ""
	}

	return fmt.Sprintf("%s/_matrix/media/v3/download/%s/%s", homeserverURL, parts[0], parts[1])
}

// migrateOnce ensures migration happens only once.
var migrateOnce sync.Once

// InitMediaDB initializes media database tables.
func InitMediaDB() {
	migrateOnce.Do(func() {
		if db.GetDB() != nil {
			err := db.GetDB().AutoMigrate(&MatrixMedia{}, &MatrixThumbnail{})
			if err != nil {
				log.Error("Failed to migrate media tables: %v", err)
			}
		}
	})
}

// ToJSON 序列化为 JSON.
func (m *MatrixMedia) ToJSON() ([]byte, error) {
	return json.Marshal(m)
}

func originalMediaFilename(header *multipart.FileHeader, fallback string) string {
	if header == nil || header.Filename == "" {
		return fallback
	}
	return filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
}
