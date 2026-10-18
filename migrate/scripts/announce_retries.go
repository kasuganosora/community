package scripts

import (
	"time"

	"gorm.io/gorm"
)

// AnnounceRetriesScript creates activitypub_announce_retries (boosts whose
// original object could not be fetched yet). Snapshot of
// activitypub.AnnounceRetry. Idempotent and portable.
type AnnounceRetriesScript struct{}

type announceRetryRow struct {
	ID            uint64     `gorm:"primaryKey;autoIncrement"`
	ActivityID    string     `gorm:"type:varchar(1024);not null;uniqueIndex:idx_announce_retry_activity,length:191"`
	Actor         string     `gorm:"type:varchar(1024);not null"`
	ObjectURI     string     `gorm:"column:object_uri;type:varchar(2048);not null"`
	Published     string     `gorm:"type:varchar(64);not null;default:''"`
	Attempts      int        `gorm:"not null;default:0"`
	NextAttemptAt time.Time  `gorm:"not null;index"`
	LastError     string     `gorm:"type:text"`
	CreatedAt     *time.Time `gorm:"autoCreateTime"`
}

func (announceRetryRow) TableName() string { return "activitypub_announce_retries" }

func (AnnounceRetriesScript) Up(db *gorm.DB) error { return db.AutoMigrate(&announceRetryRow{}) }

func (AnnounceRetriesScript) Down(*gorm.DB) error { return nil }
