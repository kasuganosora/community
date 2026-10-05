package scripts

import (
	"fmt"
	"time"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// FollowedTagsScript creates the table the followed-tag API already queries.
type FollowedTagsScript struct{}

type followedTagRow struct {
	Id        uint64     `gorm:"primaryKey;autoIncrement"`
	AccountId uint64     `gorm:"not null;uniqueIndex:uidx_followed_tag"`
	TagId     uint64     `gorm:"not null;uniqueIndex:uidx_followed_tag;index"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime"`
}

func (followedTagRow) TableName() string { return "followed_tags" }

func (FollowedTagsScript) Up(db *gorm.DB) error {
	return db.AutoMigrate(&followedTagRow{})
}

func (FollowedTagsScript) Down(db *gorm.DB) error {
	return db.Migrator().DropTable(&followedTagRow{})
}

// QuoteStatusScript adds statuses.quote_id without a foreign key, so deleting
// the quoted post does not fail.
type QuoteStatusScript struct{}

func (QuoteStatusScript) Up(db *gorm.DB) error {
	if db.Migrator().HasColumn(&models.Status{}, "QuoteId") {
		return nil
	}
	return db.Migrator().AddColumn(&models.Status{}, "QuoteId")
}

func (QuoteStatusScript) Down(db *gorm.DB) error {
	if !db.Migrator().HasColumn(&models.Status{}, "QuoteId") {
		return nil
	}
	if err := db.Migrator().DropColumn(&models.Status{}, "QuoteId"); err != nil {
		return fmt.Errorf("drop quote_id: %w", err)
	}
	return nil
}
