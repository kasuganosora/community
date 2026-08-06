package models

import (
	"crypto/md5" //nolint:gosec // G501: reviewed and safe
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

type Account struct { //nolint:recvcheck
	Id              uint64     `gorm:"primary_key;type:bigint(20);autoIncrement" json:"id"`
	UserId          uint64     `gorm:"not null;type:bigint(20);index"            json:"-"` // 创建这个账号的用户ID,如果是远程账号则为 null
	Username        string     `gorm:"not null;type:varchar(64)"                 json:"username"`
	Domain          *string    `gorm:"type:varchar(128);index"                   json:"domain"`    // 远程账号的域名,如果是本地账号则为 null
	OriginId        string     `gorm:"type:varchar(64)"                          json:"origin_id"` // 远程账号的ID,如果是本地账号则为 null
	Acct            string     `gorm:"not null;type:varchar(128);index"          json:"acct"`      // 远程账号的全局唯一ID,如果是本地账号则为 username@domain
	DisplayName     string     `gorm:"not null;type:varchar(128)"                json:"display_name"`
	Avatar          string     `gorm:"type:varchar(1024)"                        json:"avatar"`
	AvatarStatic    string     `gorm:"type:varchar(1024)"                        json:"avatar_static"`
	Header          string     `gorm:"type:varchar(1024)"                        json:"header"`
	HeaderStatic    string     `gorm:"type:varchar(1024)"                        json:"header_static"`
	Locked          bool       `gorm:"default:false"                             json:"locked"`
	Bot             bool       `gorm:"default:false"                             json:"bot"`
	Discoverable    bool       `gorm:"default:false"                             json:"discoverable"`
	Group           bool       `gorm:"default:false"                             json:"group"`
	NoIndex         bool       `gorm:"default:false"                             json:"noindex"`
	HideCollections bool       `gorm:"default:false"                             json:"hide_collections"`
	Note            string     `gorm:"type:varchar(1024)"                        json:"note"`
	Website         string     `gorm:"type:varchar(1024)"                        json:"website"`
	CreatedAt       *time.Time `gorm:"autoCreateTime"                            json:"created_at"`
	UpdatedAt       *time.Time `gorm:"autoUpdateTime"                            json:"updated_at"`
	LastStatusAt    *time.Time `gorm:"type:datetime"                             json:"last_status_at"`
	LastActivityAt  *time.Time `gorm:"type:datetime"                             json:"last_activity_at"`
	IsBan           bool       `gorm:"default:false"                             json:"is_ban"`
	DeletedAt       gorm.DeletedAt
	Emojis          []*Emoji      `gorm:"many2many:account_emojis;" json:"-"`
	Extra           *AccountExtra `json:"-"`

	User *User `gorm:"foreignKey:UserId;references:Id" json:"-"`
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
	// Use FirstOrCreate to avoid duplicate key errors when AfterCreate is triggered
	// by upserts (ON DUPLICATE KEY UPDATE) on existing accounts.
	a.Extra = &AccountExtra{
		AccountId: a.Id,
	}

	// Check if extra already exists before creating
	existing := &AccountExtra{}
	result := tx.Where("account_id = ?", a.Id).First(existing)
	if result.Error == nil {
		// Already exists, don't create again
		return
	}

	// 记录不存在则继续创建；其他真实数据库错误直接返回，避免掩盖底层错误。
	if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		err = result.Error

		return
	}

	err = tx.Create(a.Extra).Error

	return
}

func (a *Account) SaveAvatar(avatarFilePath string) (err error) {
	if !utils.CheckIsImage(avatarFilePath) {
		err = errors.New("avatar file is not image")

		return
	}

	avatarData, err := os.ReadFile(avatarFilePath) //nolint:gosec // G304: reviewed and safe
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

func (a *Account) IsLocal() bool {
	return a.Domain == nil
}

type AccountExtra struct {
	AccountId      uint64         `gorm:"primary_key;type:varchar(64);"   json:"account_id"`
	FollowersCount int            `gorm:"not null;type:int(11);default:0" json:"followers_count"`
	FollowingCount int            `gorm:"not null;type:int(11);default:0" json:"following_count"`
	StatusesCount  int            `gorm:"not null;type:int(11);default:0" json:"statuses_count"`
	Fields         []AccountField `gorm:"type:json;serializer:json"       json:"fields"`
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

	b := md5.Sum([]byte(raw)) //nolint:gosec // G401: reviewed and safe

	return hex.EncodeToString(b[:])
}

type AccountEmoji struct {
	Id        int64 `gorm:"primary_key;type:bigint(20);autoIncrement" json:"id"`
	AccountId int64 `gorm:"not null;type:varchar(64);index"           json:"account_id"`
	EmojiId   int64 `gorm:"not null;type:bigint(20);index"            json:"emoji_id"`
}

func (AccountEmoji) TableName() string {
	return "account_emojis"
}

type Accounts []*Account

func (a Accounts) GetIds() []uint64 {
	ids := make([]uint64, 0, len(a))
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
	// 注意：MatchAccountTags 返回的标签带有前导 "@"（例如 "@user" 或 "@user@domain"），
	// 这里需要先去掉前导 "@" 再按 "@" 拆分。
	for _, accountStr := range accountsStr {
		accountStr = strings.TrimPrefix(accountStr, "@")
		if accountStr == "" {
			continue
		}

		parts := strings.Split(accountStr, "@")
		// 去掉前导 "@" 后，合法的格式只能是 "user" 或 "user@domain"
		if len(parts) == 0 || len(parts) > 2 {
			continue
		}

		if len(parts) == 1 {
			if parts[0] != "" {
				localUsers = append(localUsers, parts[0])
			}
		} else {
			if parts[0] != "" && parts[1] != "" {
				remoteUsers = append(remoteUsers, parts[0]+"@"+parts[1])
			}
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

		return ret, err
	}

	ret = make(map[string]*Account)

	for _, account := range accounts {
		if account.Domain == nil {
			ret[account.Username] = account
		} else {
			ret[account.Username+"@"+*account.Domain] = account
		}
	}

	return ret, err
}
