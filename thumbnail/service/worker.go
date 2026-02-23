package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/thumbnail/processor"
	"github.com/kasuganosora/community/thumbnail/storage"
)

// Worker 缩略图生成工作器
type Worker struct {
	config    *models.ThumbnailServiceConfig
	storage   storage.Backend
	processor processor.Processor
	stopChan  chan struct{}
}

// NewWorker 创建工作器
func NewWorker(cfg *models.ThumbnailServiceConfig, backend storage.Backend) *Worker {
	return &Worker{
		config:    cfg,
		storage:   backend,
		processor: processor.NewDefaultProcessor(),
		stopChan:  make(chan struct{}),
	}
}

// Start 启动工作器
func (w *Worker) Start(workers int) {
	if workers <= 0 {
		workers = 3
	}

	log.Info("Starting thumbnail workers: %d", workers)

	for i := 0; i < workers; i++ {
		go w.run(i)
	}
}

// Stop 停止工作器
func (w *Worker) Stop() {
	close(w.stopChan)
	log.Info("Thumbnail workers stopped")
}

// run 工作循环
func (w *Worker) run(id int) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopChan:
			return
		case <-ticker.C:
			w.processJobs(id)
		}
	}
}

// processJobs 处理任务
func (w *Worker) processJobs(workerID int) {
	// 获取待处理的任务
	var jobs []models.ThumbnailJob
	db.GetDB().
		Where("status = ?", "pending").
		Order("priority DESC, created_at ASC").
		Limit(10).
		Find(&jobs)

	for _, job := range jobs {
		if err := w.processJob(&job); err != nil {
			log.Error("Worker %d failed to process job %d: %v", workerID, job.ID, err)
			w.markFailed(&job, err.Error())
		}
	}
}

// processJob 处理单个任务
func (w *Worker) processJob(job *models.ThumbnailJob) error {
	// 标记为处理中
	now := time.Now()
	job.Status = "processing"
	job.StartedAt = &now
	db.GetDB().Save(job)

	// 获取缩略图记录
	var thumb models.Thumbnail
	if err := db.GetDB().Where("original_url = ?", job.OriginalURL).First(&thumb).Error; err != nil {
		return fmt.Errorf("thumbnail record not found: %w", err)
	}

	// 更新缩略图状态
	thumb.Status = models.ThumbnailStatusProcessing
	db.GetDB().Save(&thumb)

	// 匹配规则
	_, rule, mediaType := w.matchRule(job.OriginalURL)
	if rule == nil {
		return fmt.Errorf("no matching rule found")
	}

	// 下载原图
	tempFile, err := w.downloadOriginal(job.OriginalURL)
	if err != nil {
		return fmt.Errorf("failed to download original: %w", err)
	}
	defer os.Remove(tempFile)

	// 生成缩略图
	var thumbData io.Reader
	var thumbWidth, thumbHeight int
	var thumbFormat string

	opts := processor.ThumbnailOptions{
		MaxOriginalSize:   rule.MaxOriginalSize,
		MaxWidth:          rule.MaxWidth,
		MaxHeight:         rule.MaxHeight,
		Quality:           rule.Quality,
		Format:            rule.Format,
		ExcludeAnimations: rule.ExcludeAnimations,
	}

	if mediaType == "video" {
		// 视频使用视频处理器
		videoFile, videoErr := os.Open(tempFile)
		if videoErr != nil {
			return fmt.Errorf("failed to open temp file for video: %w", videoErr)
		}
		defer videoFile.Close()
		videoProc := processor.NewVideoProcessor("")
		thumbData, thumbWidth, thumbHeight, thumbFormat, err = videoProc.GenerateThumbnail(videoFile, opts)
	} else {
		// 图片使用图片处理器
		file, err := os.Open(tempFile)
		if err != nil {
			return fmt.Errorf("failed to open temp file: %w", err)
		}
		defer file.Close()

		thumbData, thumbWidth, thumbHeight, thumbFormat, err = w.processor.GenerateThumbnail(file, opts)
	}

	if err != nil {
		return fmt.Errorf("failed to generate thumbnail: %w", err)
	}

	// 读取数据到buffer
	buf := new(bytes.Buffer)
	size, err := buf.ReadFrom(thumbData)
	if err != nil {
		return fmt.Errorf("failed to read thumbnail data: %w", err)
	}

	// 生成缓存key
	cacheKey := GenerateCacheKey(job.OriginalURL, thumbFormat)

	// 上传到存储
	ctx := context.Background()
	contentType := fmt.Sprintf("image/%s", thumbFormat)
	if err := w.storage.Put(ctx, cacheKey, bytes.NewReader(buf.Bytes()), size, contentType); err != nil {
		return fmt.Errorf("failed to upload thumbnail: %w", err)
	}

	// 获取缩略图URL
	thumbURL := w.storage.GetURL(cacheKey)

	// 更新记录
	thumb.ThumbnailURL = thumbURL
	thumb.StorageKey = cacheKey
	thumb.ThumbnailSize = size
	thumb.ThumbnailWidth = thumbWidth
	thumb.ThumbnailHeight = thumbHeight
	thumb.ThumbnailFormat = thumbFormat
	thumb.Status = models.ThumbnailStatusCompleted
	now = time.Now()

	if err := db.GetDB().Save(&thumb).Error; err != nil {
		return fmt.Errorf("failed to update thumbnail record: %w", err)
	}

	// 完成任务
	job.Status = "completed"
	job.CompletedAt = &now
	db.GetDB().Save(job)

	log.Info("Thumbnail generated for %s: %s (%dx%d)", job.OriginalURL, thumbURL, thumbWidth, thumbHeight)

	return nil
}

// downloadOriginal 下载原图
func (w *Worker) downloadOriginal(url string) (string, error) {
	// 创建临时文件
	tempDir := os.TempDir()
	tempFile := filepath.Join(tempDir, fmt.Sprintf("thumb_dl_%d", time.Now().UnixNano()))

	// 下载
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	// 写入文件
	file, err := os.Create(tempFile)
	if err != nil {
		return "", err
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		os.Remove(tempFile)
		return "", err
	}

	return tempFile, nil
}

// markFailed 标记失败
func (w *Worker) markFailed(job *models.ThumbnailJob, errorMsg string) {
	job.Status = "failed"
	job.Error = errorMsg
	job.RetryCount++
	db.GetDB().Save(job)

	// 更新缩略图记录
	var thumb models.Thumbnail
	if err := db.GetDB().Where("original_url = ?", job.OriginalURL).First(&thumb).Error; err == nil {
		thumb.Status = models.ThumbnailStatusFailed
		thumb.ErrorMessage = errorMsg
		db.GetDB().Save(&thumb)
	}
}

// matchRule 匹配规则
func (w *Worker) matchRule(url string) (bool, *models.ThumbnailRule, string) {
	ext := filepath.Ext(url)
	if ext != "" {
		ext = ext[1:]
	}

	contentType := ""
	for _, rule := range w.config.Rules {
		if !rule.Enabled {
			continue
		}

		// 检查扩展名
		if len(rule.Extensions) > 0 {
			for _, e := range rule.Extensions {
				if e == ext {
					return true, &rule, rule.MediaType
				}
			}
		}

		// 检查content-type
		if len(rule.ContentTypes) > 0 && contentType != "" {
			for _, ct := range rule.ContentTypes {
				if contentType == ct {
					return true, &rule, rule.MediaType
				}
			}
		}
	}

	return false, nil, ""
}
