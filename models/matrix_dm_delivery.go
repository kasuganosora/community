package models

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// MatrixDMDelivery is independent of feed/federation delivery acknowledgements.
// It contains identifiers only; the worker reloads committed recipients and content.
type MatrixDMDelivery struct {
	StatusID      uint64    `gorm:"primaryKey;autoIncrement:false"`
	State         string    `gorm:"size:16;index;not null"`
	NextAttemptAt time.Time `gorm:"index;not null"`
	LeaseToken    string    `gorm:"size:36"`
	Attempts      int
	RoomID        string `gorm:"size:255;index"`
	EventID       string `gorm:"size:255;index"`
	LastError     string `gorm:"size:128"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (MatrixDMDelivery) TableName() string { return "matrix_dm_deliveries" }

func QueueMatrixDirectMessage(tx *gorm.DB, status *Status) error {
	if status == nil || status.Visibility != StatusVisibilityDirect || status.ScheduledAt != nil || status.ReblogId != nil {
		return nil
	}
	// Homeserver bootstrapping and isolated model fixtures may precede its tables.
	// The durable startup backfill covers rows committed before migration.
	if !tx.Migrator().HasTable(&MatrixDMDelivery{}) {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&MatrixDMDelivery{StatusID: status.Id, State: "pending", NextAttemptAt: time.Now()}).Error
}
