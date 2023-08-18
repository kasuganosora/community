package models

import "time"

type Marker struct {
	AccountId uint64                         `gorm:"primary_key;auto_increment:false"`
	Timelines map[string]*MarkerTimelineItem `gorm:"type:json; serialize:json"`
}

func (m *Marker) TableName() string {
	return "markers"
}

type MarkerTimelineItem struct {
	LastReadId uint64     `json:"last_read_id"`
	UpdatedAt  *time.Time `json:"updated_at"`
	Version    uint64     `json:"version"`
}
