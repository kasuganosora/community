package models

import (
	"github.com/google/uuid"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"reflect"
)

type User struct {
	ID        int64  `json:"id" gorm:"primary_key;autoIncrement""`
	UUID      string `json:"uuid" gorm:"unique;not null;type:varchar(36)"`     // UUID
	UserName  string `json:"username" gorm:"unique;not null;type:varchar(20)"` // 用户名
	IsBan     bool   `json:"is_ban" gorm:"default:false"`                      // 是否被封禁
	IsAdmin   bool   `json:"is_admin" gorm:"default:false"`                    // 是否是管理员
	CreatedAt int64  `gorm:"autoCreateTime"`                                   // 创建时间
	UpdatedAt int64  `gorm:"autoUpdateTime"`                                   // 更新时间
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
	UserID   int64  `json:"user_id" gorm:"primary_key, column:user_id"`
	Salt     string `json:"salt" gorm:"type:varchar(255);not null"`
	Password string `json:"password" gorm:"type:varchar(255);not null" encrypt:"true"`
	Email    string `json:"email" gorm:"type:varchar(255);not null" encrypt:"true"`
	Phone    string `json:"phone" gorm:"type:varchar(255);not null" encrypt:"true"`

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
