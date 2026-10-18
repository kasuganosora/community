package scripts

import "gorm.io/gorm"

// AccountRequireSigninScript adds accounts.require_signin_to_view (default
// false): the per-account "require sign-in to view my content" setting.
type AccountRequireSigninScript struct{}

type accountRequireSigninColumn struct {
	RequireSigninToView bool `gorm:"not null;default:false"`
}

func (accountRequireSigninColumn) TableName() string { return "accounts" }

func (AccountRequireSigninScript) Up(conn *gorm.DB) error {
	if conn.Migrator().HasColumn(&accountRequireSigninColumn{}, "require_signin_to_view") {
		return nil
	}
	return conn.Migrator().AddColumn(&accountRequireSigninColumn{}, "RequireSigninToView")
}

func (AccountRequireSigninScript) Down(conn *gorm.DB) error {
	if !conn.Migrator().HasColumn(&accountRequireSigninColumn{}, "require_signin_to_view") {
		return nil
	}
	return conn.Migrator().DropColumn(&accountRequireSigninColumn{}, "RequireSigninToView")
}
