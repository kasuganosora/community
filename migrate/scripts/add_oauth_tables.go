package scripts

import (
	"gorm.io/gorm"
	"time"
)

// AddOAuthTables adds OAuth related tables.
type AddOAuthTables struct{}

// OAuthApplication extends Application with OAuth-specific fields for migration.
type OAuthApplicationMigrate struct {
	Id           uint64         `gorm:"primary_key;autoIncrement"`
	Uuid         *string        `gorm:"type:char(36);uniqueIndex;not null"`
	UserId       int64          `gorm:"index"`
	Name         string         `gorm:"type:varchar(32);not null"`
	Website      string         `gorm:"type:varchar(255)"`
	RedirectUri  string         `gorm:"type:varchar(255);not null"`
	Scopes       string         `gorm:"type:varchar(255);not null"`
	ClientId     string         `gorm:"type:varchar(64);uniqueIndex;not null"`
	ClientSecret string         `gorm:"type:varchar(128);not null"`
	IsEnabled    bool           `gorm:"not null;default:true"`
	CreatedAt    *time.Time     `gorm:"autoCreateTime"`
	UpdatedAt    *time.Time     `gorm:"autoUpdateTime"`
	DeletedAt    gorm.DeletedAt `gorm:"index"`
}

func (OAuthApplicationMigrate) TableName() string {
	return "applications"
}

// OAuthAuthorizationCode stores temporary authorization codes.
type OAuthAuthorizationCodeMigrate struct {
	Id            uint64     `gorm:"primary_key;autoIncrement"`
	Code          string     `gorm:"type:varchar(128);uniqueIndex;not null"`
	ApplicationId uint64     `gorm:"not null;index"`
	AccountId     uint64     `gorm:"not null;index"`
	RedirectUri   string     `gorm:"type:varchar(255);not null"`
	Scopes        string     `gorm:"type:varchar(255);not null"`
	ExpiresAt     *time.Time `gorm:"not null"`
	CreatedAt     *time.Time `gorm:"autoCreateTime"`
	Used          bool       `gorm:"default:false"`
}

func (OAuthAuthorizationCodeMigrate) TableName() string {
	return "oauth_authorization_codes"
}

func (s AddOAuthTables) Up(db *gorm.DB) error {
	// Add client_id and client_secret columns to applications table if they don't exist
	if !db.Migrator().HasColumn(&OAuthApplicationMigrate{}, "client_id") {
		err := db.Migrator().AddColumn(&OAuthApplicationMigrate{}, "client_id")
		if err != nil {
			return err
		}
	}

	if !db.Migrator().HasColumn(&OAuthApplicationMigrate{}, "client_secret") {
		err := db.Migrator().AddColumn(&OAuthApplicationMigrate{}, "client_secret")
		if err != nil {
			return err
		}
	}

	// Create oauth_authorization_codes table if it doesn't exist
	if !db.Migrator().HasTable(&OAuthAuthorizationCodeMigrate{}) {
		err := db.Migrator().CreateTable(&OAuthAuthorizationCodeMigrate{})
		if err != nil {
			return err
		}
	}

	return nil
}

func (s AddOAuthTables) Down(db *gorm.DB) error {
	// Drop oauth_authorization_codes table
	if db.Migrator().HasTable(&OAuthAuthorizationCodeMigrate{}) {
		err := db.Migrator().DropTable(&OAuthAuthorizationCodeMigrate{})
		if err != nil {
			return err
		}
	}

	// Remove client_id and client_secret columns from applications table
	if db.Migrator().HasColumn(&OAuthApplicationMigrate{}, "client_id") {
		err := db.Migrator().DropColumn(&OAuthApplicationMigrate{}, "client_id")
		if err != nil {
			return err
		}
	}

	if db.Migrator().HasColumn(&OAuthApplicationMigrate{}, "client_secret") {
		err := db.Migrator().DropColumn(&OAuthApplicationMigrate{}, "client_secret")
		if err != nil {
			return err
		}
	}

	return nil
}
