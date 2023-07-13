package models

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"time"
)

const (
	AuthTokenTypeWeb = iota
	AuthTokenTypeOauth
)

type AuthToken struct {
	Token         string     `gorm:"column:token;type:varchar(128);not null;primary_key"`
	Type          uint8      `gorm:"column:type;not null"`
	UserId        uint64     `gorm:"column:user_id;not null;index"`
	ApplicationId uint64     `gorm:"column:application_id;index"`
	ExpiredAt     *time.Time `gorm:"column:expired_at;"` // 如果为空则表示永不过期,如果 now > expired_at 则表示已经过期, 会有定时任务定时清理过期的token
	CreatedAt     *time.Time `gorm:"column:created_at;not null;autoCreateTime"`
	Revoke        *time.Time `gorm:"column:revoke"` // 撤销时间,当撤销时间不为空时候,表示token已经被撤销
}

func (AuthToken) TableName() string {
	return "auth_tokens"
}

// AuthTokenData 用于存放在token里面的数据, 用于解析token,降低数据库查询次数
type AuthTokenData struct {
	UserId      uint64            `json:"user_id"`
	AuthType    uint8             `json:"auth_type"`
	Permissions uint64            `json:"permissions"`
	Scopes      *ApplicationScope `json:"scopes"`
	CreatedAt   int64             `json:"created_at"`
	ExpiredAt   int64             `json:"expired_at"`
}

func (a AuthTokenData) GetTokenStr(key string) string {
	// AuthTokenData 先进行json序列化, 并且在 前面填充 8 个字节的随机字符串, 用于防止被猜解
	rawData, _ := json.Marshal(a)
	randStr := utils.GetRandomString(8)

	rawStr := randStr + string(rawData)
	// 使用aes加密
	encryptedStr, _ := utils.AesEncrypt([]byte(rawStr), key)

	rawResult := randStr + encryptedStr
	// 把rawResult 进行 m5加密, 用于校验
	md5Str := md5.Sum([]byte(rawResult))
	// 取md5的前8位
	rawResult = hex.EncodeToString(md5Str[:])[:8] + rawResult

	return rawResult
}

func GetAuthTokenData(tokenStr string, encKey string) (tokenData *AuthTokenData, err error) {
	// 取出校验码 token 的前 8 位
	md5Str := tokenStr[:8]
	// 取出校验码后的 8位随机字符串
	randStr := tokenStr[8:16]
	// 取出加密后的字符串
	encryptedStr := tokenStr[16:]

	// 校验 token
	md5Bytes := md5.Sum([]byte(randStr + encryptedStr))
	checkMd5Str := hex.EncodeToString(md5Bytes[:])[:8]
	if md5Str != string(checkMd5Str[:8]) {
		err = errors.New("invalid token check code")
		return
	}

	// 解密
	decryptedStr, err := utils.AesDecrypt(encryptedStr, encKey)
	if err != nil {
		err = errors.Wrap(err, "invalid token encrypted data")
		return
	}

	// 取出随机字符串
	innerRandStr := string(decryptedStr[:8])

	// 判断随机字符串是否正确
	if innerRandStr != randStr {
		err = errors.New("invalid token rand str")
		return
	}

	// 取出真实的数据
	rawData := decryptedStr[8:]

	// 解析数据
	tokenData = &AuthTokenData{}
	err = json.Unmarshal(rawData, tokenData)
	if err != nil {
		err = errors.Wrap(err, "invalid token data")
		return
	}

	return

}

func NewAuthToken(userId uint64, authType uint8, permissions uint64, scopes *ApplicationScope, expiredIn int, encKey string) (tokenRow *AuthToken, tokenStr string) {
	now := time.Now()
	var expiredAt *time.Time

	if expiredIn > 0 {
		expiredAt = &time.Time{}
		*expiredAt = now.Add(time.Second * time.Duration(expiredIn))
	}

	tokenData := &AuthTokenData{
		UserId:      userId,
		AuthType:    authType,
		Permissions: permissions,
		Scopes:      scopes,
		CreatedAt:   now.Unix(),
		ExpiredAt:   expiredAt.Unix(),
	}

	if tokenData.ExpiredAt > 0 {
		tokenData.ExpiredAt = expiredAt.Unix()
	}

	tokenStr = tokenData.GetTokenStr(encKey)

	// 进行sha256加密存储
	sha256Token := sha256.Sum256([]byte(tokenStr))
	sha256TokenStr := hex.EncodeToString(sha256Token[:])
	tokenRow = &AuthToken{
		UserId:    userId,
		Type:      authType,
		Token:     sha256TokenStr,
		ExpiredAt: expiredAt,
	}

	return
}

func GetTokenByTokenStr(tokenStr string) (token *AuthToken, err error) {
	sha256Token := sha256.Sum256([]byte(tokenStr))
	sha256TokenStr := hex.EncodeToString(sha256Token[:])
	token = &AuthToken{}
	err = db.GetDB().Where("token = ?", sha256TokenStr).First(token).Error
	if err != nil {
		err = errors.Wrap(err, "get token by token str error")
		return
	}

	return
}
