package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/thumbnail/processor"
	"github.com/kasuganosora/community/thumbnail/storage"
)

// Service 缩略图服务
type Service struct {
	config    *models.ThumbnailServiceConfig
	storage   storage.Backend
	processor processor.Processor
	enabled   bool
}

// NewService 创建缩略图服务
func NewService(cfg *models.ThumbnailServiceConfig, backend storage.Backend) (*Service, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	s := &Service{
		config:    cfg,
		storage:   backend,
		enabled:   cfg.Enabled,
		processor: processor.NewDefaultProcessor(),
	}

	return s, nil
}

// IsEnabled 是否启用
func (s *Service) IsEnabled() bool {
	return s.enabled
}

// GetConfig 获取配置
func (s *Service) GetConfig() *models.ThumbnailServiceConfig {
	return s.config
}

// GetStorage 获取存储后端
func (s *Service) GetStorage() storage.Backend {
	return s.storage
}

// ThumbnailResult 缩略图结果
type ThumbnailResult struct {
	OriginalURL  string `json:"original_url"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	Status       string `json:"status"`
	IsThumbnail  bool   `json:"is_thumbnail"`
	Reason       string `json:"reason,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
}

// GetThumbnailURL 获取缩略图URL
func (s *Service) GetThumbnailURL(ctx context.Context, originalURL string) (*ThumbnailResult, error) {
	if !s.enabled {
		return &ThumbnailResult{
			OriginalURL: originalURL,
			Status:      models.ThumbnailStatusSkipped,
			IsThumbnail: false,
			Reason:      "service disabled",
		}, nil
	}

	// 检查数据库中是否已有记录
	var thumb models.Thumbnail
	urlHash := models.GenerateURLHash(originalURL)
	err := db.GetDB().Where("original_url_hash = ?", urlHash).First(&thumb).Error

	if err == nil {
		// 找到记录
		switch thumb.Status {
		case models.ThumbnailStatusCompleted:
			return &ThumbnailResult{
				OriginalURL:  originalURL,
				ThumbnailURL: thumb.ThumbnailURL,
				Status:       thumb.Status,
				IsThumbnail:  true,
				Width:        thumb.ThumbnailWidth,
				Height:       thumb.ThumbnailHeight,
			}, nil

		case models.ThumbnailStatusProcessing, models.ThumbnailStatusPending:
			return &ThumbnailResult{
				OriginalURL: originalURL,
				Status:      thumb.Status,
				IsThumbnail: false,
				Reason:      "processing",
			}, nil

		case models.ThumbnailStatusFailed:
			// 失败时降级到原图
			return &ThumbnailResult{
				OriginalURL: originalURL,
				Status:      thumb.Status,
				IsThumbnail: false,
				Reason:      "failed: " + thumb.ErrorMessage,
			}, nil

		case models.ThumbnailStatusSkipped:
			return &ThumbnailResult{
				OriginalURL: originalURL,
				Status:      thumb.Status,
				IsThumbnail: false,
				Reason:      thumb.SkipReason,
			}, nil
		}
	}

	// 没有记录，检查是否需要生成缩略图
	match, rule, mediaType := s.matchRule(originalURL)
	if !match {
		// 不匹配规则，记录为跳过
		s.recordSkip(originalURL, "no matching rule")
		return &ThumbnailResult{
			OriginalURL: originalURL,
			Status:      models.ThumbnailStatusSkipped,
			IsThumbnail: false,
			Reason:      "no matching rule",
		}, nil
	}

	// 检查原图大小
	needThumbnail, size, err := s.checkOriginalSize(originalURL, rule)
	if err != nil {
		log.Warn("Failed to check original size for %s: %v", originalURL, err)
		// 出错时降级到原图
		return &ThumbnailResult{
			OriginalURL: originalURL,
			Status:      models.ThumbnailStatusSkipped,
			IsThumbnail: false,
			Reason:      "check size failed",
		}, nil
	}

	if !needThumbnail {
		s.recordSkip(originalURL, fmt.Sprintf("size %d <= threshold %d", size, rule.MaxOriginalSize))
		return &ThumbnailResult{
			OriginalURL: originalURL,
			Status:      models.ThumbnailStatusSkipped,
			IsThumbnail: false,
			Reason:      "size below threshold",
		}, nil
	}

	// 创建待处理记录
	thumb = models.Thumbnail{
		OriginalURL:       originalURL,
		OriginalURLHash:   urlHash,
		OriginalSize:      size,
		OriginalMediaType: mediaType,
		StorageType:       s.storage.Type(),
		Bucket:            s.config.Storage.Bucket,
		Status:            models.ThumbnailStatusPending,
	}

	if err := db.GetDB().Create(&thumb).Error; err != nil {
		log.Error("Failed to create thumbnail record: %v", err)
	}

	// 触发异步生成
	TriggerAsyncGeneration(originalURL, rule)

	return &ThumbnailResult{
		OriginalURL: originalURL,
		Status:      models.ThumbnailStatusPending,
		IsThumbnail: false,
		Reason:      "pending generation",
	}, nil
}

// BatchGetThumbnailURLs 批量获取缩略图URL
func (s *Service) BatchGetThumbnailURLs(ctx context.Context, urls []string) map[string]*ThumbnailResult {
	results := make(map[string]*ThumbnailResult)

	for _, url := range urls {
		result, err := s.GetThumbnailURL(ctx, url)
		if err != nil {
			results[url] = &ThumbnailResult{
				OriginalURL: url,
				Status:      models.ThumbnailStatusFailed,
				Reason:      err.Error(),
			}
		} else {
			results[url] = result
		}
	}

	return results
}

// ProcessContent 处理内容中的链接
func (s *Service) ProcessContent(ctx context.Context, content string) (string, error) {
	if !s.enabled || !s.config.RewriteEnabled {
		return content, nil
	}

	// 提取图片URL
	urls := extractImageURLs(content)
	if len(urls) == 0 {
		return content, nil
	}

	// 批量获取缩略图URL
	results := s.BatchGetThumbnailURLs(ctx, urls)

	// 替换内容中的URL
	processed := content
	for originalURL, result := range results {
		if result.Status == models.ThumbnailStatusCompleted && result.ThumbnailURL != "" {
			processed = strings.ReplaceAll(processed, originalURL, result.ThumbnailURL)
		}
	}

	return processed, nil
}

// matchRule 匹配规则
func (s *Service) matchRule(url string) (bool, *models.ThumbnailRule, string) {
	// 获取文件扩展名
	ext := strings.ToLower(filepath.Ext(url))
	if ext != "" {
		ext = ext[1:] // 去掉点
	}

	// 尝试从URL推断content-type
	contentType := inferContentType(url)

	for _, rule := range s.config.Rules {
		if !rule.Enabled {
			continue
		}

		// 检查扩展名匹配
		if len(rule.Extensions) > 0 {
			for _, e := range rule.Extensions {
				if strings.EqualFold(e, ext) {
					return true, &rule, rule.MediaType
				}
			}
		}

		// 检查content-type匹配
		if len(rule.ContentTypes) > 0 && contentType != "" {
			for _, ct := range rule.ContentTypes {
				if strings.HasPrefix(contentType, ct) {
					return true, &rule, rule.MediaType
				}
			}
		}
	}

	return false, nil, ""
}

// checkOriginalSize 检查原图大小
func (s *Service) checkOriginalSize(url string, rule *models.ThumbnailRule) (bool, int64, error) {
	// 发送HEAD请求获取大小
	resp, err := http.Head(url)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()

	size := resp.ContentLength

	// MaxOriginalSize为0表示所有都处理
	if rule.MaxOriginalSize == 0 {
		return true, size, nil
	}

	return size > rule.MaxOriginalSize, size, nil
}

// recordSkip 记录跳过
func (s *Service) recordSkip(url, reason string) {
	urlHash := models.GenerateURLHash(url)
	thumb := models.Thumbnail{
		OriginalURL:     url,
		OriginalURLHash: urlHash,
		StorageType:     s.storage.Type(),
		Status:          models.ThumbnailStatusSkipped,
		SkipReason:      reason,
	}

	// 使用Upsert避免重复
	db.GetDB().Where(models.Thumbnail{OriginalURLHash: urlHash}).
		Assign(thumb).
		FirstOrCreate(&models.Thumbnail{})
}

// extractImageURLs 提取内容中的图片URL
func extractImageURLs(content string) []string {
	// 使用正则表达式提取图片URL
	// 匹配常见的图片URL格式
	patterns := []string{
		`https?://[^\s"]+\.(jpg|jpeg|png|gif|webp|svg|bmp|ico)(\?[^\s"]*)?`,
		`https?://[^\s"]+\.(mp4|webm|mov)(\?[^\s"]*)?`,
	}

	var urls []string
	seen := make(map[string]bool)

	for _, pattern := range patterns {
		re := regexp.MustCompile(`(?i)` + pattern)
		matches := re.FindAllString(content, -1)
		for _, match := range matches {
			if !seen[match] {
				seen[match] = true
				urls = append(urls, match)
			}
		}
	}

	return urls
}

// inferContentType 从URL推断Content-Type
func inferContentType(url string) string {
	ext := strings.ToLower(filepath.Ext(url))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	default:
		return ""
	}
}

// GenerateCacheKey 生成缓存key
func GenerateCacheKey(url string, format string) string {
	hash := md5.Sum([]byte(url))
	hashStr := hex.EncodeToString(hash[:])
	return fmt.Sprintf("%s.%s", hashStr[:16], format)
}

// TriggerAsyncGeneration 触发异步生成
func TriggerAsyncGeneration(url string, rule *models.ThumbnailRule) {
	// 添加到任务队列
	job := models.ThumbnailJob{
		OriginalURL: url,
		Priority:    0,
		Status:      "pending",
		MaxRetries:  3,
	}

	if err := db.GetDB().Create(&job).Error; err != nil {
		log.Error("Failed to create thumbnail job: %v", err)
	}
}

// DeleteThumbnail 删除缩略图（包括存储和数据库记录）
func (s *Service) DeleteThumbnail(ctx context.Context, id uint64) error {
	// 获取缩略图记录
	var thumb models.Thumbnail
	if err := db.GetDB().First(&thumb, id).Error; err != nil {
		return fmt.Errorf("thumbnail not found: %w", err)
	}

	// 从存储删除文件
	if thumb.StorageKey != "" && s.storage != nil {
		if err := s.storage.Delete(ctx, thumb.StorageKey); err != nil {
			log.Error("Failed to delete thumbnail from storage: %v", err)
			// 继续删除数据库记录，不返回错误
		}
	}

	// 删除数据库记录
	if err := db.GetDB().Delete(&thumb).Error; err != nil {
		return fmt.Errorf("failed to delete thumbnail record: %w", err)
	}

	return nil
}

// DeleteThumbnailByURL 根据URL删除缩略图
func (s *Service) DeleteThumbnailByURL(ctx context.Context, url string) error {
	urlHash := models.GenerateURLHash(url)

	var thumb models.Thumbnail
	if err := db.GetDB().Where("original_url_hash = ?", urlHash).First(&thumb).Error; err != nil {
		return fmt.Errorf("thumbnail not found: %w", err)
	}

	return s.DeleteThumbnail(ctx, thumb.ID)
}

// CleanupThumbnails 清理缩略图
// olderThanDays: 删除多少天前的缩略图，0 表示不限制
// status: 指定状态删除，空字符串表示不限制
func (s *Service) CleanupThumbnails(ctx context.Context, olderThanDays int, status string) (int64, error) {
	query := db.GetDB().Model(&models.Thumbnail{})

	if olderThanDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -olderThanDays)
		query = query.Where("updated_at < ?", cutoff)
	}

	if status != "" {
		query = query.Where("status = ?", status)
	}

	// 获取要删除的记录
	var thumbs []models.Thumbnail
	if err := query.Find(&thumbs).Error; err != nil {
		return 0, fmt.Errorf("failed to find thumbnails: %w", err)
	}

	var deletedCount int64
	for _, thumb := range thumbs {
		// 从存储删除
		if thumb.StorageKey != "" && s.storage != nil {
			if err := s.storage.Delete(ctx, thumb.StorageKey); err != nil {
				log.Error("Failed to delete thumbnail %d from storage: %v", thumb.ID, err)
				continue
			}
		}

		// 删除数据库记录
		if err := db.GetDB().Delete(&thumb).Error; err != nil {
			log.Error("Failed to delete thumbnail %d from database: %v", thumb.ID, err)
			continue
		}

		deletedCount++
	}

	return deletedCount, nil
}

// RegenerateThumbnail 重新生成缩略图
func (s *Service) RegenerateThumbnail(ctx context.Context, id uint64) error {
	// 获取缩略图记录
	var thumb models.Thumbnail
	if err := db.GetDB().First(&thumb, id).Error; err != nil {
		return fmt.Errorf("thumbnail not found: %w", err)
	}

	// 删除旧的存储文件
	if thumb.StorageKey != "" && s.storage != nil {
		if err := s.storage.Delete(ctx, thumb.StorageKey); err != nil {
			log.Warn("Failed to delete old thumbnail from storage: %v", err)
		}
	}

	// 重置状态
	thumb.Status = models.ThumbnailStatusPending
	thumb.ErrorMessage = ""
	thumb.ThumbnailURL = ""
	thumb.StorageKey = ""
	if err := db.GetDB().Save(&thumb).Error; err != nil {
		return fmt.Errorf("failed to reset thumbnail status: %w", err)
	}

	// 触发异步生成
	_, rule, _ := s.matchRule(thumb.OriginalURL)
	if rule != nil {
		TriggerAsyncGeneration(thumb.OriginalURL, rule)
	}

	return nil
}

// GetThumbnailStats 获取缩略图统计
func (s *Service) GetThumbnailStats() (*ThumbnailStats, error) {
	var stats ThumbnailStats

	// 获取各种状态的计数
	db.GetDB().Model(&models.Thumbnail{}).Count(&stats.Total)
	db.GetDB().Model(&models.Thumbnail{}).Where("status = ?", models.ThumbnailStatusCompleted).Count(&stats.Completed)
	db.GetDB().Model(&models.Thumbnail{}).Where("status = ?", models.ThumbnailStatusFailed).Count(&stats.Failed)
	db.GetDB().Model(&models.Thumbnail{}).Where("status = ?", models.ThumbnailStatusPending).Count(&stats.Pending)
	db.GetDB().Model(&models.Thumbnail{}).Where("status = ?", models.ThumbnailStatusProcessing).Count(&stats.Processing)

	// 获取存储使用统计
	db.GetDB().Model(&models.Thumbnail{}).Where("status = ?", models.ThumbnailStatusCompleted).Select("COALESCE(SUM(thumbnail_size), 0)").Scan(&stats.StorageUsed)

	return &stats, nil
}

// ThumbnailStats 缩略图统计
type ThumbnailStats struct {
	Total       int64 `json:"total"`
	Completed   int64 `json:"completed"`
	Failed      int64 `json:"failed"`
	Pending     int64 `json:"pending"`
	Processing  int64 `json:"processing"`
	Skipped     int64 `json:"skipped"`
	StorageUsed int64 `json:"storage_used"`
}
