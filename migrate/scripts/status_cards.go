package scripts

import (
	"time"

	"gorm.io/gorm"
)

// StatusCardsScript creates status_cards (link previews). Snapshot of
// models.StatusCard, so later model edits cannot change this migration.
// Idempotent and portable.
type StatusCardsScript struct{}

type statusCardRow struct {
	Id           uint64     `gorm:"primaryKey;autoIncrement"`
	StatusId     uint64     `gorm:"not null;uniqueIndex"`
	Url          string     `gorm:"type:varchar(2048);not null"`
	UrlHash      string     `gorm:"type:varchar(64);not null;index"`
	Title        string     `gorm:"type:varchar(512);not null;default:''"`
	Description  string     `gorm:"type:text"`
	ImageUrl     string     `gorm:"type:varchar(1024);not null;default:''"`
	ImageWidth   int        `gorm:"not null;default:0"`
	ImageHeight  int        `gorm:"not null;default:0"`
	CardType     string     `gorm:"type:varchar(16);not null;default:'link'"`
	AuthorName   string     `gorm:"type:varchar(255);not null;default:''"`
	AuthorUrl    string     `gorm:"type:varchar(1024);not null;default:''"`
	ProviderName string     `gorm:"type:varchar(255);not null;default:''"`
	ProviderUrl  string     `gorm:"type:varchar(1024);not null;default:''"`
	CreatedAt    *time.Time `gorm:"autoCreateTime;index"`
}

func (statusCardRow) TableName() string { return "status_cards" }

func (StatusCardsScript) Up(db *gorm.DB) error { return db.AutoMigrate(&statusCardRow{}) }

func (StatusCardsScript) Down(*gorm.DB) error { return nil }
