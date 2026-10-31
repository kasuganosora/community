package models

import (
	"context"
	"errors"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/kasuganosora/community/storage"
)

// MediaType 媒体类型.
type MediaType string

const (
	MediaTypeImage   MediaType = "image"
	MediaTypeVideo   MediaType = "video"
	MediaTypeAudio   MediaType = "audio"
	MediaTypeUnknown MediaType = "unknown"
)

// MediaAttachment 媒体附件.
type MediaAttachment struct {
	Id            uint64    `gorm:"primaryKey;autoIncrement"           json:"id"`
	AccountId     uint64    `gorm:"not null;index;type:bigint(20)"     json:"account_id"` // must match accounts.id (signed) for the FK
	StatusId      *uint64   `gorm:"index"                              json:"status_id"`
	FileName      string    `gorm:"size:255;not null"                  json:"file_name"`
	FilePath      string    `gorm:"size:500;not null"                  json:"file_path"`
	FileSize      int64     `gorm:"not null"                           json:"file_size"`
	ContentType   string    `gorm:"size:128;not null"                  json:"content_type"`
	Type          MediaType `gorm:"size:20;not null;default:'unknown'" json:"type"`
	Width         *int      `json:"width"`
	Height        *int      `json:"height"`
	Duration      *float64  `json:"duration"`
	ThumbnailPath *string   `gorm:"size:500"                           json:"thumbnail_path"`
	RemoteUrl     string    `gorm:"size:2048"                          json:"remote_url"` // original URL of federated media
	Description   string    `gorm:"type:text"                          json:"description"`
	Blurhash      string    `gorm:"size:255"                           json:"blurhash"`
	// Sensitive is the per-file flag federated servers send (Misskey, Mastodon).
	Sensitive bool    `gorm:"not null;default:false"             json:"sensitive"`
	FocusX    float64 `gorm:"default:0"                          json:"focus_x"`
	FocusY    float64 `gorm:"default:0"                          json:"focus_y"`
	// DetachedAt is set when a status delete unlinks this attachment and
	// cleared when it is linked again. Expiry uses updated_at, which that
	// delete also refreshes, so rows unlinked before this column existed are
	// still eligible once they are old enough.
	DetachedAt *time.Time `gorm:"index"                              json:"-"`
	CreatedAt  time.Time  `gorm:"autoCreateTime"                     json:"created_at"`
	UpdatedAt  time.Time  `gorm:"autoUpdateTime"                     json:"updated_at"`

	// 关联
	Account *Account `gorm:"foreignKey:AccountId" json:"-"`
	Status  *Status  `gorm:"foreignKey:StatusId"  json:"-"`
}

// MediaAttachments 媒体附件列表.
type MediaAttachments []*MediaAttachment

// GetMediaTypes 根据文件扩展名获取媒体类型.
func GetMediaType(contentType string) MediaType {
	switch contentType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return MediaTypeImage
	case "video/mp4", "video/webm", "video/quicktime":
		return MediaTypeVideo
	case "audio/mpeg", "audio/ogg", "audio/wav":
		return MediaTypeAudio
	default:
		return MediaTypeUnknown
	}
}

// IsImage 是否为图片.
func (m *MediaAttachment) IsImage() bool {
	return m.Type == MediaTypeImage
}

// IsVideo 是否为视频.
func (m *MediaAttachment) IsVideo() bool {
	return m.Type == MediaTypeVideo
}

// IsAudio 是否为音频.
func (m *MediaAttachment) IsAudio() bool {
	return m.Type == MediaTypeAudio
}

// ThumbPath returns the thumbnail path or "" when there is none.
func (m *MediaAttachment) ThumbPath() string {
	if m.ThumbnailPath == nil {
		return ""
	}

	return *m.ThumbnailPath
}

// MediaKeyPrefix is the storage key namespace of media attachments. The
// FilePath column holds the part of the object key below this prefix
// ("2026/10/09/<file>"), so the object key is "media/2026/10/09/<file>" for
// both the local backend (uploads/media/...) and S3.
const MediaKeyPrefix = "media/"

// MediaStorageKey maps a stored file_path to its object key. Besides the
// canonical relative form it accepts legacy values: full bucket URLs written
// by older S3 uploads, "uploads/..." host paths, and tootctl's top-level
// "refreshed/..." keys.
func MediaStorageKey(filePath string) string {
	if filePath == "" {
		return ""
	}

	trimmed := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(filePath)), "/")
	key, isURL := storage.NormalizeKey(filePath)

	switch {
	case key == "":
		return ""
	case isURL, strings.HasPrefix(trimmed, "uploads/"), strings.HasPrefix(key, "refreshed/"):
		return key
	default:
		return MediaKeyPrefix + key
	}
}

// NormalizeMediaFilePath converts a legacy file_path (e.g. a bucket URL) to
// the canonical form stored in the column (key without "media/").
func NormalizeMediaFilePath(filePath string) string {
	key := MediaStorageKey(filePath)
	if strings.HasPrefix(key, MediaKeyPrefix) {
		return strings.TrimPrefix(key, MediaKeyPrefix)
	}

	return key
}

// StorageKey returns the object key of the original file ("" if none).
func (m *MediaAttachment) StorageKey() string {
	return MediaStorageKey(m.FilePath)
}

// ThumbnailStorageKey returns the object key of the thumbnail ("" if none).
// Thumbnails live next to the original file.
func (m *MediaAttachment) ThumbnailStorageKey() string {
	thumb := m.ThumbPath()
	if thumb == "" {
		return ""
	}

	key := m.StorageKey()
	if key == "" {
		return ""
	}

	return path.Join(path.Dir(key), thumb)
}

// DeleteStoredFiles removes the original and thumbnail objects through the
// configured storage backend (local or S3). Missing objects are ignored.
func (m *MediaAttachment) DeleteStoredFiles(ctx context.Context) error {
	var errs []error

	for _, key := range []string{m.StorageKey(), m.ThumbnailStorageKey()} {
		if key == "" {
			continue
		}

		if err := storage.Default().Delete(ctx, key); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
