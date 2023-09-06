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
	"regexp"
	"strings"
	"time"
)

type Account struct {
	Id              uint64     `json:"id" gorm:"primary_key;type:bigint(20);autoIncrement"`
	UserId          uint64     `json:"-" gorm:"not null;type:bigint(20);index"` // 创建这个账号的用户ID,如果是远程账号则为 null
	Username        string     `json:"username" gorm:"not null;type:varchar(64)"`
	Domain          *string    `json:"domain" gorm:"type:varchar(128);index"`        // 远程账号的域名,如果是本地账号则为 null
	OriginId        string     `json:"origin_id" gorm:"type:varchar(64)"`            // 远程账号的ID,如果是本地账号则为 null
	Acct            string     `json:"acct" gorm:"not null;type:varchar(128);index"` // 远程账号的全局唯一ID,如果是本地账号则为 username@domain
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
	LastActivityAt  *time.Time `json:"last_activity_at" gorm:"type:datetime"`
	IsBan           bool       `json:"is_ban" gorm:"default:false"`
	DeletedAt       gorm.DeletedAt
	Emojis          []*Emoji      `json:"-" gorm:"many2many:account_emojis;"`
	Extra           *AccountExtra `json:"-"`

	User *User `json:"-" gorm:"foreignKey:UserId;references:Id"`
}

func (Account) TableName() string {
	return "accounts"
}

func (a *Account) BeforeSave(tx *gorm.DB) (err error) {
	var domain string
	if a.Domain != nil {
		domain = *a.Domain
	} else {
		domain = "__LOCAL__"
	}

	a.Acct = a.Username + "@" + domain
	return
}

func (a *Account) BeforeCreate(tx *gorm.DB) (err error) {
	if a.IsBan {
		a.Locked = true
	}

	return
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

func (a Account) IsLocal() bool {
	return a.Domain == nil
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

type Accounts []*Account

func (a Accounts) GetIds() []uint64 {
	var ids []uint64
	for _, account := range a {
		ids = append(ids, account.Id)
	}
	return ids
}

func (a Accounts) Contains(id uint64) bool {
	for _, account := range a {
		if account.Id == id {
			return true
		}
	}
	return false
}

func GetLocalAccounts(db *gorm.DB, accountsOrAccountId any) (ret Accounts, err error) {
	ret = make(Accounts, 0)
	if accountsOrAccountId == nil {
		return
	}

	if accounts, ok := accountsOrAccountId.(Accounts); ok {
		for _, account := range accounts {
			if account.Domain == nil {
				ret = append(ret, account)
			}
		}
		return
	}

	if accountsId, ok := accountsOrAccountId.([]uint64); ok {
		err = db.Where("id IN ? and domain is null", accountsId).Find(&ret).Error
		return
	}

	err = errors.New("invalid accountsOrAccountId type, must be Accounts or []uint64")
	return

}

func GetAccountsByText(db *gorm.DB, text string, limit int) (ret map[string]*Account, err error) {
	accountsStr := MatchAccountTags(text)
	if len(accountsStr) == 0 {
		return
	}

	return FindAccountByAccountTag(db, accountsStr, limit)
}

func MatchAccountTags(text string) []string {
	regex := `@(\S+)`
	return utils.FilterDuplicateAndEmptyString(regexp.MustCompile(regex).FindAllString(text, -1))
}

func FindAccountByAccountTag(db *gorm.DB, accountsStr []string, limit int) (ret map[string]*Account, err error) {
	accounts := make(Accounts, 0)
	localUsers := make([]string, 0)
	remoteUsers := make([]string, 0)

	// 分类那些是本地账号，那些是远程账号
	for _, accountStr := range accountsStr {
		parts := strings.Split(accountStr, "@")
		if len(parts) > 2 {
			continue
		}

		if len(parts) == 1 {
			localUsers = append(localUsers, parts[0])
		} else {
			remoteUsers = append(remoteUsers, parts[0]+"@"+parts[1])
		}
	}

	query := db.Model(&Account{})

	searchAccs := make([]string, 0)

	if len(localUsers) > 0 {
		for _, localUser := range localUsers {
			searchAccs = append(searchAccs, localUser+"@__LOCAL__")
		}
	}

	if len(remoteUsers) > 0 {
		searchAccs = append(searchAccs, remoteUsers...)
	}

	query = query.Where("acct IN ?", searchAccs)

	if limit > 0 {
		query = query.Limit(limit)
	}

	err = query.Find(&accounts).Error
	if err != nil {
		err = errors.Wrap(err, "get accounts error")
		return
	}

	ret = make(map[string]*Account)
	for _, account := range accounts {
		if account.Domain == nil {
			ret[account.Username] = account
		} else {
			ret[account.Username+"@"+*account.Domain] = account
		}
	}
	return
}
