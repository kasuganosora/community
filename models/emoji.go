package models

import "gorm.io/gorm"

type Emoji struct {
	ID        int64  `json:"id" gorm:"primary_key;autoIncrement"`
	Shortcode string `json:"shortcode" gorm:"unique;not null;type:varchar(36)"`
	Url       string `json:"url" gorm:"type:varchar(255);not null"`
	StaticUrl string `json:"static_url" gorm:"type:varchar(255);not null"`
	CreatedAt int64  `gorm:"autoCreateTime"`
	UpdatedAt int64  `gorm:"autoUpdateTime"`
	DeletedAt gorm.DeletedAt
}

func (Emoji) TableName() string {
	return "emojis"
}
