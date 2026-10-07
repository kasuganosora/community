package models

import "time"

// ReadAt is compared to UpdatedAt so substantive edits reappear as unread.
type AnnouncementDismissal struct {
	AccountID      uint64    `gorm:"primaryKey;autoIncrement:false"`
	AnnouncementID uint64    `gorm:"primaryKey;autoIncrement:false"`
	ReadAt         time.Time `gorm:"not null"`
}
