package models

import (
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"os"
)

type Account struct {
	Id           string `json:"id" gorm:"primary_key;type:varchar(64);"`
	UserId       int64  `json:"-" gorm:"not null;type:bigint(20);index"` // 创建这个账号的用户ID,如果是远程账号则为 null
	Username     string `json:"username" gorm:"not null;type:varchar(64)"`
	Acct         string `json:"acct" gorm:"not null;type:varchar(128)"`
	DisplayName  string `json:"display_name" gorm:"not null;type:varchar(128)"`
	Avatar       string `json:"avatar" gorm:"type:varchar(1024)"`
	AvatarStatic string `json:"avatar_static" gorm:"type:varchar(1024)"`
	Header       string `json:"header" gorm:"type:varchar(1024)"`
	HeaderStatic string `json:"header_static" gorm:"type:varchar(1024)"`
	Locked       bool   `json:"locked" gorm:"default:false"`
	Bot          bool   `json:"bot" gorm:"default:false"`
	Discoverable bool   `json:"discoverable" gorm:"default:false"`
	Group        bool   `json:"group" gorm:"default:false"`
	NoIndex      bool   `json:"noindex" gorm:"default:false"`
	Note         string `json:"note" gorm:"type:varchar(1024)"`
	CreatedAt    int64  `gorm:"autoCreateTime"`
	UpdatedAt    int64  `gorm:"autoUpdateTime"`
	DeletedAt    gorm.DeletedAt
	Roles        []*Role `json:"roles,omitempty" gorm:"many2many:account_roles;"`
}

func (Account) TableName() string {
	return "accounts"
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
	AccountId      string         `json:"account_id" gorm:"primary_key;type:varchar(64);"`
	FollowersCount int            `json:"followers_count" gorm:"not null;type:int(11);default:0"`
	FollowingCount int            `json:"following_count" gorm:"not null;type:int(11);default:0"`
	StatusesCount  int            `json:"statuses_count" gorm:"not null;type:int(11);default:0"`
	Fields         []AccountField `json:"fields" gorm:"type:json;serializer:json"`
}

func (AccountExtra) TableName() string {
	return "accounts_extra"
}

type AccountField struct {
	Name       string `json:"name"`
	Val        string `json:"val"`
	VerifiedAt int64  `json:"verified_at"`
}

type AccountFull struct {
	Account
	AccountExtra
	Emojis []Emoji `json:"emojis"`
}

type AccountRole struct {
	AccountId string `json:"account_id" gorm:"primary_key;type:varchar(64);"`
	RoleId    int64  `json:"role_id" gorm:"primary_key;type:bigint(20);"`
}

func (AccountRole) TableName() string {
	return "accounts_roles"
}
