package scripts

import (
	"time"

	"gorm.io/gorm"
)

// InboxJobsScript creates activitypub_inbox_jobs, the queue of verified
// inbound activities. Snapshot of activitypub.InboxJob. Idempotent and
// portable.
type InboxJobsScript struct{}

type inboxJobRow struct {
	ID              uint64     `gorm:"primaryKey;autoIncrement"`
	ActivityID      string     `gorm:"type:varchar(1024);not null;uniqueIndex:idx_inbox_jobs_activity,length:191"`
	ActivityType    string     `gorm:"type:varchar(64);not null;default:''"`
	Actor           string     `gorm:"type:varchar(1024);not null"`
	TargetAccountID *uint64    `gorm:""`
	RelayID         *uint64    `gorm:""`
	Body            string     `gorm:"size:1048577;not null"`
	Shard           int        `gorm:"not null;default:0"`
	State           string     `gorm:"type:varchar(16);not null;default:'pending';index:idx_inbox_jobs_due,priority:1"`
	NextAttemptAt   time.Time  `gorm:"not null;index:idx_inbox_jobs_due,priority:2"`
	LeaseUntil      *time.Time `gorm:""`
	Attempts        int        `gorm:"not null;default:0"`
	LastStatus      int        `gorm:"not null;default:0"`
	LastError       string     `gorm:"type:text"`
	CreatedAt       *time.Time `gorm:"autoCreateTime"`
}

func (inboxJobRow) TableName() string { return "activitypub_inbox_jobs" }

func (InboxJobsScript) Up(db *gorm.DB) error { return db.AutoMigrate(&inboxJobRow{}) }

func (InboxJobsScript) Down(*gorm.DB) error { return nil }
