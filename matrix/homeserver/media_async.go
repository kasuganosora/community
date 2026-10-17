package homeserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/storage"
	"gorm.io/gorm"
)

// ===== MSC2246: 异步媒体上传实现 =====

// AsyncUploadResponse 异步上传响应.
type AsyncUploadResponse struct {
	ContentURI string `json:"content_uri"`
}

// UploadConfigResponse 上传配置响应.
type UploadConfigResponse struct {
	MaxUploadSize    int64    `json:"m.upload.size,omitempty"`
	MaxFileSize      int64    `json:"m.max_file_size,omitempty"`
	SupportedFormats []string `json:"m.supported_formats,omitempty"`
}

// AsyncMediaInfo 异步媒体信息（存储在数据库）.
type AsyncMediaInfo struct {
	ClaimToken     string `gorm:"type:varchar(64)"`
	ClaimExpiresAt *time.Time

	ID          uint64    `gorm:"primaryKey;autoIncrement"`
	MediaID     string    `gorm:"uniqueIndex;type:varchar(64);not null"`
	UploadURL   string    `gorm:"type:varchar(512)"`
	ContentType string    `gorm:"type:varchar(128)"`
	Filename    string    `gorm:"type:varchar(255)"`
	Size        int64     `gorm:"default:0"`
	Sha256Hash  string    `gorm:"type:varchar(64)"`
	Status      string    `gorm:"type:varchar(16);default:'pending'"` // pending, processing, done, failed
	ContentURI  string    `gorm:"type:varchar(512)"`
	CreatedAt   time.Time `gorm:"autoCreateTime"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime"`
	ExpiresAt   *time.Time
	Uploader    string `gorm:"type:varchar(255)"`
}

func (AsyncMediaInfo) TableName() string {
	return "matrix_async_media"
}

// AsyncMediaService 异步媒体服务.
type AsyncMediaService struct {
	hs *HomeServer
}

// NewAsyncMediaService 创建异步媒体服务.
func NewAsyncMediaService(hs *HomeServer) *AsyncMediaService {
	return &AsyncMediaService{hs: hs}
}

// CreateAsyncUpload 创建异步上传.
func (ams *AsyncMediaService) CreateAsyncUpload(userID, filename, contentType string, size int64) (*AsyncMediaInfo, error) {
	if userID == "" {
		return nil, ErrMediaForbidden
	}
	if size < 0 || size > matrixMediaLimit {
		return nil, ErrMediaTooLarge
	}
	mediaID := generateAsyncMediaID()

	// 生成预签名上传 URL（简化实现，实际应该对接 S3/MinIO）
	uploadURL := "/_matrix/media/v3/upload/" + mediaID

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

	err := db.GetDB().Create(media).Error
	if err != nil {
		return nil, err
	}

	return media, nil
}

// ErrCannotOverwriteMedia is returned when trying to upload to an already-completed media ID.
var ErrCannotOverwriteMedia = errors.New("M_CANNOT_OVERWRITE_MEDIA")

var ErrMediaForbidden = errors.New("media belongs to another user")
var ErrMediaTooLarge = errors.New("media exceeds upload limit")

const asyncUploadTimeout = 5 * time.Minute
const asyncUploadLease = asyncUploadTimeout + 30*time.Second

type asyncContextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r asyncContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}

// CompleteAsyncUpload streams opaque bytes to a private file. The claim prevents
// concurrent requests from overwriting the same MXC identity.
func (ams *AsyncMediaService) CompleteAsyncUpload(ctx context.Context, mediaID, uploader string, source io.Reader, uploadContentType string) (*AsyncUploadResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, asyncUploadTimeout)
	defer cancel()
	// A request body must not keep its lease alive after the bounded upload ends.
	if closer, ok := source.(io.Closer); ok {
		stop := context.AfterFunc(ctx, func() { _ = closer.Close() })
		defer stop()
	}
	source = asyncContextReader{ctx: ctx, source: source}

	conn := db.GetDB().WithContext(ctx)
	var pending AsyncMediaInfo
	if err := conn.Where("media_id = ?", mediaID).First(&pending).Error; err != nil {
		return nil, errors.New("media not found")
	}
	if uploader == "" || pending.Uploader != uploader {
		return nil, ErrMediaForbidden
	}
	if pending.Status != "pending" && !(pending.Status == "processing" && pending.ClaimExpiresAt != nil && !pending.ClaimExpiresAt.After(time.Now())) {
		return nil, ErrCannotOverwriteMedia
	}
	if pending.ExpiresAt != nil && time.Now().After(*pending.ExpiresAt) {
		return nil, errors.New("upload expired")
	}
	if pending.Size > matrixMediaLimit {
		return nil, ErrMediaTooLarge
	}
	token := uuid.NewString()
	now := time.Now()
	leaseEnd := now.Add(asyncUploadLease)
	claim := conn.Model(&AsyncMediaInfo{}).Where("id = ? AND uploader = ? AND (status = ? OR (status = ? AND claim_expires_at <= ?))", pending.ID, uploader, "pending", "processing", now).Updates(map[string]any{"status": "processing", "claim_token": token, "claim_expires_at": leaseEnd})
	if claim.Error != nil {
		return nil, claim.Error
	}
	if claim.RowsAffected != 1 {
		return nil, ErrCannotOverwriteMedia
	}
	committed := false
	defer func() {
		if !committed {
			db.GetDB().Model(&AsyncMediaInfo{}).Where("id = ? AND status = ? AND claim_token = ?", pending.ID, "processing", token).Updates(map[string]any{"status": "pending", "claim_token": "", "claim_expires_at": nil})
		}
	}()
	contentType := pending.ContentType
	if uploadContentType != "" {
		contentType = uploadContentType
	}
	ext := filepath.Ext(originalMediaFilename(&multipart.FileHeader{Filename: pending.Filename}, ""))
	if ext == "" {
		exts, _ := mime.ExtensionsByType(contentType)
		if len(exts) > 0 {
			ext = exts[0]
		}
	}
	// Stage in a private temp file, then store through the storage backend
	// under the key "matrix/local/<date>/<name>" (local uploads/ or S3).
	file, err := os.CreateTemp("", "async-*"+ext)
	if err != nil {
		return nil, err
	}
	path := file.Name()
	key := "matrix/local/" + time.Now().Format("2006/01/02") + "/" + filepath.Base(path)
	stored := false
	defer func() {
		_ = file.Close()
		_ = os.Remove(path)
		if stored && !committed {
			_ = storage.Delete(key)
		}
	}()
	hash := sha256.New()
	size, err := copyAsyncMedia(io.MultiWriter(file, hash), source, matrixMediaLimit)
	if err != nil {
		return nil, err
	}
	if size > matrixMediaLimit {
		return nil, ErrMediaTooLarge
	}
	if pending.Size > 0 && pending.Size != size {
		return nil, errors.New("size mismatch")
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	if err = storage.PutFile(context.Background(), path, key, contentType); err != nil {
		return nil, err
	}
	stored = true
	uri := fmt.Sprintf("mxc://%s/%s", ams.hs.Config.ServerName, mediaID)
	record := &MatrixMedia{Origin: "local", MediaID: mediaID, ServerName: ams.hs.Config.ServerName, FileName: originalMediaFilename(&multipart.FileHeader{Filename: pending.Filename}, mediaID+ext), ContentType: contentType, FileSize: size, FilePath: key, UploaderID: uploader, CreatedAt: time.Now()}
	err = conn.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(record).Error; err != nil {
			return err
		}
		result := tx.Model(&AsyncMediaInfo{}).Where("id = ? AND status = ? AND claim_token = ? AND claim_expires_at > ?", pending.ID, "processing", token, time.Now()).Updates(map[string]any{"claim_token": "", "claim_expires_at": nil, "status": "done", "content_uri": uri, "size": size, "sha256_hash": hex.EncodeToString(hash.Sum(nil)), "content_type": contentType, "updated_at": time.Now()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrCannotOverwriteMedia
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	committed = true
	return &AsyncUploadResponse{ContentURI: uri}, nil
}

// copyAsyncMedia stops at the first excess byte without buffering the body.
func copyAsyncMedia(dst io.Writer, source io.Reader, limit int64) (int64, error) {
	n, err := io.Copy(dst, io.LimitReader(source, limit+1))
	if err == nil && n > limit {
		err = ErrMediaTooLarge
	}
	return n, err
}

// GetUploadStatus 获取上传状态.
func (ams *AsyncMediaService) GetUploadStatus(mediaID string) (*AsyncMediaInfo, error) {
	var media AsyncMediaInfo
	err := db.GetDB().Where("media_id = ?", mediaID).First(&media).Error
	if err != nil {
		return nil, err
	}

	return &media, nil
}

// GetUploadConfig 获取上传配置.
func (ams *AsyncMediaService) GetUploadConfig() *UploadConfigResponse {
	return &UploadConfigResponse{
		MaxUploadSize: matrixMediaLimit,
		MaxFileSize:   50 * 1024 * 1024, // 50MB
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

// CleanupExpiredUploads 清理过期的上传.
func (ams *AsyncMediaService) CleanupExpiredUploads() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := cleanupAsyncMedia(ctx, db.GetDB(), time.Now()); err != nil {
		log.Warn("Matrix async upload cleanup failed: %v", err)
	}
}

// ===== HTTP Handlers =====

// RegisterAsyncMediaRoutes 注册异步媒体路由.
func RegisterAsyncMediaRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	// Register under both v3 and v1 (Complement tests use v1)
	for _, version := range []string{"v3", "v1"} {
		media := r.Group("/_matrix/media/" + version)
		{
			media.GET("/config", handleMediaConfig)
			media.POST("/create", MatrixAuthMiddleware(), handleCreateAsyncUpload)
			// Use wildcard to handle both PUT /upload/:mediaId and PUT /upload/:serverName/:mediaId
			media.PUT("/upload/*path", MatrixAuthMiddleware(), handleCompleteAsyncUploadWildcard)
			media.GET("/upload/:mediaId", MatrixAuthMiddleware(), handleGetUploadStatus)
		}
	}
}

// handleMediaConfig 处理媒体配置请求.
func handleMediaConfig(c *gin.Context) {
	hs := GetHomeServer()
	config := hs.AsyncMedia.GetUploadConfig()
	c.JSON(http.StatusOK, config)
}

// handleCreateAsyncUpload 创建异步上传.
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
			"errcode": "M_INTERNAL_ERROR", //nolint:goconst
			"error":   err.Error(),        //nolint:goconst
		})

		return
	}

	hs2 := GetHomeServer()
	contentURI := fmt.Sprintf("mxc://%s/%s", hs2.Config.ServerName, media.MediaID)

	var unusedExpiresAt int64
	if media.ExpiresAt != nil {
		unusedExpiresAt = media.ExpiresAt.UnixMilli()
	}

	c.JSON(http.StatusOK, gin.H{
		"content_uri":       contentURI,
		"unused_expires_at": unusedExpiresAt,
	})
}

// handleCompleteAsyncUploadWildcard extracts mediaId from wildcard path
// Handles both /upload/{mediaId} and /upload/{serverName}/{mediaId}.
func handleCompleteAsyncUploadWildcard(c *gin.Context) {
	path := strings.TrimPrefix(c.Param("path"), "/")
	parts := strings.Split(path, "/")
	// Last segment is always the mediaId
	if len(parts) == 0 || path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_BAD_REQUEST", "error": "Missing media ID"}) //nolint:goconst

		return
	}

	c.Params = append(c.Params, gin.Param{Key: "mediaId", Value: parts[len(parts)-1]})
	handleCompleteAsyncUpload(c)
}

// handleCompleteAsyncUpload 完成异步上传.
func handleCompleteAsyncUpload(c *gin.Context) {
	hs := GetHomeServer()
	mediaID := c.Param("mediaId")

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, matrixMediaLimit)
	resp, err := hs.AsyncMedia.CompleteAsyncUpload(c.Request.Context(), mediaID, getUserID(c), c.Request.Body, c.GetHeader("Content-Type"))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.Is(err, ErrMediaForbidden) {
			c.JSON(403, gin.H{"errcode": "M_FORBIDDEN", "error": "Media belongs to another user"})
		} else if errors.Is(err, ErrMediaTooLarge) || errors.As(err, &tooLarge) {
			c.JSON(413, gin.H{"errcode": "M_TOO_LARGE", "error": "Media exceeds upload limit"})
		} else if errors.Is(err, ErrCannotOverwriteMedia) {
			c.JSON(http.StatusConflict, gin.H{
				"errcode": "M_CANNOT_OVERWRITE_MEDIA",
				"error":   "Media has already been uploaded",
			})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{
				"errcode": "M_BAD_REQUEST",
				"error":   err.Error(),
			})
		}

		return
	}

	c.JSON(http.StatusOK, resp)
}

// handleGetUploadStatus 获取上传状态.
func handleGetUploadStatus(c *gin.Context) {
	hs := GetHomeServer()
	mediaID := c.Param("mediaId")

	media, err := hs.AsyncMedia.GetUploadStatus(mediaID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND", //nolint:goconst
			"error":   "Upload not found",
		})

		return
	}

	if media.Uploader != getUserID(c) || getUserID(c) == "" {
		c.JSON(403, gin.H{"errcode": "M_FORBIDDEN", "error": "Media belongs to another user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"media_id":    media.MediaID,
		"status":      media.Status,
		"content_uri": media.ContentURI,
		"created_at":  media.CreatedAt.Unix(),
		"expires_at": func() int64 {
			if media.ExpiresAt != nil {
				return media.ExpiresAt.Unix()
			}

			return 0
		}(),
	})
}

// generateAsyncMediaID 生成异步媒体 ID.
func generateAsyncMediaID() string {
	return uuid.New().String()
}
