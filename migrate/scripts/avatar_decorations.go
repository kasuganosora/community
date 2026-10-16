package scripts

import (
	"time"

	"gorm.io/gorm"
)

// AvatarDecorationsScript adds the Misskey-compatible decoration list
// (accounts.avatar_decorations, nullable JSON text) and the remote fetch
// timestamp. Both columns are nullable so the ALTER is cheap on a populated
// table on PostgreSQL, MySQL and SQLite. Existing avatar_decoration_id
// selections keep working: the application reads them as a one-item list
// until the user saves again. Idempotent.
type AvatarDecorationsScript struct{}

type avatarDecorationsAccountRow struct {
	AvatarDecorations          *string    `gorm:"type:text"`
	AvatarDecorationsFetchedAt *time.Time `gorm:""`
}

func (avatarDecorationsAccountRow) TableName() string { return "accounts" }

func (AvatarDecorationsScript) Up(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable("accounts") {
		return nil
	}
	for _, column := range []string{"avatar_decorations", "avatar_decorations_fetched_at"} {
		if m.HasColumn(&avatarDecorationsAccountRow{}, column) {
			continue
		}
		if err := m.AddColumn(&avatarDecorationsAccountRow{}, column); err != nil {
			return err
		}
	}
	return nil
}

func (AvatarDecorationsScript) Down(*gorm.DB) error { return nil }
