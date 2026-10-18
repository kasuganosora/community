package models

import "time"

// AccountDeletion survives process restarts. The account ID is retained as a
// tombstone so deleted identities cannot be claimed by somebody else.
type AccountDeletion struct {
	OutboxCursor   uint64     `gorm:"not null;default:0" json:"-"`
	DeliveryCursor uint64     `gorm:"not null;default:0" json:"-"`
	LeaseToken     string     `gorm:"size:36" json:"-"`
	AccountID      uint64     `gorm:"primaryKey" json:"-"`
	UserID         uint64     `gorm:"not null" json:"-"`
	State          string     `gorm:"size:24;not null;index" json:"state"`
	Attempts       int        `json:"-"`
	LastError      string     `gorm:"type:text" json:"-"`
	NextAttemptAt  *time.Time `gorm:"index" json:"-"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (AccountDeletion) TableName() string { return "account_deletions" }
