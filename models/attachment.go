package models

import (
	"gorm.io/gorm"
	"time"
)

const (
	AttachmentTypeUnknown = iota
	AttachmentTypeImage
	AttachmentTypeVideo
	AttachmentTypeAudio
	AttachmentTypeFile
	AttachmentTypeGifv
)

type Attachment struct {
	Id              uint64         `gorm:"primary_key;autoIncrement" json:"id"`
	Type            uint           `gorm:"type:tinyint(2);not null" json:"type"`
	SavePath        string         `gorm:"type:varchar(255);not null" json:"save_path"`
	PreviewSavePath string         `gorm:"type:varchar(255);" json:"preview_save_path"`
	Meta            map[string]any `gorm:"type:json;serializer:json" json:"meta"`
	Description     string         `gorm:"type:varchar(255);not null" json:"description"`
	BlurHash        *string        `gorm:"type:varchar(255);not null" json:"blurhash"`
	AccountId       uint64         `gorm:"type:bigint(20);index" json:"account_id"`
	FileSize        uint64         `gorm:"type:bigint(20);not null" json:"file_size"`
	PreviewFileSize uint64         `gorm:"type:bigint(20);not null" json:"preview_file_size"`
	TotalSize       uint64         `gorm:"type:bigint(20);not null" json:"total_size"`
	CreatedAt       *time.Time     `gorm:"type:timestamp;autoCreateTime" json:"created_at"`
	UpdatedAt       *time.Time     `gorm:"type:timestamp;autoUpdateTime" json:"updated_at"`
	DeleteAt        gorm.DeletedAt `gorm:"type:timestamp;index" json:"delete_at"`
}

func (Attachment) TableName() string {
	return "attachments"
}

func (a *Attachment) BeforeSave(tx *gorm.DB) (err error) {
	a.TotalSize = a.FileSize + a.PreviewFileSize
	return
}
