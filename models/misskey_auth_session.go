package models

import "time"

// MisskeyAuthSession is a MiAuth session or a legacy /api/auth/session.
//
// MiAuth callbacks redirect to callback?session=<Session>.
// Legacy callbacks redirect to callbackUrl?token=<Session> (the session id,
// not the access token). The access token is returned once from userkey
// or from /api/miauth/:session/check.
type MisskeyAuthSession struct {
	Id            uint64     `gorm:"primaryKey;autoIncrement"`
	Session       string     `gorm:"type:varchar(128);uniqueIndex;not null"`
	Kind          string     `gorm:"type:varchar(16);not null"`
	AppName       string     `gorm:"type:varchar(128)"`
	AppIcon       string     `gorm:"type:varchar(1024)"`
	CallbackURL   string     `gorm:"type:varchar(2048)"`
	Permission    string     `gorm:"type:varchar(1024)"`
	ApplicationId uint64     `gorm:"index"`
	UserId        uint64     `gorm:"index"`
	AccountId     uint64     `gorm:"index"`
	AccessToken   string     `gorm:"type:text"`
	Approved      bool       `gorm:"not null;default:false"`
	Consumed      bool       `gorm:"not null;default:false"`
	ExpiresAt     *time.Time `gorm:"index"`
	CreatedAt     *time.Time `gorm:"autoCreateTime"`
}

func (MisskeyAuthSession) TableName() string {
	return "misskey_auth_sessions"
}
