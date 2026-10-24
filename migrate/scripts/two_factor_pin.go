package scripts

import (
	"github.com/kasuganosora/community/db/dialect"
	"gorm.io/gorm"
)

// TwoFactorSecurityPinScript adds user_two_factors.pin_hash, a bcrypt hash
// of the optional security PIN. Empty string means unset. Idempotent, and
// the column type is portable across PostgreSQL and MySQL (varchar, not
// null, default empty). Existing TOTP secrets are not read or rewritten.
type TwoFactorSecurityPinScript struct{}

type userTwoFactorPinColumn struct {
	PinHash string `gorm:"type:varchar(255);not null;default:''"`
}

func (userTwoFactorPinColumn) TableName() string { return "user_two_factors" }

func (TwoFactorSecurityPinScript) Up(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable("user_two_factors") || m.HasColumn("user_two_factors", "pin_hash") {
		return nil
	}

	return m.AddColumn(&userTwoFactorPinColumn{}, "PinHash")
}

func (TwoFactorSecurityPinScript) Down(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable("user_two_factors") || !m.HasColumn("user_two_factors", "pin_hash") {
		return nil
	}

	// SQLite's GORM migrator rebuilds the table from its parsed DDL and can
	// leave a column that was added later. ALTER TABLE DROP COLUMN is
	// supported by PostgreSQL, MySQL, and SQLite.
	table, col := quoteIdent(db, "user_two_factors"), quoteIdent(db, "pin_hash")

	return db.Exec("ALTER TABLE " + table + " DROP COLUMN " + col).Error
}

func quoteIdent(db *gorm.DB, ident string) string {
	if dialect.IsPostgres(db) || dialect.IsSQLite(db) {
		return `"` + ident + `"`
	}

	return "`" + ident + "`"
}
