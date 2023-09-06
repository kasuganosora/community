package models

import "time"

type Announcement struct {
	Id                uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Text              string     `gorm:"type:text;not null" json:"text"`
	Published         bool       `gorm:"default:false;not null" json:"published"`
	AllDay            bool       `gorm:"default:false;not null" json:"all_day"`
	ScheduledAt       *time.Time `gorm:"type:datetime" json:"scheduled_at"`
	StartsAt          *time.Time `gorm:"type:datetime" json:"starts_at"`
	EndsAt            *time.Time `gorm:"type:datetime" json:"ends_at"`
	CreatedAt         *time.Time `gorm:"type:datetime;not null;autoCreateTime" json:"created_at"`
	UpdatedAt         *time.Time `gorm:"type:datetime;not null;autoUpdateTime" json:"updated_at"`
	PublishedAt       *time.Time `gorm:"type:datetime" json:"published_at"`
	StatusIds         []uint64   `gorm:"type:json;serializer:json" json:"status_ids"`
	MentionAccountIds []uint64   `gorm:"type:json;serializer:json" json:"mentions_account_ids"`
	Tags              []string   `gorm:"type:json;serializer:json" json:"tags"`
	EmojisId          []uint64   `gorm:"type:json;serializer:json" json:"emojis_id"`
}

func (Announcement) TableName() string {
	return "announcements"
}

func (a *Announcement) Publish() {
	a.Published = true
	a.PublishedAt = &time.Time{}
}

func (a *Announcement) UnPublish() {
	a.Published = false
	a.PublishedAt = nil
}
