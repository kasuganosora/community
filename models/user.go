package models

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"reflect"
	"strings"
	"time"
)

const (
	UserStatusNormal = iota
	UserStatusUnapproved
	UserStatusRejected
	UserStatusUnverifiedMail // 未验证邮箱
)

type User struct { //nolint:recvcheck
	Id        uint64     `gorm:"primary_key;autoIncrement"                        json:"id"`
	UUID      string     `gorm:"unique;not null;type:varchar(36)"                 json:"uuid"`       // UUID
	UserName  string     `gorm:"unique;column:username;not null;type:varchar(64)" json:"username"`   // 用户名
	IsBan     bool       `gorm:"default:false"                                    json:"is_ban"`     // 是否被封禁
	IsAdmin   bool       `gorm:"default:false"                                    json:"is_admin"`   // 是否是管理员
	EmailHash string     `gorm:"type:varchar(255); unique;not null"               json:"email_hash"` // 邮箱的 hash 值 用于 email登录,和测试是否是同一个邮箱
	CreatedAt *time.Time `gorm:"autoCreateTime"`                                                     // 创建时间
	UpdatedAt *time.Time `gorm:"autoUpdateTime"`                                                     // 更新时间
	InviteBy  uint64     `gorm:"default:0"                                        json:"invite_by"`  // 邀请人ID
	Status    int        `gorm:"default:0"                                        json:"status"`     // 用户状态 0:正常 1:未审批, 2:被拒绝,3: 未验证邮箱
	// Current* is the latest successful web sign-in. Last* is the one before it.
	// Empty IP strings mean unknown; nothing here is inferred.
	CurrentSignInAt *time.Time      `json:"-"`
	LastSignInAt    *time.Time      `json:"-"`
	CurrentSignInIP string          `gorm:"type:varchar(45);not null;default:''" json:"-"`
	LastSignInIP    string          `gorm:"type:varchar(45);not null;default:''" json:"-"`
	SignInCount     int             `gorm:"not null;default:0" json:"-"`
	Properties      map[string]any  `gorm:"type:json;serializer:json"                        json:"properties"` // 用户的属性
	IsMailVerified  bool            `gorm:"default:false"                                    json:"-"`          // 邮箱是否验证
	UserSecret      *UserSecretData `json:"-"`                                                                  // 用户的敏感数据
	UserMeta        []*UserMeta     `json:"-"`                                                                  // 用户的元数据
	DeletedAt       gorm.DeletedAt  `gorm:"index"                                            json:"-"`
	Settings        *UserSetting    `json:"-"` // 用户的设置
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
	PasswordScheme  string         `gorm:"type:varchar(32);default:''" json:"-"`
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

// EncryptSecretValue encrypts a value for an `encrypt:"true"` column of a
// row with the given salt, exactly as BeforeSave does. Use it for column
// updates that bypass the struct hooks.
func EncryptSecretValue(value, salt string) (string, error) {
	if config.C.EncryptedKey == "" {
		return value, nil
	}

	return utils.AesEncrypt([]byte(value), utils.GetEncryptKey(config.C.EncryptedKey, salt))
}

// passwordHashV2Prefix marks hashes produced by the current scheme.
// Hashes without it were produced by the legacy "bcrypt(salt+password)" scheme.
const passwordHashV2Prefix = "v2$"

// bcryptMaxInput is the maximum number of bytes bcrypt looks at.
const bcryptMaxInput = 72

// prehashPassword derives a fixed-length (44 byte) bcrypt input from the salt
// and password using HMAC-SHA256.
//
// The legacy scheme fed salt+password straight into bcrypt. Salts are 64
// characters long, so salt+password exceeded bcrypt's 72-byte limit for any
// password longer than 8 bytes: current golang.org/x/crypto rejects that with
// ErrPasswordTooLong (registration / password change / tootctl all failed
// with "encrypt password failed"), and older versions silently truncated,
// meaning only the first 8 bytes of the password were ever checked.
func prehashPassword(password, salt string) []byte {
	mac := hmac.New(sha256.New, []byte(salt))
	mac.Write([]byte(password))

	return []byte(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
}

func EncryptPassword(password string, salt string) (ret string, err error) {
	hash, err := bcrypt.GenerateFromPassword(prehashPassword(password, salt), bcrypt.DefaultCost)
	if err != nil {
		return
	}

	ret = passwordHashV2Prefix + base64.StdEncoding.EncodeToString(hash)

	return
}

func CompareHashAndPassword(base64PwdHash, password string, salt string) (err error) {
	if strings.HasPrefix(base64PwdHash, passwordHashV2Prefix) {
		hash, decErr := base64.StdEncoding.DecodeString(strings.TrimPrefix(base64PwdHash, passwordHashV2Prefix))
		if decErr != nil {
			return decErr
		}

		return bcrypt.CompareHashAndPassword(hash, prehashPassword(password, salt))
	}

	// Legacy hash: bcrypt(salt+password). Older bcrypt implementations
	// silently truncated the input to 72 bytes, so do the same here to keep
	// verifying hashes that were created that way.
	legacy := []byte(salt + password)
	if len(legacy) > bcryptMaxInput {
		legacy = legacy[:bcryptMaxInput]
	}

	hash, err := base64.StdEncoding.DecodeString(base64PwdHash)
	if err != nil {
		return
	}

	return bcrypt.CompareHashAndPassword(hash, legacy)
}

// CheckPassword retains the source verifier only when explicitly marked by a
// validated import. A subsequent password change uses the native scheme.
func (u *UserSecretData) CheckPassword(password string) error {
	switch u.PasswordScheme {
	case "", "community":
		return CompareHashAndPassword(u.Password, password, u.Salt)
	case "bcrypt":
		if err := ValidateImportedBcrypt(u.Password); err != nil {
			return err
		}
		input := []byte(password)
		if len(input) > bcryptMaxInput {
			input = input[:bcryptMaxInput]
		}
		return bcrypt.CompareHashAndPassword([]byte(u.Password), input)
	default:
		return errors.New("unsupported password scheme")
	}
}
func ValidateImportedBcrypt(hash string) error {
	if !(strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2y$")) {
		return errors.New("unsupported imported bcrypt encoding")
	}
	if len(hash) != 60 {
		return errors.New("invalid imported bcrypt encoding")
	}
	_, err := bcrypt.Cost([]byte(hash))
	return err
}
