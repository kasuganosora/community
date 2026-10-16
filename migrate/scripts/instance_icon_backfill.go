package scripts

import (
	"time"

	"gorm.io/gorm"
)

// InstanceIconBackfillScript makes every instance_metadata row due for a
// refresh so the background worker (8 domains/min) re-runs icon discovery
// (Misskey meta, Mastodon instance icon, homepage <link rel=icon>, verified
// favicon.ico) and replaces the previously unverified /favicon.ico guess.
// The icon_url column itself already exists (varchar(1024)); it is added
// here only if missing. Rows inside a failure backoff are left alone.
// Portable across PostgreSQL, MySQL and SQLite; idempotent.
type InstanceIconBackfillScript struct{}

type instanceIconRow struct {
	IconURL string `gorm:"type:varchar(1024);default:''"`
}

func (instanceIconRow) TableName() string { return "instance_metadata" }

func (InstanceIconBackfillScript) Up(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable("instance_metadata") {
		return nil
	}
	if !m.HasColumn(&instanceIconRow{}, "icon_url") {
		if err := m.AddColumn(&instanceIconRow{}, "icon_url"); err != nil {
			return err
		}
	}
	return db.Table("instance_metadata").
		Where("fail_count = 0 AND next_attempt_at > ?", time.Now()).
		Update("next_attempt_at", time.Now()).Error
}

func (InstanceIconBackfillScript) Down(db *gorm.DB) error { return nil }
