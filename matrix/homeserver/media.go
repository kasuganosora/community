package homeserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/httpclient"
	"github.com/kasuganosora/community/media"
	"github.com/kasuganosora/community/storage"
)

// MatrixMedia Matrix 媒体记录
type MatrixMedia struct {
	ID          uint64    `gorm:"primaryKey" json:"-"`
	MediaID     string    `gorm:"uniqueIndex;size:64;not null" json:"media_id"`
	ServerName  string    `gorm:"index;size:255;not null" json:"server_name"`
	FileName    string    `gorm:"size:255" json:"file_name"`
	ContentType string    `gorm:"size:100" json:"content_type"`
	FileSize    int64     `json:"file_size"`
	Width       *int      `json:"width,omitempty"`
	Height      *int      `json:"height,omitempty"`
	FilePath    string    `gorm:"size:500" json:"-"`
	UploaderID  string    `gorm:"size:255" json:"uploader_id"`
	CreatedAt   time.Time `json:"created_at"`
}

// TableName 表名
func (MatrixMedia) TableName() string {
	return "matrix_media"
}

// MatrixThumbnail Matrix 缩略图
type MatrixThumbnail struct {
	ID         uint64    `gorm:"primaryKey" json:"-"`
	MediaID    string    `gorm:"index;size:64;not null" json:"media_id"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	ResizeMethod string   `gorm:"size:20;default:'crop'" json:"resize_method"` // crop 或 scale
	FilePath   string    `gorm:"size:500" json:"-"`
	CreatedAt  time.Time `json:"created_at"`
}

// TableName 表名
func (MatrixThumbnail) TableName() string {
	return "matrix_media_thumbnails"
}

// MediaService 媒体服务
type MediaService struct {
	hs *HomeServer
}

// NewMediaService 创建媒体服务
func NewMediaService(hs *HomeServer) *MediaService {
	return &MediaService{hs: hs}
}

// UploadMedia 上传媒体文件
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
	
	// 确定存储路径
	datePath := time.Now().Format("2006/01/02")
	fileName := mediaID + ext
	relativePath := filepath.Join("matrix", datePath, fileName)
	fullPath := filepath.Join("uploads", relativePath)
	
	// 创建目录
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}
	
	// 创建目标文件
	dst, err := os.Create(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}
	defer dst.Close()
	
	// 复制文件内容
	fileSize, err := io.Copy(dst, file)
	if err != nil {
		return nil, fmt.Errorf("failed to save file: %w", err)
	}
	
	// 获取文件信息
	var width, height *int
	var thumbnailPath string
	
	// 处理图片
	if strings.HasPrefix(contentType, "image/") {
		processor := media.NewImageProcessor()
		thumbPath, _, w, h, err := processor.ProcessImage(fullPath)
		if err == nil {
			width = &w
			height = &h
			thumbnailPath = thumbPath
		}
	}
	
	// 上传到云存储（如果启用）
	cloudStorage := storage.GetStorage()
	if cloudStorage.IsEnabled() {
		cloudPath, err := cloudStorage.Upload(fullPath, "matrix/"+relativePath)
		if err == nil {
			fullPath = cloudPath
		}
	}
	
	// 创建数据库记录
	matrixMedia := &MatrixMedia{
		MediaID:     mediaID,
		ServerName:  s.hs.Config.ServerName,
		FileName:    fileName,
		ContentType: contentType,
		FileSize:    fileSize,
		Width:       width,
		Height:      height,
		FilePath:    fullPath,
		UploaderID:  uploaderID,
		CreatedAt:   time.Now(),
	}
	
	if err := db.GetDB().Create(matrixMedia).Error; err != nil {
		os.Remove(fullPath)
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
		db.GetDB().Create(thumbnail)
	}
	
	return matrixMedia, nil
}

// GetMedia 获取媒体文件
func (s *MediaService) GetMedia(serverName, mediaID string) (*MatrixMedia, io.ReadCloser, error) {
	var matrixMedia MatrixMedia
	if err := db.GetDB().Where("media_id = ? AND server_name = ?", mediaID, serverName).First(&matrixMedia).Error; err != nil {
		return nil, nil, fmt.Errorf("media not found")
	}
	
	// 尝试从云存储获取
	cloudStorage := storage.GetStorage()
	if cloudStorage.IsEnabled() {
		reader, err := cloudStorage.Download(matrixMedia.FilePath)
		if err == nil {
			return &matrixMedia, reader, nil
		}
	}
	
	// 从本地存储读取
	file, err := os.Open(matrixMedia.FilePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open file: %w", err)
	}
	
	return &matrixMedia, file, nil
}

// GetThumbnail 获取缩略图
func (s *MediaService) GetThumbnail(serverName, mediaID string, width, height int, method string) (*MatrixThumbnail, io.ReadCloser, error) {
	var thumbnail MatrixThumbnail
	query := db.GetDB().Where("media_id = ?", mediaID)
	
	if width > 0 {
		query = query.Where("width >= ?", width)
	}
	if height > 0 {
		query = query.Where("height >= ?", height)
	}
	if method != "" {
		query = query.Where("resize_method = ?", method)
	}
	
	if err := query.First(&thumbnail).Error; err != nil {
		return nil, nil, fmt.Errorf("thumbnail not found")
	}
	
	// 从本地存储读取
	file, err := os.Open(thumbnail.FilePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open thumbnail: %w", err)
	}
	
	return &thumbnail, file, nil
}

// GetMediaByMXC 通过 MXC URI 获取媒体
func (s *MediaService) GetMediaByMXC(mxcURI string) (*MatrixMedia, io.ReadCloser, error) {
	// 解析 mxc://server_name/media_id
	if !strings.HasPrefix(mxcURI, "mxc://") {
		return nil, nil, fmt.Errorf("invalid MXC URI")
	}
	
	parts := strings.Split(strings.TrimPrefix(mxcURI, "mxc://"), "/")
	if len(parts) != 2 {
		return nil, nil, fmt.Errorf("invalid MXC URI format")
	}
	
	return s.GetMedia(parts[0], parts[1])
}

// DeleteMedia 删除媒体
func (s *MediaService) DeleteMedia(serverName, mediaID string) error {
	var matrixMedia MatrixMedia
	if err := db.GetDB().Where("media_id = ? AND server_name = ?", mediaID, serverName).First(&matrixMedia).Error; err != nil {
		return fmt.Errorf("media not found")
	}
	
	// 删除缩略图
	db.GetDB().Where("media_id = ?", mediaID).Delete(&MatrixThumbnail{})
	
	// 删除媒体记录
	if err := db.GetDB().Delete(&matrixMedia).Error; err != nil {
		return err
	}
	
	// 删除文件
	cloudStorage := storage.GetStorage()
	if cloudStorage.IsEnabled() {
		cloudStorage.Delete(matrixMedia.FilePath)
	} else {
		os.Remove(matrixMedia.FilePath)
	}
	
	return nil
}

// generateMediaID 生成媒体 ID
func generateMediaID() string {
	// 使用 UUID + 时间戳生成唯一 ID
	data := fmt.Sprintf("%s%d", uuid.New().String(), time.Now().UnixNano())
	hash := sha256.Sum256([]byte(data))
	return base64.RawURLEncoding.EncodeToString(hash[:16])
}

// ===== HTTP Handlers =====

// handleUploadMedia 处理媒体上传
func handleUploadMedia(c *gin.Context) {
	hs := GetHomeServer()
	userID := getUserID(c)
	
	// 获取上传的文件
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "No file provided",
		})
		return
	}
	defer file.Close()
	
	// 获取 Content-Type
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	
	// 获取文件名（可选）
	filename := c.PostForm("filename")
	if filename == "" {
		filename = header.Filename
	}
	
	// 上传媒体
	mediaService := NewMediaService(hs)
	matrixMedia, err := mediaService.UploadMedia(file, header, contentType, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_UNKNOWN",
			"error":   err.Error(),
		})
		return
	}
	
	// 返回 MXC URI
	c.JSON(http.StatusOK, gin.H{
		"content_uri": fmt.Sprintf("mxc://%s/%s", hs.Config.ServerName, matrixMedia.MediaID),
	})
}

// handleDownloadMedia 处理媒体下载
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
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Media not found",
		})
		return
	}
	defer reader.Close()
	
	// 设置响应头
	c.Header("Content-Type", matrixMedia.ContentType)
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", matrixMedia.FileName))
	c.Header("Content-Length", fmt.Sprintf("%d", matrixMedia.FileSize))
	
	// 流式传输文件
	io.Copy(c.Writer, reader)
}

// handleRemoteMediaDownload 处理远程媒体下载
func handleRemoteMediaDownload(c *gin.Context, serverName, mediaID string) {
	hs := GetHomeServer()

	// 检查缓存
	mediaService := NewMediaService(hs)
	matrixMedia, reader, err := mediaService.GetMedia(serverName, mediaID)
	if err == nil {
		defer reader.Close()
		c.Header("Content-Type", matrixMedia.ContentType)
		c.Header("Content-Length", fmt.Sprintf("%d", matrixMedia.FileSize))
		io.Copy(c.Writer, reader)
		return
	}

	// 通过联邦获取远程媒体
	fedClient := NewFederationClient(hs)
	data, contentType, err := fedClient.FetchMedia(serverName, mediaID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Remote media not available",
		})
		return
	}

	// 保存到缓存
	reader = io.NopCloser(bytes.NewReader(data))
	header := &multipart.FileHeader{Size: int64(len(data))}
	cachedMedia, cacheErr := mediaService.UploadMedia(reader, header, contentType, "")
	if cacheErr == nil {
		// 更新服务器名称
		db.GetDB().Model(cachedMedia).Update("server_name", serverName)
	}

	// 返回媒体
	c.Header("Content-Type", contentType)
	c.Header("Content-Length", fmt.Sprintf("%d", len(data)))
	io.Copy(c.Writer, bytes.NewReader(data))
}

// handleThumbnailMedia 处理缩略图请求
func handleThumbnailMedia(c *gin.Context) {
	hs := GetHomeServer()
	serverName := c.Param("serverName")
	mediaID := c.Param("mediaId")
	
	// 解析参数
	width := 0
	height := 0
	method := "crop"
	
	if w := c.Query("width"); w != "" {
		fmt.Sscanf(w, "%d", &width)
	}
	if h := c.Query("height"); h != "" {
		fmt.Sscanf(h, "%d", &height)
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
		fedClient := NewFederationClient(hs)
		data, contentType, err := fedClient.FetchThumbnail(serverName, mediaID, width, height, method)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{
				"errcode": "M_NOT_FOUND",
				"error":   "Remote thumbnail not available",
			})
			return
		}

		// 返回缩略图
		c.Header("Content-Type", contentType)
		c.Header("Cache-Control", "public, max-age=31536000")
		c.Header("Content-Length", fmt.Sprintf("%d", len(data)))
		io.Copy(c.Writer, bytes.NewReader(data))
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
	defer reader.Close()

	// 设置响应头
	c.Header("Content-Type", "image/jpeg")
	c.Header("Cache-Control", "public, max-age=31536000")

	// 获取文件信息（安全类型断言）
	if f, ok := reader.(*os.File); ok {
		fileInfo, _ := f.Stat()
		if fileInfo != nil {
			c.Header("Content-Length", fmt.Sprintf("%d", fileInfo.Size()))
		}
	}
	
	io.Copy(c.Writer, reader)
	_ = thumbnail
}

// handlePreviewURL 处理 URL 预览
func handlePreviewURL(c *gin.Context) {
	previewURL := c.Query("url")
	if previewURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Missing url parameter",
		})
		return
	}

	// 获取 URL 预览
	hs := GetHomeServer()
	preview := hs.GetURLPreview(previewURL)

	c.JSON(http.StatusOK, preview)
}

// URLPreview URL 预览结果
type URLPreview struct {
	MatrixImageSize  int64  `json:"matrix:image:size,omitempty"`
	OGImage          string `json:"og:image,omitempty"`
	OGImageHeight    int    `json:"og:image:height,omitempty"`
	OGImageWidth     int    `json:"og:image:width,omitempty"`
	OGImageType      string `json:"og:image:type,omitempty"`
	OGTitle          string `json:"og:title,omitempty"`
	OGType           string `json:"og:type,omitempty"`
	OGURL            string `json:"og:url,omitempty"`
	OGDescription    string `json:"og:description,omitempty"`
	OGSiteName       string `json:"og:site_name,omitempty"`
}

// GetURLPreview 获取 URL 预览
func (hs *HomeServer) GetURLPreview(urlStr string) *URLPreview {
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
	return preview
}

// parseOpenGraphTags 解析 Open Graph 标签
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

	// 如果没有标题，使用 URL
	if preview.OGTitle == "" {
		preview.OGTitle = baseURL
	}

	return preview
}

// extractMetaContent 提取 meta 标签的 content 属性
func extractMetaContent(html string) string {
	// 查找 content="..."
	if idx := strings.Index(html, `content="`); idx != -1 {
		rest := html[idx+9:]
		if end := strings.Index(rest, `"`); end != -1 {
			return strings.TrimSpace(rest[:end])
		}
	}
	// 查找 content='...'
	if idx := strings.Index(html, `content='`); idx != -1 {
		rest := html[idx+9:]
		if end := strings.Index(rest, `'`); end != -1 {
			return strings.TrimSpace(rest[:end])
		}
	}
	return ""
}

// ===== 配置端点 =====

// handleGetMediaConfig 获取媒体配置
func handleGetMediaConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"m.upload.size":       50 * 1024 * 1024, // 50MB
		"m.upload.size.images": 10 * 1024 * 1024, // 10MB for images
	})
}

// handleGetMediaConfigPerUser 获取用户媒体配置
func handleGetMediaConfigPerUser(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"m.upload.size":       50 * 1024 * 1024,
		"m.upload.size.images": 10 * 1024 * 1024,
	})
}

// ===== 辅助函数 =====

// MXCToURL 将 MXC URI 转换为 HTTP URL
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

// init 初始化时自动迁移数据库表
func init() {
	// 延迟执行，确保数据库已初始化
	go func() {
		time.Sleep(1 * time.Second)
		if db.GetDB() != nil {
			db.GetDB().AutoMigrate(&MatrixMedia{}, &MatrixThumbnail{})
		}
	}()
}

// ToJSON 序列化为 JSON
func (m *MatrixMedia) ToJSON() ([]byte, error) {
	return json.Marshal(m)
}
