package scripts

import (
	"time"

	"gorm.io/gorm"
)

// PollCloseNotifiedScript adds polls.closed_notified_at, the idempotency
// marker of the poll-close worker (poll notifications + closing Update).
//
// Each step is explicit and idempotent, so the script is safe to rerun after
// a partial failure and on any supported database:
//  1. add the column if it is missing,
//  2. create idx_polls_closed_notified_at if it is missing,
//  3. mark polls that had already ended so they are not notified again.
type PollCloseNotifiedScript struct{}

const pollCloseNotifiedIndex = "idx_polls_closed_notified_at"

type pollCloseNotifiedRow struct {
	ClosedNotifiedAt *time.Time `gorm:"column:closed_notified_at;index:idx_polls_closed_notified_at"`
}

func (pollCloseNotifiedRow) TableName() string { return "polls" }

func (PollCloseNotifiedScript) Up(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable("polls") {
		return nil
	}
	added := false
	if !m.HasColumn(&pollCloseNotifiedRow{}, "closed_notified_at") {
		// Field name, not column name: GORM looks fields up by Go name.
		if err := m.AddColumn(&pollCloseNotifiedRow{}, "ClosedNotifiedAt"); err != nil {
			return err
		}
		added = true
	}
	if !m.HasIndex(&pollCloseNotifiedRow{}, pollCloseNotifiedIndex) {
		// CreateIndex resolves the index by its tag name.
		if err := m.CreateIndex(&pollCloseNotifiedRow{}, pollCloseNotifiedIndex); err != nil {
			return err
		}
		// Finishing an interrupted run (MySQL DDL is not transactional).
		added = true
	}
	if !added {
		return nil
	}
	now := time.Now()
	return db.Table("polls").Where("closed_notified_at IS NULL AND expires_at IS NOT NULL AND expires_at <= ?", now).Update("closed_notified_at", now).Error
}

func (PollCloseNotifiedScript) Down(*gorm.DB) error { return nil }
