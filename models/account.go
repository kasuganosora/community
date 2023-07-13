package models

import (
	"crypto/md5"
	"encoding/hex"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"os"
	"time"
)

type Account struct {
	Id              uint64     `json:"id" gorm:"primary_key;type:bigint(20);autoIncrement"`
	UserId          uint64     `json:"-" gorm:"not null;type:bigint(20);index"` // 创建这个账号的用户ID,如果是远程账号则为 null
	Username        string     `json:"username" gorm:"not null;type:varchar(64)"`
	Domain          string     `json:"domain" gorm:"type:varchar(128)"`   // 远程账号的域名,如果是本地账号则为 null
	OriginId        string     `json:"origin_id" gorm:"type:varchar(64)"` // 远程账号的ID,如果是本地账号则为 null
	DisplayName     string     `json:"display_name" gorm:"not null;type:varchar(128)"`
	Avatar          string     `json:"avatar" gorm:"type:varchar(1024)"`
	AvatarStatic    string     `json:"avatar_static" gorm:"type:varchar(1024)"`
	Header          string     `json:"header" gorm:"type:varchar(1024)"`
	HeaderStatic    string     `json:"header_static" gorm:"type:varchar(1024)"`
	Locked          bool       `json:"locked" gorm:"default:false"`
	Bot             bool       `json:"bot" gorm:"default:false"`
	Discoverable    bool       `json:"discoverable" gorm:"default:false"`
	Group           bool       `json:"group" gorm:"default:false"`
	NoIndex         bool       `json:"noindex" gorm:"default:false"`
	HideCollections bool       `json:"hide_collections" gorm:"default:false"`
	Note            string     `json:"note" gorm:"type:varchar(1024)"`
	Website         string     `json:"website" gorm:"type:varchar(1024)"`
	CreatedAt       *time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt       *time.Time `json:"updated_at" gorm:"autoUpdateTime"`
	LastStatusAt    *time.Time `json:"last_status_at" gorm:"type:datetime"`
	IsBan           bool       `json:"is_ban" gorm:"default:false"`
	DeletedAt       gorm.DeletedAt
	Emojis          []*Emoji      `json:"-" gorm:"many2many:account_emojis;"`
	Extra           *AccountExtra `json:"-"`
}

func (Account) TableName() string {
	return "accounts"
}

func (a *Account) AfterCreate(tx *gorm.DB) (err error) {
	a.Extra = &AccountExtra{
		AccountId: a.Id,
	}

	err = tx.Create(a.Extra).Error
	return
}

func (a *Account) SaveAvatar(avatarFilePath string) (err error) {
	if !utils.CheckIsImage(avatarFilePath) {
		err = errors.New("avatar file is not image")
		return
	}

	avatarData, err := os.ReadFile(avatarFilePath)
	if err != nil {
		err = errors.Wrap(err, "read avatar file error")
		return
	}
	// 保存头像
	avatarSavePath, err := storage.Save(avatarFilePath, avatarData)

	if err != nil {
		err = errors.Wrap(err, "save avatar file error")
		return
	}

	a.Avatar = avatarSavePath

	return
}

type AccountExtra struct {
	AccountId      uint64         `json:"account_id" gorm:"primary_key;type:varchar(64);"`
	FollowersCount int            `json:"followers_count" gorm:"not null;type:int(11);default:0"`
	FollowingCount int            `json:"following_count" gorm:"not null;type:int(11);default:0"`
	StatusesCount  int            `json:"statuses_count" gorm:"not null;type:int(11);default:0"`
	Fields         []AccountField `json:"fields" gorm:"type:json;serializer:json"`
}

func (AccountExtra) TableName() string {
	return "accounts_extra"
}

type AccountField struct {
	Name       string     `json:"name"`
	Val        string     `json:"val"`
	VerifiedAt *time.Time `json:"verified_at"`
}

func GetEmailHash(email string) string {
	raw := config.C.EncryptedKey + ":" + email

	b := md5.Sum([]byte(raw))

	return hex.EncodeToString(b[:])
}

type AccountEmoji struct {
	Id        int64 `json:"id" gorm:"primary_key;type:bigint(20);autoIncrement"`
	AccountId int64 `json:"account_id" gorm:"not null;type:varchar(64);index"`
	EmojiId   int64 `json:"emoji_id" gorm:"not null;type:bigint(20);index"`
}

func (AccountEmoji) TableName() string {
	return "account_emojis"
}
