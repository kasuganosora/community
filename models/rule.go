package models

import (
	"gorm.io/gorm"
	"time"
)

type Rule struct {
	Id        uint64         `json:"id" gorm:"primary_key;autoIncrement"`
	Priority  uint           `json:"priority" gorm:"type:int;not null;default:0"`
	Text      string         `json:"text" gorm:"type:varchar(255);not null"`
	CreatedAt *time.Time     `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt *time.Time     `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

func (Rule) TableName() string {
	return "rules"
}
