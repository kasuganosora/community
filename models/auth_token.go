package models

import (
	"encoding/json"
	"github.com/kasuganosora/community/utils"
	"time"
)

const (
	AuthTokenTypeWeb = iota
	AuthTokenTypeApp
	AuthTokenTypeOauth
)

type AuthToken struct {
	Id        int64      `gorm:"column:id;primary_key;auto_increment;not null"`
	Type      uint8      `gorm:"column:type;not null"`
	UserId    int64      `gorm:"column:user_id;not null;index"`
	Token     string     `gorm:"column:token;type:varchar(255);not null;uniqueIndex"`
	ExpiredAt *time.Time `gorm:"column:expired_at;"` // 如果为空则表示永不过期,如果 now > expired_at 则表示已经过期, 会有定时任务定时清理过期的token
	CreatedAt *time.Time `gorm:"column:created_at;not null;autoCreateTime"`
	Revoke    *time.Time `gorm:"column:revoke"` // 撤销时间,当撤销时间不为空时候,表示token已经被撤销
}

func (AuthToken) TableName() string {
	return "auth_tokens"
}

// AuthTokenData 用于存放在token里面的数据, 用于解析token,降低数据库查询次数
type AuthTokenData struct {
	UserId    int64 `json:"user_id"`
	CreatedAt int64 `json:"created_at"`
	ExpiredAt int64 `json:"expired_at"`
}

func (a AuthTokenData) GetTokenStr(key string) string {
	// AuthTokenData 先进行json序列化, 并且在 前面填充 8 个字节的随机字符串, 用于防止被猜解
	rawData, _ := json.Marshal(a)
	randStr := utils.GetRandomString(9)

	rawStr := randStr + ":" + string(rawData)
	// 使用aes加密
	encryptedStr, _ := utils.AesEncrypt([]byte(rawStr), key)

	return encryptedStr
}

func GetAuthTokenData(tokenStr string, encKey string) (tokenData *AuthTokenData, err error) {
	rawStr, err := utils.AesDecrypt(tokenStr, encKey)
	if err != nil {
		return nil, err
	}

	// 去掉前面的随机字符串和时间戳
	tokenData = &AuthTokenData{}
	err = json.Unmarshal(rawStr[10:], tokenData)
	if err != nil {
		return nil, err
	}

	return tokenData, nil
}

func NewAuthToken(userId int64, authType uint8, expiredIn int, encKey string) *AuthToken {
	now := time.Now()
	var expiredAt *time.Time

	if expiredIn > 0 {
		expiredAt = &time.Time{}
		*expiredAt = now.Add(time.Second * time.Duration(expiredIn))
	}

	tokenData := &AuthTokenData{
		UserId:    userId,
		CreatedAt: now.Unix(),
	}

	if tokenData.ExpiredAt > 0 {
		tokenData.ExpiredAt = expiredAt.Unix()
	}

	token := &AuthToken{
		UserId:    userId,
		Type:      authType,
		Token:     tokenData.GetTokenStr(encKey),
		ExpiredAt: expiredAt,
	}

	return token
}
