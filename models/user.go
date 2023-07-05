package models

import (
	"crypto/md5"
	"encoding/base64"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"reflect"
)

const (
	UserStatusNormal = iota
	UserStatusUnapproved
	UserStatusRejected
	UserStatusUnverifiedMail // 未验证邮箱
)

type User struct {
	Id             int64          `json:"id" gorm:"primary_key;autoIncrement"`
	UUID           string         `json:"uuid" gorm:"unique;not null;type:varchar(36)"`                     // UUID
	UserName       string         `json:"username" gorm:"unique;column:username;not null;type:varchar(20)"` // 用户名
	IsBan          bool           `json:"is_ban" gorm:"default:false"`                                      // 是否被封禁
	IsAdmin        bool           `json:"is_admin" gorm:"default:false"`                                    // 是否是管理员
	EmailHash      string         `json:"email_hash" gorm:"type:varchar(255); unique;not null"`             // 邮箱的 hash 值 用于 email登录,和测试是否是同一个邮箱
	CreatedAt      int64          `gorm:"autoCreateTime"`                                                   // 创建时间
	UpdatedAt      int64          `gorm:"autoUpdateTime"`                                                   // 更新时间
	InviteBy       int64          `json:"invite_by" gorm:"default:0"`                                       // 邀请人ID
	Status         int            `json:"status" gorm:"default:0"`                                          // 用户状态 0:正常 1:未审批, 2:被拒绝,3: 未验证邮箱
	Properties     map[string]any `json:"properties" gorm:"type:json;serializer;json"`                      // 用户的属性
	MailVerifyCode *string        `json:"-" gorm:"type:varchar(128);uniqueIndex"`                           // 邮箱验证的验证码
	IsMailVerified bool           `json:"-" gorm:"default:false"`                                           // 邮箱是否验证
	gorm.DeletedAt
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

// UserSecretData 保存用户的敏感数据，如密码，邮箱等 里面的所有数据在保存时候都会加密
type UserSecretData struct {
	UserId          int64          `json:"user_id" gorm:"primary_key, column:user_id"`
	Salt            string         `json:"salt" gorm:"type:varchar(255);not null"`
	Password        string         `json:"password" gorm:"type:varchar(255);not null"` // 密码不需要加密, 因为入库的时候进行过不可逆加密
	Email           string         `json:"email" gorm:"type:varchar(255);not null" encrypt:"true"`
	Phone           string         `json:"phone" gorm:"type:varchar(255);not null" encrypt:"true"`
	originAttribute map[string]any `gorm:"-"`
}

func (UserSecretData) TableName() string {
	return "user_secret_data"
}

// AfterFind 设置 AfterFind UserSecretData 对象查询时候的钩子函数
// 用于查询出来的时候把数据存到 originAttribute 里面，用于保存时候判断是否需要加密
func (u *UserSecretData) AfterFind(tx *gorm.DB) (err error) {
	u.originAttribute = make(map[string]any)
	elm := reflect.ValueOf(u).Elem()
	eType := elm.Type()
	for i := 0; i < eType.NumField(); i++ {
		field := eType.Field(i)
		if field.Name == "originAttribute" {
			continue
		}

		fieldValue := elm.Field(i)
		u.originAttribute[field.Name] = fieldValue.Interface()
	}

	return
}

// BeforeSave 设置 BeforeSave UserSecretData 对象保存时候的钩子函数
func (u *UserSecretData) BeforeSave(tx *gorm.DB) (err error) {
	// 如果没有设置加密密钥则不加密
	if config.C.EncryptedKey == "" {
		return
	}

	// 每次都把上次都解密的数据重新加密
	key := utils.GetEncryptKey(config.C.EncryptedKey, u.Salt)
	elm := reflect.ValueOf(u).Elem()
	eType := elm.Type()

	for i := 0; i < eType.NumField(); i++ {
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
		if oldValue, ok := u.originAttribute[field.Name]; ok && oldValue.(string) == fieldValue.String() {
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

	return
}

// AfterSave 设置 AfterSave UserSecretData 对象保存时候的钩子函数
// 刷新 originAttribute 里面的数据
func (u *UserSecretData) AfterSave(tx *gorm.DB) (err error) {
	u.originAttribute = make(map[string]any)
	u.originAttribute = make(map[string]any)
	elm := reflect.ValueOf(u).Elem()
	eType := elm.Type()
	for i := 0; i < eType.NumField(); i++ {
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
	pBytes := md5.Sum([]byte(password))
	// 使用 bcrypt 加密
	hash, err := bcrypt.GenerateFromPassword(pBytes[:], bcrypt.DefaultCost)
	if err != nil {
		return
	}

	ret = base64.StdEncoding.EncodeToString(hash)
	return
}
