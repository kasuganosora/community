package thumbnail

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
)

// AccessMode 访问模式.
type AccessMode string

const (
	AccessModeProxy    AccessMode = "proxy"    // 服务器代理访问
	AccessModeRedirect AccessMode = "redirect" // 直通模式（重定向到原URL）
)

// StorageType 存储类型.
type StorageType string

const (
	StorageTypeLocal      StorageType = "local"
	StorageTypeS3         StorageType = "s3"
	StorageTypeMinIO      StorageType = "minio"
	StorageTypeAliyunOSS  StorageType = "aliyun_oss"
	StorageTypeTencentCOS StorageType = "tencent_cos"
)

// StorageConfig 存储配置.
type StorageConfig struct {
	Type       StorageType `json:"type"`
	Bucket     string      `json:"bucket"`
	Region     string      `json:"region"`
	Endpoint   string      `json:"endpoint"`
	PathPrefix string      `json:"path_prefix"`
	LocalPath  string      `json:"local_path"`
	PublicURL  string      `json:"public_url"`
	AccessKey  string      `json:"access_key"`
	SecretKey  string      `json:"secret_key"`
}

// ThumbnailRule 缩略图规则.
type ThumbnailRule struct {
	ContentTypes      []string `json:"content_types"`
	Extensions        []string `json:"extensions"`
	MaxOriginalSize   int64    `json:"max_original_size"`
	MaxWidth          int      `json:"max_width"`
	MaxHeight         int      `json:"max_height"`
	Quality           int      `json:"quality"`
	Format            string   `json:"format"`
	Enabled           bool     `json:"enabled"`
	ExcludeAnimations bool     `json:"exclude_animations"`
	MediaType         string   `json:"media_type"` // image, video
}

// Config 缩略图服务配置.
type Config struct {
	Enabled        bool            `json:"enabled"`
	Storage        StorageConfig   `json:"storage"`
	AccessMode     AccessMode      `json:"access_mode"`
	RewriteEnabled bool            `json:"rewrite_enabled"`
	Rules          []ThumbnailRule `json:"rules"`
}

// DefaultConfig 默认配置.
func DefaultConfig() *Config {
	return &Config{
		Enabled:        false,
		AccessMode:     AccessModeRedirect,
		RewriteEnabled: true,
		Storage: StorageConfig{
			Type:       StorageTypeLocal,
			PathPrefix: "thumbnails/", //nolint:goconst
			LocalPath:  "./storage/thumbnails",
			PublicURL:  "",
		},
		Rules: []ThumbnailRule{
			{
				ContentTypes:      []string{"image/jpeg", "image/png"}, //nolint:goconst
				Extensions:        []string{"jpg", "jpeg", "png"},      //nolint:goconst
				MaxOriginalSize:   524288,                              // 512KB
				MaxWidth:          1920,
				MaxHeight:         1080,
				Quality:           85,
				Format:            "webp", //nolint:goconst
				Enabled:           true,
				ExcludeAnimations: false,
				MediaType:         "image", //nolint:goconst
			},
			{
				ContentTypes:      []string{"image/webp"},
				Extensions:        []string{"webp"},
				MaxOriginalSize:   0, // 所有都处理
				MaxWidth:          1920,
				MaxHeight:         1080,
				Quality:           85,
				Format:            "webp",
				Enabled:           true,
				ExcludeAnimations: true,
				MediaType:         "image",
			},
			{
				ContentTypes: []string{"image/gif"},
				Extensions:   []string{"gif"}, //nolint:goconst
				Enabled:      false,
				MediaType:    "image",
			},
			{
				ContentTypes: []string{"image/svg+xml"},
				Extensions:   []string{"svg"},
				Enabled:      false,
				MediaType:    "image",
			},
			{
				ContentTypes:    []string{"image/heic", "image/heif"},
				Extensions:      []string{"heic", "heif"},
				MaxOriginalSize: 0,
				MaxWidth:        1920,
				MaxHeight:       1080,
				Quality:         85,
				Format:          "webp",
				Enabled:         true,
				MediaType:       "image",
			},
			{
				ContentTypes:    []string{"image/avif"},
				Extensions:      []string{"avif"},
				MaxOriginalSize: 0,
				MaxWidth:        1920,
				MaxHeight:       1080,
				Quality:         85,
				Format:          "webp",
				Enabled:         true,
				MediaType:       "image",
			},
			{
				ContentTypes:    []string{"video/mp4", "video/webm", "video/quicktime"},
				Extensions:      []string{"mp4", "webm", "mov"},
				MaxOriginalSize: 10485760, // 10MB
				MaxWidth:        1280,
				MaxHeight:       720,
				Quality:         80,
				Format:          "webp",
				Enabled:         true,
				MediaType:       "video",
			},
		},
	}
}

// LoadConfig 从数据库加载配置.
func LoadConfig() (*Config, error) {
	cfg := DefaultConfig()

	// 从数据库加载各项配置
	var configs []models.ThumbnailConfig
	err := db.GetDB().Find(&configs).Error
	if err != nil {
		log.Warn("Failed to load thumbnail configs from database: %v", err)

		return cfg, nil
	}

	configMap := make(map[string]string)
	for _, c := range configs {
		configMap[c.Key] = c.Value
	}

	// 解析基本配置
	if v, ok := configMap[models.ConfigKeyEnabled]; ok {
		cfg.Enabled = v == "true"
	}

	if v, ok := configMap[models.ConfigKeyAccessMode]; ok {
		cfg.AccessMode = AccessMode(v)
	}

	if v, ok := configMap[models.ConfigKeyRewriteEnabled]; ok {
		cfg.RewriteEnabled = v == "true"
	}

	// 解析存储配置
	if v, ok := configMap[models.ConfigKeyStorageType]; ok {
		cfg.Storage.Type = StorageType(v)
	}

	if v, ok := configMap[models.ConfigKeyStorageBucket]; ok {
		cfg.Storage.Bucket = v
	}

	if v, ok := configMap[models.ConfigKeyStorageRegion]; ok {
		cfg.Storage.Region = v
	}

	if v, ok := configMap[models.ConfigKeyStorageEndpoint]; ok {
		cfg.Storage.Endpoint = v
	}

	if v, ok := configMap[models.ConfigKeyStoragePrefix]; ok {
		cfg.Storage.PathPrefix = v
	}

	if v, ok := configMap[models.ConfigKeyStoragePublicURL]; ok {
		cfg.Storage.PublicURL = v
	}

	// 从环境变量读取凭证
	cfg.Storage.AccessKey = os.Getenv("THUMBNAIL_STORAGE_ACCESS_KEY")
	cfg.Storage.SecretKey = os.Getenv("THUMBNAIL_STORAGE_SECRET_KEY")

	// 解析规则
	if v, ok := configMap[models.ConfigKeyRules]; ok && v != "" {
		var rules []ThumbnailRule
		err := json.Unmarshal([]byte(v), &rules)
		if err == nil {
			cfg.Rules = rules
		}
	}

	return cfg, nil
}

// SaveConfig 保存配置到数据库.
func SaveConfig(cfg *Config) error {
	tx := db.GetDB().Begin()

	// 保存基本配置
	configs := []models.ThumbnailConfig{ //nolint:prealloc
		{Key: models.ConfigKeyEnabled, Value: strconv.FormatBool(cfg.Enabled)},
		{Key: models.ConfigKeyAccessMode, Value: string(cfg.AccessMode)},
		{Key: models.ConfigKeyRewriteEnabled, Value: strconv.FormatBool(cfg.RewriteEnabled)},
		{Key: models.ConfigKeyStorageType, Value: string(cfg.Storage.Type)},
		{Key: models.ConfigKeyStorageBucket, Value: cfg.Storage.Bucket},
		{Key: models.ConfigKeyStorageRegion, Value: cfg.Storage.Region},
		{Key: models.ConfigKeyStorageEndpoint, Value: cfg.Storage.Endpoint},
		{Key: models.ConfigKeyStoragePrefix, Value: cfg.Storage.PathPrefix},
		{Key: models.ConfigKeyStoragePublicURL, Value: cfg.Storage.PublicURL},
	}

	// 序列化规则
	rulesJSON, err := json.Marshal(cfg.Rules)
	if err != nil {
		return fmt.Errorf("failed to marshal rules: %w", err)
	}

	configs = append(configs, models.ThumbnailConfig{
		Key:   models.ConfigKeyRules,
		Value: string(rulesJSON),
	})

	// 批量保存
	for _, c := range configs {
		err := tx.Where(models.ThumbnailConfig{Key: c.Key}).
			Assign(c).
			FirstOrCreate(&models.ThumbnailConfig{}).Error
		if err != nil {
			tx.Rollback()

			return err
		}
	}

	return tx.Commit().Error
}

// GetStoragePublicURL 获取存储公共URL.
func (c *Config) GetStoragePublicURL() string {
	if c.Storage.PublicURL != "" {
		return c.Storage.PublicURL
	}

	// 根据不同存储类型生成默认URL
	switch c.Storage.Type {
	case StorageTypeS3:
		if c.Storage.Region != "" {
			return fmt.Sprintf("https://%s.s3.%s.amazonaws.com", c.Storage.Bucket, c.Storage.Region)
		}

		return fmt.Sprintf("https://%s.s3.amazonaws.com", c.Storage.Bucket)
	case StorageTypeMinIO:
		return fmt.Sprintf("%s/%s", c.Storage.Endpoint, c.Storage.Bucket)
	case StorageTypeLocal:
		return "/thumbnails" // 本地存储使用相对路径
	}

	return ""
}
