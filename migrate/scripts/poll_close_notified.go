package scripts

import (
	"time"

	"gorm.io/gorm"
)

// PollCloseNotifiedScript adds polls.closed_notified_at, the idempotency
// marker of the poll-close worker (poll notifications + closing Update).
// Existing polls that have already ended are marked as notified so the new
// worker does not flood users with notifications about old polls.
type PollCloseNotifiedScript struct{}

type pollCloseNotifiedRow struct {
	ClosedNotifiedAt *time.Time `gorm:"index"`
}

func (pollCloseNotifiedRow) TableName() string { return "polls" }

func (PollCloseNotifiedScript) Up(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable("polls") || m.HasColumn(&pollCloseNotifiedRow{}, "closed_notified_at") {
		return nil
	}
	if err := m.AddColumn(&pollCloseNotifiedRow{}, "closed_notified_at"); err != nil {
		return err
	}
	if !m.HasIndex(&pollCloseNotifiedRow{}, "closed_notified_at") {
		if err := m.CreateIndex(&pollCloseNotifiedRow{}, "closed_notified_at"); err != nil {
			return err
		}
	}
	now := time.Now()
	return db.Table("polls").Where("expires_at IS NOT NULL AND expires_at <= ?", now).Update("closed_notified_at", now).Error
}

func (PollCloseNotifiedScript) Down(*gorm.DB) error { return nil }
