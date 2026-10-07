package models

import "time"

// AvatarDecoration stores only validated, instance-hosted images. No remote
// tracking assets or script-bearing SVG may be supplied as URLs.
type AvatarDecoration struct {
	ID        uint64    `gorm:"primaryKey" json:"id,string"`
	Name      string    `gorm:"size:80;not null" json:"name"`
	ImageURL  string    `gorm:"size:2048;not null" json:"image_url"`
	Enabled   bool      `gorm:"not null;default:false" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Advertisement struct {
	ID        uint64     `gorm:"primaryKey" json:"id,string"`
	Title     string     `gorm:"size:120;not null" json:"title"`
	ImageURL  string     `gorm:"size:2048;not null" json:"image_url"`
	TargetURL string     `gorm:"size:2048;not null" json:"target_url"`
	Enabled   bool       `gorm:"not null;default:false;index" json:"enabled"`
	StartsAt  *time.Time `json:"starts_at"`
	EndsAt    *time.Time `json:"ends_at"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}
