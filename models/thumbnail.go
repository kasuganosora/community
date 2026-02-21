package models

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// 缩略图状态常量
const (
	ThumbnailStatusPending    = "pending"
	ThumbnailStatusProcessing = "processing"
	ThumbnailStatusCompleted  = "completed"
	ThumbnailStatusFailed     = "failed"
	ThumbnailStatusSkipped    = "skipped"
)

// 配置项常量
const (
	ConfigKeyEnabled          = "thumbnail_enabled"
	ConfigKeyAccessMode       = "thumbnail_access_mode"
	ConfigKeyRewriteEnabled   = "thumbnail_rewrite_enabled"
	ConfigKeyStorageType      = "thumbnail_storage_type"
	ConfigKeyStorageBucket    = "thumbnail_storage_bucket"
	ConfigKeyStorageRegion    = "thumbnail_storage_region"
	ConfigKeyStorageEndpoint  = "thumbnail_storage_endpoint"
	ConfigKeyStoragePrefix    = "thumbnail_storage_prefix"
	ConfigKeyStoragePublicURL = "thumbnail_storage_public_url"
	ConfigKeyRules            = "thumbnail_rules"
)

// AccessMode 访问模式
type AccessMode string

const (
	AccessModeProxy    AccessMode = "proxy"    // 服务器代理访问
	AccessModeRedirect AccessMode = "redirect" // 直通模式
)

// StorageType 存储类型
type StorageType string

const (
	StorageTypeLocal      StorageType = "local"
	StorageTypeS3         StorageType = "s3"
	StorageTypeMinIO      StorageType = "minio"
	StorageTypeAliyunOSS  StorageType = "aliyun_oss"
	StorageTypeTencentCOS StorageType = "tencent_cos"
)

// ThumbnailServiceConfig 缩略图服务配置
type ThumbnailServiceConfig struct {
	Enabled        bool            `json:"enabled"`
	Storage        ThumbnailStorageConfig `json:"storage"`
	AccessMode     AccessMode      `json:"access_mode"`
	RewriteEnabled bool            `json:"rewrite_enabled"`
	Rules          []ThumbnailRule `json:"rules"`
}

// ThumbnailStorageConfig 存储配置
type ThumbnailStorageConfig struct {
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



// ThumbnailRule 缩略图规则
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

// GenerateURLHash 生成URL的哈希值
func GenerateURLHash(url string) string {
	hash := sha256.Sum256([]byte(url))
	return hex.EncodeToString(hash[:])
}

// Thumbnail 缩略图记录
//
// swagger:model Thumbnail
type Thumbnail struct {
	ID        uint64 `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// 原始文件信息
	OriginalURL       string `gorm:"size:2048" json:"original_url"`
	OriginalURLHash   string `gorm:"size:64;uniqueIndex" json:"original_url_hash"`
	OriginalSize      int64  `json:"original_size"`
	OriginalMediaType string `json:"original_media_type"` // image, video

	// 缩略图信息
	ThumbnailURL    string `gorm:"size:2048" json:"thumbnail_url,omitempty"`
	ThumbnailWidth  int    `json:"thumbnail_width,omitempty"`
	ThumbnailHeight int    `json:"thumbnail_height,omitempty"`
	ThumbnailFormat string `json:"thumbnail_format,omitempty"`
	ThumbnailSize   int64  `json:"thumbnail_size,omitempty"`

	// 存储信息
	StorageType string `json:"storage_type"`
	Bucket      string `json:"bucket"`
	StorageKey  string `json:"storage_key"`

	// 状态
	Status       string `json:"status"` // pending, processing, completed, failed, skipped
	ErrorMessage string `gorm:"size:512" json:"error_message,omitempty"`
	SkipReason   string `gorm:"size:256" json:"skip_reason,omitempty"`
}

// TableName 指定表名
func (Thumbnail) TableName() string {
	return "thumbnails"
}

// ThumbnailJob 缩略图生成任务
//
// swagger:model ThumbnailJob
type ThumbnailJob struct {
	ID          uint64 `gorm:"primaryKey" json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	OriginalURL string `gorm:"size:2048" json:"original_url"`
	Status      string `json:"status"` // pending, processing, completed, failed
	Priority    int    `json:"priority"`
	RetryCount  int    `json:"retry_count"`
	MaxRetries  int    `json:"max_retries"`
	Error       string `gorm:"size:512" json:"error,omitempty"`
	ProcessorID string `json:"processor_id,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// TableName 指定表名
func (ThumbnailJob) TableName() string {
	return "thumbnail_jobs"
}

// ThumbnailStats 缩略图统计
//
// swagger:model ThumbnailStats
type ThumbnailStats struct {
	ID               uint64    `gorm:"primaryKey"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	Date             time.Time `gorm:"index"`
	TotalRequests    int64     `json:"total_requests"`
	CacheHits        int64     `json:"cache_hits"`
	CacheMisses      int64     `json:"cache_misses"`
	GeneratedToday   int64     `json:"generated_today"`
	FailedToday      int64     `json:"failed_today"`
	StorageUsedBytes int64     `json:"storage_used_bytes"`
	FileCount        int64     `json:"file_count"`
}

// TableName 指定表名
func (ThumbnailStats) TableName() string {
	return "thumbnail_stats"
}

// ThumbnailConfig 缩略图配置（Key-Value 存储）
//
// swagger:model ThumbnailConfig
type ThumbnailConfig struct {
	ID        uint64    `gorm:"primaryKey"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Key       string    `gorm:"size:128;uniqueIndex;not null" json:"key"`
	Value     string    `gorm:"type:text" json:"value"`
}

// TableName 指定表名
func (ThumbnailConfig) TableName() string {
	return "thumbnail_configs"
}
