package models

import (
	"encoding/base64"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"reflect"
	"time"
)

const (
	UserStatusNormal = iota
	UserStatusUnapproved
	UserStatusRejected
	UserStatusUnverifiedMail // 未验证邮箱
)

type User struct { //nolint:recvcheck
	Id             uint64          `gorm:"primary_key;autoIncrement"                        json:"id"`
	UUID           string          `gorm:"unique;not null;type:varchar(36)"                 json:"uuid"`       // UUID
	UserName       string          `gorm:"unique;column:username;not null;type:varchar(20)" json:"username"`   // 用户名
	IsBan          bool            `gorm:"default:false"                                    json:"is_ban"`     // 是否被封禁
	IsAdmin        bool            `gorm:"default:false"                                    json:"is_admin"`   // 是否是管理员
	EmailHash      string          `gorm:"type:varchar(255); unique;not null"               json:"email_hash"` // 邮箱的 hash 值 用于 email登录,和测试是否是同一个邮箱
	CreatedAt      *time.Time      `gorm:"autoCreateTime"`                                                     // 创建时间
	UpdatedAt      *time.Time      `gorm:"autoUpdateTime"`                                                     // 更新时间
	InviteBy       uint64          `gorm:"default:0"                                        json:"invite_by"`  // 邀请人ID
	Status         int             `gorm:"default:0"                                        json:"status"`     // 用户状态 0:正常 1:未审批, 2:被拒绝,3: 未验证邮箱
	Properties     map[string]any  `gorm:"type:json;serializer:json"                        json:"properties"` // 用户的属性
	IsMailVerified bool            `gorm:"default:false"                                    json:"-"`          // 邮箱是否验证
	UserSecret     *UserSecretData `json:"-"`                                                                  // 用户的敏感数据
	UserMeta       []*UserMeta     `json:"-"`                                                                  // 用户的元数据
	DeletedAt      gorm.DeletedAt  `gorm:"index"                                            json:"-"`
	Settings       *UserSetting    `json:"-"` // 用户的设置
}

func (User) TableName() string {
	return "users"
}

func (u *User) BeforeCreate(tx *gorm.DB) (err error) {
	if u.UUID == "" {
		u.UUID = uuid.New().String()
	}

	return
}

func (u *User) GetAccount(db *gorm.DB) (account *Account, err error) {
	account = &Account{}
	err = db.Preload("Emojis").
		Preload("Extra").Where("user_id = ?", u.Id).First(account).Error

	return
}

func (u *User) GetAccountBase(db *gorm.DB) (account *Account, err error) {
	account = &Account{}
	err = db.Where("user_id = ?", u.Id).First(account).Error

	return
}

// UserSecretData 保存用户的敏感数据，如密码，邮箱等 里面的所有数据在保存时候都会加密.
type UserSecretData struct { //nolint:recvcheck
	UserId          uint64         `gorm:"primary_key, column:user_id" json:"user_id"`
	Salt            string         `gorm:"type:varchar(255);not null"  json:"salt"`
	Password        string         `gorm:"type:varchar(255);not null"  json:"password"` // 密码不需要加密, 因为入库的时候进行过不可逆加密
	Email           string         `encrypt:"true"                     gorm:"type:varchar(255);not null" json:"email"`
	Phone           string         `encrypt:"true"                     gorm:"type:varchar(255);not null" json:"phone"`
	originAttribute map[string]any `gorm:"-"`
}

func (UserSecretData) TableName() string {
	return "user_secret_data"
}

// AfterFind 设置 AfterFind UserSecretData 对象查询时候的钩子函数
// 用于查询出来的时候把数据存到 originAttribute 里面，用于保存时候判断是否需要加密.
func (u *UserSecretData) AfterFind(tx *gorm.DB) (err error) {
	u.originAttribute = make(map[string]any)
	elm := reflect.ValueOf(u).Elem()

	eType := elm.Type()
	for i := range eType.NumField() {
		field := eType.Field(i)
		if field.Name == "originAttribute" { //nolint:goconst
			continue
		}

		fieldValue := elm.Field(i)
		u.originAttribute[field.Name] = fieldValue.Interface()
	}

	return
}

// BeforeSave 设置 BeforeSave UserSecretData 对象保存时候的钩子函数.
func (u *UserSecretData) BeforeSave(tx *gorm.DB) (err error) {
	// 如果没有设置加密密钥则不加密
	if config.C.EncryptedKey == "" {
		return err
	}

	// 每次都把上次都解密的数据重新加密
	key := utils.GetEncryptKey(config.C.EncryptedKey, u.Salt)
	elm := reflect.ValueOf(u).Elem()
	eType := elm.Type()

	for i := range eType.NumField() {
		field := eType.Field(i)
		if field.Name == "originAttribute" {
			continue
		}
		// 判断是否需要加密
		if field.Tag.Get("encrypt") != "true" || field.Type.Kind() != reflect.String {
			continue
		}

		fieldValue := elm.Field(i)
		// 如果原始数据和当前数据一样则不需要加密
		if oldValue, ok := u.originAttribute[field.Name]; ok && oldValue.(string) == fieldValue.String() { //nolint:forcetypeassert
			continue
		}

		// 加密
		var encrypted string

		encrypted, err = utils.AesEncrypt([]byte(fieldValue.String()), key)
		if err != nil {
			return errors.Wrapf(err, "encrypt %s error", field.Name)
		}

		fieldValue.SetString(encrypted)
	}

	return err
}

// AfterSave 设置 AfterSave UserSecretData 对象保存时候的钩子函数
// 刷新 originAttribute 里面的数据.
func (u *UserSecretData) AfterSave(tx *gorm.DB) (err error) {
	u.originAttribute = make(map[string]any)
	elm := reflect.ValueOf(u).Elem()

	eType := elm.Type()
	for i := range eType.NumField() {
		field := eType.Field(i)
		if field.Name == "originAttribute" {
			continue
		}

		fieldValue := elm.Field(i)
		u.originAttribute[field.Name] = fieldValue.Interface()
	}

	return
}

func EncryptPassword(password string, salt string) (ret string, err error) {
	password = salt + password
	// 直接使用 bcrypt 加密，不经过 MD5 预处理
	// MD5 会降低密码熵，bcrypt 本身已经足够安全
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return
	}

	ret = base64.StdEncoding.EncodeToString(hash)

	return
}

func CompareHashAndPassword(base64PwdHash, password string, salt string) (err error) {
	password = salt + password

	hash, err := base64.StdEncoding.DecodeString(base64PwdHash)
	if err != nil {
		return
	}

	return bcrypt.CompareHashAndPassword(hash, []byte(password))
}
