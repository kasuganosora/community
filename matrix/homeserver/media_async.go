package homeserver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
)

// ===== MSC2246: 异步媒体上传实现 =====

// AsyncUploadResponse 异步上传响应
type AsyncUploadResponse struct {
	ContentURI string `json:"content_uri"`
}

// UploadConfigResponse 上传配置响应
type UploadConfigResponse struct {
	MaxUploadSize    int64    `json:"m.upload.size,omitempty"`
	MaxFileSize      int64    `json:"m.max_file_size,omitempty"`
	SupportedFormats []string `json:"m.supported_formats,omitempty"`
}

// AsyncMediaInfo 异步媒体信息（存储在数据库）
type AsyncMediaInfo struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement"`
	MediaID      string    `gorm:"uniqueIndex;type:varchar(64);not null"`
	UploadURL    string    `gorm:"type:varchar(512)"`
	ContentType  string    `gorm:"type:varchar(128)"`
	Filename     string    `gorm:"type:varchar(255)"`
	Size         int64     `gorm:"default:0"`
	Sha256Hash   string    `gorm:"type:varchar(64)"`
	Status       string    `gorm:"type:varchar(16);default:'pending'"` // pending, processing, done, failed
	ContentURI   string    `gorm:"type:varchar(512)"`
	CreatedAt    time.Time `gorm:"autoCreateTime"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime"`
	ExpiresAt    *time.Time
	Uploader     string    `gorm:"type:varchar(255)"`
}

func (AsyncMediaInfo) TableName() string {
	return "matrix_async_media"
}

// AsyncMediaService 异步媒体服务
type AsyncMediaService struct {
	hs *HomeServer
}

// NewAsyncMediaService 创建异步媒体服务
func NewAsyncMediaService(hs *HomeServer) *AsyncMediaService {
	return &AsyncMediaService{hs: hs}
}

// CreateAsyncUpload 创建异步上传
func (ams *AsyncMediaService) CreateAsyncUpload(userID, filename, contentType string, size int64) (*AsyncMediaInfo, error) {
	mediaID := generateAsyncMediaID()

	// 生成预签名上传 URL（简化实现，实际应该对接 S3/MinIO）
	uploadURL := fmt.Sprintf("/_matrix/media/v3/upload/%s", mediaID)

	expiresAt := time.Now().Add(24 * time.Hour)

	media := &AsyncMediaInfo{
		MediaID:     mediaID,
		UploadURL:   uploadURL,
		Filename:    filename,
		ContentType: contentType,
		Size:        size,
		Status:      "pending",
		ExpiresAt:   &expiresAt,
		Uploader:    userID,
	}

	if err := db.GetDB().Create(media).Error; err != nil {
		return nil, err
	}

	return media, nil
}

// CompleteAsyncUpload 完成异步上传
func (ams *AsyncMediaService) CompleteAsyncUpload(mediaID string, data []byte) (*AsyncUploadResponse, error) {
	var media AsyncMediaInfo
	if err := db.GetDB().Where("media_id = ?", mediaID).First(&media).Error; err != nil {
		return nil, fmt.Errorf("media not found")
	}

	// 检查是否过期
	if media.ExpiresAt != nil && time.Now().After(*media.ExpiresAt) {
		return nil, fmt.Errorf("upload expired")
	}

	// 计算哈希
	hash := sha256.Sum256(data)
	media.Sha256Hash = hex.EncodeToString(hash[:])

	// 验证大小
	if media.Size > 0 && int64(len(data)) != media.Size {
		return nil, fmt.Errorf("size mismatch")
	}

	// 保存文件（使用现有的媒体存储）
	contentURI, err := ams.saveMediaFile(mediaID, data, media.ContentType, media.Filename)
	if err != nil {
		media.Status = "failed"
		db.GetDB().Save(&media)
		return nil, err
	}

	// 更新状态
	media.Status = "done"
	media.ContentURI = contentURI
	media.Size = int64(len(data))
	media.UpdatedAt = time.Now()

	if err := db.GetDB().Save(&media).Error; err != nil {
		return nil, err
	}

	return &AsyncUploadResponse{
		ContentURI: contentURI,
	}, nil
}

// saveMediaFile 保存媒体文件
func (ams *AsyncMediaService) saveMediaFile(mediaID string, data []byte, contentType, filename string) (string, error) {
	// 使用现有的媒体服务存储文件
	// 这里简化实现，实际应该调用 media.go 中的存储逻辑
	
	// 生成 mxc:// URI
	contentURI := fmt.Sprintf("mxc://%s/%s", ams.hs.Config.ServerName, mediaID)
	
	// TODO: 实际保存到存储后端（本地/S3/MinIO）
	
	return contentURI, nil
}

// GetUploadStatus 获取上传状态
func (ams *AsyncMediaService) GetUploadStatus(mediaID string) (*AsyncMediaInfo, error) {
	var media AsyncMediaInfo
	if err := db.GetDB().Where("media_id = ?", mediaID).First(&media).Error; err != nil {
		return nil, err
	}
	return &media, nil
}

// GetUploadConfig 获取上传配置
func (ams *AsyncMediaService) GetUploadConfig() *UploadConfigResponse {
	return &UploadConfigResponse{
		MaxUploadSize: 100 * 1024 * 1024, // 100MB
		MaxFileSize:   50 * 1024 * 1024,  // 50MB
		SupportedFormats: []string{
			"image/jpeg",
			"image/png",
			"image/gif",
			"image/webp",
			"video/mp4",
			"video/webm",
			"audio/mpeg",
			"audio/ogg",
			"application/pdf",
		},
	}
}

// CleanupExpiredUploads 清理过期的上传
func (ams *AsyncMediaService) CleanupExpiredUploads() {
	now := time.Now()
	db.GetDB().Where("status = ? AND expires_at < ?", "pending", now).Delete(&AsyncMediaInfo{})
}

// ===== HTTP Handlers =====

// RegisterAsyncMediaRoutes 注册异步媒体路由
func RegisterAsyncMediaRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	media := r.Group("/_matrix/media/v3")
	{
		// 上传配置
		media.GET("/config", handleMediaConfig)
		
		// 异步上传端点
		media.POST("/create", MatrixAuthMiddleware(), handleCreateAsyncUpload)
		media.PUT("/upload/:mediaId", handleCompleteAsyncUpload)
		media.GET("/upload/:mediaId", handleGetUploadStatus)
	}
}

// handleMediaConfig 处理媒体配置请求
func handleMediaConfig(c *gin.Context) {
	hs := GetHomeServer()
	config := hs.AsyncMedia.GetUploadConfig()
	c.JSON(http.StatusOK, config)
}

// handleCreateAsyncUpload 创建异步上传
func handleCreateAsyncUpload(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")

	var req struct {
		Filename    string `json:"filename,omitempty"`
		ContentType string `json:"content_type,omitempty"`
		Size        int64  `json:"size,omitempty"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		// 如果没有 body，使用查询参数
		req.Filename = c.Query("filename")
		req.ContentType = c.GetHeader("Content-Type")
	}

	media, err := hs.AsyncMedia.CreateAsyncUpload(userID, req.Filename, req.ContentType, req.Size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"media_id":   media.MediaID,
		"upload_url": media.UploadURL,
		"expires_at": media.ExpiresAt.Unix(),
	})
}

// handleCompleteAsyncUpload 完成异步上传
func handleCompleteAsyncUpload(c *gin.Context) {
	hs := GetHomeServer()
	mediaID := c.Param("mediaId")

	// 读取上传的数据
	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_REQUEST",
			"error":   "Failed to read upload data",
		})
		return
	}

	resp, err := hs.AsyncMedia.CompleteAsyncUpload(mediaID, data)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_REQUEST",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// handleGetUploadStatus 获取上传状态
func handleGetUploadStatus(c *gin.Context) {
	hs := GetHomeServer()
	mediaID := c.Param("mediaId")

	media, err := hs.AsyncMedia.GetUploadStatus(mediaID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Upload not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"media_id":    media.MediaID,
		"status":      media.Status,
		"content_uri": media.ContentURI,
		"created_at":  media.CreatedAt.Unix(),
		"expires_at":  media.ExpiresAt.Unix(),
	})
}

// generateAsyncMediaID 生成异步媒体 ID
func generateAsyncMediaID() string {
	return uuid.New().String()
}
