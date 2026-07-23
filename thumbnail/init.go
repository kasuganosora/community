package thumbnail

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/thumbnail/service"
	"github.com/kasuganosora/community/thumbnail/storage"
)

// globalService 全局缩略图服务.
var globalService *service.Service
var globalWorker *service.Worker

// Init 初始化缩略图服务.
func Init() error {
	// 自动迁移数据库
	if err := autoMigrate(); err != nil {
		return err
	}

	// 加载配置
	cfg, err := LoadConfig()
	if err != nil {
		log.Warn("Failed to load thumbnail config: %v", err)

		return nil
	}

	if !cfg.Enabled {
		log.Info("Thumbnail service is disabled")

		return nil
	}

	// 创建存储后端
	storageCfg := storage.Config{
		Type:       string(cfg.Storage.Type),
		Bucket:     cfg.Storage.Bucket,
		Region:     cfg.Storage.Region,
		Endpoint:   cfg.Storage.Endpoint,
		PathPrefix: cfg.Storage.PathPrefix,
		LocalPath:  cfg.Storage.LocalPath,
		PublicURL:  cfg.Storage.PublicURL,
		AccessKey:  cfg.Storage.AccessKey,
		SecretKey:  cfg.Storage.SecretKey,
	}

	backend, err := storage.NewBackend(storageCfg)
	if err != nil {
		log.Error("Failed to create storage backend: %v", err)

		return nil
	}

	// 转换配置
	svcCfg := &models.ThumbnailServiceConfig{
		Enabled:        cfg.Enabled,
		AccessMode:     models.AccessMode(cfg.AccessMode),
		RewriteEnabled: cfg.RewriteEnabled,
		Storage: models.ThumbnailStorageConfig{
			Type:       models.StorageType(cfg.Storage.Type),
			Bucket:     cfg.Storage.Bucket,
			Region:     cfg.Storage.Region,
			Endpoint:   cfg.Storage.Endpoint,
			PathPrefix: cfg.Storage.PathPrefix,
			LocalPath:  cfg.Storage.LocalPath,
			PublicURL:  cfg.Storage.PublicURL,
			AccessKey:  cfg.Storage.AccessKey,
			SecretKey:  cfg.Storage.SecretKey,
		},
		Rules: convertRules(cfg.Rules),
	}

	// 创建服务
	svc, err := service.NewService(svcCfg, backend)
	if err != nil {
		log.Error("Failed to create thumbnail service: %v", err)

		return nil // 不阻塞启动
	}

	globalService = svc

	// 启动工作器
	worker := service.NewWorker(svcCfg, backend)
	worker.Start(3)
	globalWorker = worker

	log.Info("Thumbnail service initialized with %d workers", 3)

	return nil
}

// Shutdown 关闭缩略图服务.
func Shutdown() {
	if globalWorker != nil {
		globalWorker.Stop()
	}
}

// autoMigrate 自动迁移数据库.
func autoMigrate() error {
	return db.GetDB().AutoMigrate(
		&models.Thumbnail{},
		&models.ThumbnailJob{},
		&models.ThumbnailStats{},
		&models.ThumbnailConfig{},
	)
}

// GetService 获取全局服务.
func GetService() *service.Service {
	return globalService
}

// convertRules 转换规则.
func convertRules(rules []ThumbnailRule) []models.ThumbnailRule {
	result := make([]models.ThumbnailRule, len(rules))
	for i, r := range rules {
		result[i] = models.ThumbnailRule{
			ContentTypes:      r.ContentTypes,
			Extensions:        r.Extensions,
			MaxOriginalSize:   r.MaxOriginalSize,
			MaxWidth:          r.MaxWidth,
			MaxHeight:         r.MaxHeight,
			Quality:           r.Quality,
			Format:            r.Format,
			Enabled:           r.Enabled,
			ExcludeAnimations: r.ExcludeAnimations,
			MediaType:         r.MediaType,
		}
	}

	return result
}
