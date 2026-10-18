package scripts

import (
	"time"

	"gorm.io/gorm"
)

// TwoFactorScript creates user_two_factors (encrypted TOTP state) and
// user_backup_codes (bcrypt-hashed single-use recovery codes). Snapshot of
// models.UserTwoFactor / models.UserBackupCode. New tables only; no
// existing table is touched.
type TwoFactorScript struct{}

type userTwoFactorRow struct {
	UserId           uint64 `gorm:"primaryKey;autoIncrement:false;column:user_id"`
	Salt             string `gorm:"type:varchar(64);not null;default:''"`
	Secret           string `gorm:"type:text"`
	PendingSecret    string `gorm:"type:text"`
	PendingCreatedAt *time.Time
	EnabledAt        *time.Time
	LastUsedStep     int64 `gorm:"not null;default:0"`
	FailedAttempts   int   `gorm:"not null;default:0"`
	LockedUntil      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (userTwoFactorRow) TableName() string { return "user_two_factors" }

type userBackupCodeRow struct {
	Id        uint64     `gorm:"primaryKey;autoIncrement"`
	UserId    uint64     `gorm:"not null;index:idx_user_backup_codes_user"`
	CodeHash  string     `gorm:"type:varchar(255);not null"`
	UsedAt    *time.Time `gorm:""`
	CreatedAt time.Time
}

func (userBackupCodeRow) TableName() string { return "user_backup_codes" }

func (TwoFactorScript) Up(db *gorm.DB) error {
	return db.AutoMigrate(&userTwoFactorRow{}, &userBackupCodeRow{})
}

func (TwoFactorScript) Down(db *gorm.DB) error {
	return db.Migrator().DropTable("user_backup_codes", "user_two_factors")
}
