package models

import "gorm.io/gorm"

type Emoji struct {
	Id              uint64 `json:"id" gorm:"primary_key;autoIncrement"`
	Shortcode       string `json:"shortcode" gorm:"unique;not null;type:varchar(36)"`
	Url             string `json:"url" gorm:"type:varchar(255);not null"`
	StaticUrl       string `json:"static_url" gorm:"type:varchar(255);not null"`
	VisibleInPicker bool   `json:"visible_in_picker"`
	Domain          string `json:"domain" gorm:"type:varchar(255);not null;index"` // 域名,如果位空则为本站自己的emoji
	Category        string `json:"category"`
	CreatedAt       int64  `gorm:"autoCreateTime"`
	UpdatedAt       int64  `gorm:"autoUpdateTime"`
	DeletedAt       gorm.DeletedAt
}

func (Emoji) TableName() string {
	return "emojis"
}
