package scripts

import (
	"time"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// MissingAppTablesScript creates the tables application code uses but no
// earlier migration created, found by running the whole chain on a fresh
// database and comparing every GORM model with the resulting schema
// (migrate/schema_test.go keeps that comparison as a regression test):
//
//   - featured_tags     (/api/v1/featured_tags, /api/v1/accounts/:id/featured_tags)
//   - endorsements      (/api/v1/endorsements, /api/v1/accounts/:id/pin)
//   - push_subscriptions (/api/v1/push/subscription)
//   - email_domain_blocks (tootctl email-domain-blocks)
//   - thumbnails, thumbnail_jobs, thumbnail_stats, thumbnail_configs
//     (admin thumbnail API)
//
// The row structs below are snapshots of the models at the time of this
// migration (like followedTagRow), so later model edits cannot change what
// this migration does. Like followed_tags and status_tags, the link tables
// have no foreign keys; they get the unique keys the code relies on.
// featured_tags rows that already exist (an engine where the table was
// created by hand) are deduplicated per (account_id, tag_id) and stripped
// of links to deleted tags before the unique index is added.
//
// Idempotent and portable (SQLite, MySQL, PostgreSQL).
type MissingAppTablesScript struct{}

type featuredTagRow struct {
	Id        uint64     `gorm:"primaryKey;autoIncrement"`
	AccountId uint64     `gorm:"not null;index;uniqueIndex:uidx_featured_tags_account_tag,priority:1"`
	TagId     uint64     `gorm:"not null;index;uniqueIndex:uidx_featured_tags_account_tag,priority:2"`
	Name      string     `gorm:"type:varchar(64);not null"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime"`
}

func (featuredTagRow) TableName() string { return "featured_tags" }

type featuredTagCreatedAt struct {
	CreatedAt *time.Time
}

func (featuredTagCreatedAt) TableName() string { return "featured_tags" }

type endorsementRow struct {
	Id        uint64 `gorm:"primaryKey;autoIncrement"`
	AccountId uint64 `gorm:"not null;index;uniqueIndex:uidx_endorsements_account_target,priority:1"`
	TargetId  uint64 `gorm:"not null;index;uniqueIndex:uidx_endorsements_account_target,priority:2"`
}

func (endorsementRow) TableName() string { return "endorsements" }

type pushSubscriptionRow struct {
	Id        uint64     `gorm:"primaryKey;autoIncrement"`
	AccountId uint64     `gorm:"not null;index"`
	Endpoint  string     `gorm:"type:text;not null"`
	AuthKey   string     `gorm:"type:varchar(64)"`
	P256dhKey string     `gorm:"type:varchar(128)"`
	Alerts    string     `gorm:"type:json"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime"`
	UpdatedAt *time.Time `gorm:"not null;autoUpdateTime"`
}

func (pushSubscriptionRow) TableName() string { return "push_subscriptions" }

type emailDomainBlockRow struct {
	ID        uint64     `gorm:"primaryKey;autoIncrement"`
	Domain    string     `gorm:"unique;not null;type:varchar(255)"`
	CreatedAt *time.Time `gorm:"autoCreateTime"`
}

func (emailDomainBlockRow) TableName() string { return "email_domain_blocks" }

func (MissingAppTablesScript) Up(db *gorm.DB) error {
	m := db.Migrator()
	if m.HasTable("featured_tags") {
		if m.HasTable("tags") {
			if err := db.Exec("DELETE FROM featured_tags WHERE NOT EXISTS (SELECT 1 FROM tags WHERE tags.id = featured_tags.tag_id)").Error; err != nil {
				return err
			}
		}
		if _, err := dedupeLinks(db, "featured_tags", "account_id"); err != nil {
			return err
		}
		// A hand-made table may lack created_at: add it nullable, fill it,
		// and let AutoMigrate below make it NOT NULL.
		if !m.HasColumn("featured_tags", "created_at") {
			if err := m.AddColumn(&featuredTagCreatedAt{}, "CreatedAt"); err != nil {
				return err
			}
			if err := db.Table("featured_tags").Where("created_at IS NULL").Update("created_at", time.Now()).Error; err != nil {
				return err
			}
		}
	}
	if m.HasTable("endorsements") {
		if err := dedupeRows(db, "endorsements", "account_id", "target_id"); err != nil {
			return err
		}
	}
	return db.AutoMigrate(
		&featuredTagRow{},
		&endorsementRow{},
		&pushSubscriptionRow{},
		&emailDomainBlockRow{},
		&models.Thumbnail{},
		&models.ThumbnailJob{},
		&models.ThumbnailStats{},
		&models.ThumbnailConfig{},
	)
}

// dedupeRows deletes all but the oldest row of every (a, b) pair in table.
func dedupeRows(db *gorm.DB, table, a, b string) error {
	var groups []struct {
		A, B   uint64
		KeepId uint64
	}
	if err := db.Table(table).Select(a + " AS a, " + b + " AS b, MIN(id) AS keep_id").
		Group(a + ", " + b).Having("COUNT(*) > 1").Scan(&groups).Error; err != nil {
		return err
	}
	for _, g := range groups {
		if err := db.Exec("DELETE FROM "+table+" WHERE "+a+" = ? AND "+b+" = ? AND id <> ?", g.A, g.B, g.KeepId).Error; err != nil {
			return err
		}
	}
	return nil
}

// Down keeps the tables: dropping them would delete user data, and older
// code tolerated (or failed on) their absence anyway.
func (MissingAppTablesScript) Down(*gorm.DB) error { return nil }
