package models

// EmojiUpload binds a validated asset to the administrator who uploaded it.
type EmojiUpload struct {
	Id          string `gorm:"primaryKey;size:64" json:"id"`
	AccountId   uint64 `gorm:"not null;index" json:"-"`
	Url         string `gorm:"size:2048;not null" json:"url"`
	StaticUrl   string `gorm:"size:2048;not null" json:"static_url"`
	ContentType string `gorm:"size:100" json:"content_type"`
	FileSize    int64  `json:"total_file_size"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	CreatedAt   int64  `gorm:"autoCreateTime" json:"-"`
}
