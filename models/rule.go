package models

import (
	"gorm.io/gorm"
	"time"
)

type Rule struct {
	Id        uint64         `gorm:"primary_key;autoIncrement"   json:"id"`
	Priority  uint           `gorm:"type:int;not null;default:0" json:"priority"`
	Text      string         `gorm:"type:varchar(255);not null"  json:"text"`
	CreatedAt *time.Time     `gorm:"autoCreateTime"              json:"created_at"`
	UpdatedAt *time.Time     `gorm:"autoUpdateTime"              json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index"                       json:"deleted_at"`
}

func (Rule) TableName() string {
	return "rules"
}
