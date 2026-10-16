package scripts

import "gorm.io/gorm"

// InstanceModerationScript adds instance_metadata.suspension_state
// (Misskey's per-instance delivery suspension used by the admin federation
// page). NOT NULL DEFAULT 'none' so it is safe on a populated table; the
// moderation lists themselves live in system_settings. Idempotent and
// portable (PostgreSQL, MySQL, SQLite).
type InstanceModerationScript struct{}

type instanceSuspensionRow struct {
	SuspensionState string `gorm:"type:varchar(32);not null;default:'none'"`
}

func (instanceSuspensionRow) TableName() string { return "instance_metadata" }

func (InstanceModerationScript) Up(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable("instance_metadata") || m.HasColumn(&instanceSuspensionRow{}, "suspension_state") {
		return nil
	}
	return m.AddColumn(&instanceSuspensionRow{}, "suspension_state")
}

func (InstanceModerationScript) Down(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable("instance_metadata") || !m.HasColumn(&instanceSuspensionRow{}, "suspension_state") {
		return nil
	}
	return m.DropColumn(&instanceSuspensionRow{}, "suspension_state")
}
