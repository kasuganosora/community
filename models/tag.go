package models

import "time"

type Tag struct {
	Id               uint64     `gorm:"primary_key;auto_increment" json:"id"`
	Name             string     `gorm:"type:varchar(64);not null;index" json:"name"`
	StatusCount      uint64     `gorm:"not null;default:0" json:"status_count"`
	AccountCount     uint64     `gorm:"not null;default:0" json:"account_count"`
	LastStatusUsedAt *time.Time `gorm:"index" json:"last_status_used_at"`
	CreatedAt        *time.Time `gorm:"not null;autoCreateTime;index" json:"created_at"`
	UpdatedAt        *time.Time `gorm:"not null;autoUpdateTime" json:"updated_at"`
}

func (t *Tag) TableName() string {
	return "tags"
}

type Tags []*Tag
