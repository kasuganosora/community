package models

import (
	"time"
)

// MediaType 媒体类型
type MediaType string

const (
	MediaTypeImage   MediaType = "image"
	MediaTypeVideo   MediaType = "video"
	MediaTypeAudio   MediaType = "audio"
	MediaTypeUnknown MediaType = "unknown"
)

// MediaAttachment 媒体附件
type MediaAttachment struct {
	Id          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	AccountId   uint64    `gorm:"not null;index" json:"account_id"`
	StatusId    *uint64   `gorm:"index" json:"status_id"`
	FileName    string    `gorm:"size:255;not null" json:"file_name"`
	FilePath    string    `gorm:"size:500;not null" json:"file_path"`
	FileSize    int64     `gorm:"not null" json:"file_size"`
	ContentType string    `gorm:"size:100;not null" json:"content_type"`
	Type        MediaType `gorm:"size:20;not null;default:'unknown'" json:"type"`
	Width       *int      `json:"width"`
	Height      *int      `json:"height"`
	Duration    *float64  `json:"duration"`
	ThumbnailPath *string `gorm:"size:500" json:"thumbnail_path"`
	Description string    `gorm:"type:text" json:"description"`
	Blurhash    string    `gorm:"size:50" json:"blurhash"`
	FocusX      float64   `gorm:"default:0" json:"focus_x"`
	FocusY      float64   `gorm:"default:0" json:"focus_y"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`

	// 关联
	Account *Account `gorm:"foreignKey:AccountId" json:"-"`
	Status  *Status  `gorm:"foreignKey:StatusId" json:"-"`
}

// MediaAttachments 媒体附件列表
type MediaAttachments []*MediaAttachment

// GetMediaTypes 根据文件扩展名获取媒体类型
func GetMediaType(contentType string) MediaType {
	switch {
	case contentType == "image/jpeg" || contentType == "image/png" || 
	     contentType == "image/gif" || contentType == "image/webp":
		return MediaTypeImage
	case contentType == "video/mp4" || contentType == "video/webm" || 
	     contentType == "video/quicktime":
		return MediaTypeVideo
	case contentType == "audio/mpeg" || contentType == "audio/ogg" || 
	     contentType == "audio/wav":
		return MediaTypeAudio
	default:
		return MediaTypeUnknown
	}
}

// IsImage 是否为图片
func (m *MediaAttachment) IsImage() bool {
	return m.Type == MediaTypeImage
}

// IsVideo 是否为视频
func (m *MediaAttachment) IsVideo() bool {
	return m.Type == MediaTypeVideo
}

// IsAudio 是否为音频
func (m *MediaAttachment) IsAudio() bool {
	return m.Type == MediaTypeAudio
}
