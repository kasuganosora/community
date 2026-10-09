package models

import "time"

// StatusCard is the link preview of a status (Mastodon's status.card). One
// row per status; the serialized card is also cached in
// status_contents.card, which statuses already load.
type StatusCard struct {
	Id           uint64     `gorm:"primaryKey;autoIncrement"                     json:"id"`
	StatusId     uint64     `gorm:"not null;uniqueIndex"                         json:"status_id"`
	Url          string     `gorm:"type:varchar(2048);not null"                  json:"url"`
	UrlHash      string     `gorm:"type:varchar(64);not null;index"              json:"-"`
	Title        string     `gorm:"type:varchar(512);not null;default:''"        json:"title"`
	Description  string     `gorm:"type:text"                                    json:"description"`
	ImageUrl     string     `gorm:"type:varchar(1024);not null;default:''"       json:"image_url"`
	ImageWidth   int        `gorm:"not null;default:0"                           json:"width"`
	ImageHeight  int        `gorm:"not null;default:0"                           json:"height"`
	CardType     string     `gorm:"type:varchar(16);not null;default:'link'"     json:"type"`
	AuthorName   string     `gorm:"type:varchar(255);not null;default:''"        json:"author_name"`
	AuthorUrl    string     `gorm:"type:varchar(1024);not null;default:''"       json:"author_url"`
	ProviderName string     `gorm:"type:varchar(255);not null;default:''"        json:"provider_name"`
	ProviderUrl  string     `gorm:"type:varchar(1024);not null;default:''"       json:"provider_url"`
	CreatedAt    *time.Time `gorm:"autoCreateTime;index"                         json:"created_at"`
}

func (StatusCard) TableName() string { return "status_cards" }

// APIMap is the Mastodon API shape of the card.
func (c *StatusCard) APIMap() map[string]any {
	return map[string]any{
		"url": c.Url, "title": c.Title, "description": c.Description, "type": c.CardType,
		"author_name": c.AuthorName, "author_url": c.AuthorUrl,
		"provider_name": c.ProviderName, "provider_url": c.ProviderUrl,
		"html": "", "width": c.ImageWidth, "height": c.ImageHeight,
		"image": nilIfEmpty(c.ImageUrl), "embed_url": "", "blurhash": nil,
	}
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
