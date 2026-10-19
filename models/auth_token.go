package models

import (
	"crypto/md5" //nolint:gosec // G501: reviewed and safe
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"time"
)

const (
	AuthTokenTypeWeb = iota
	AuthTokenTypeOauth
)

type AuthToken struct {
	Token          string `gorm:"column:token;type:varchar(128);not null;primary_key"`
	Type           uint8  `gorm:"column:type;not null"`
	UserId         uint64 `gorm:"column:user_id;not null;index"`
	ApplicationId  uint64 `gorm:"column:application_id;index"`
	ExternalClient string `gorm:"column:external_client;type:varchar(512)"` // IndieAuth / Misskey OAuth client_id URL
	// PairHash is the SHA-256 of the other token in this access/refresh pair.
	// Refreshing or reusing an authorization code revokes both halves.
	PairHash  string     `gorm:"column:pair_hash;type:varchar(64)"`
	ExpiredAt *time.Time `gorm:"column:expired_at;"` // 如果为空则表示永不过期,如果 now > expired_at 则表示已经过期, 会有定时任务定时清理过期的token
	CreatedAt *time.Time `gorm:"column:created_at;not null;autoCreateTime"`
	Revoke    *time.Time `gorm:"column:revoke"` // 撤销时间,当撤销时间不为空时候,表示token已经被撤销
	// LastUsedAt is refreshed by the auth middleware at most once an hour;
	// NULL means never used since the column was added.
	LastUsedAt *time.Time `gorm:"column:last_used_at"`
	// RevokeReason explains an automatic revoke ("inactive"); empty otherwise.
	RevokeReason string `gorm:"column:revoke_reason;size:32"`
	// EffectiveScopes, set by the auth middleware for personal access tokens,
	// replaces the scopes baked into the token blob (they can be edited).
	EffectiveScopes *ApplicationScope `gorm:"-" json:"-"`
}

func (AuthToken) TableName() string {
	return "auth_tokens"
}

// AuthTokenData 用于存放在token里面的数据, 用于解析token,降低数据库查询次数.
type AuthTokenData struct {
	UserId        uint64            `json:"user_id"`
	AuthType      uint8             `json:"auth_type"`
	ApplicationId uint64            `json:"application_id"`
	Permissions   uint64            `json:"permissions"`
	Scopes        *ApplicationScope `json:"scopes"`
	Scope         string            `json:"scope,omitempty"`
	CreatedAt     int64             `json:"created_at"`
	ExpiredAt     int64             `json:"expired_at"`
}

func (a AuthTokenData) GetTokenStr(key string) string {
	// AuthTokenData 先进行json序列化, 并且在 前面填充 8 个字节的随机字符串, 用于防止被猜解
	rawData, err := json.Marshal(a)
	if err != nil {
		return ""
	}
	randStr := utils.GetRandomString(8)

	rawStr := randStr + string(rawData)
	// 使用aes加密
	encryptedStr, err := utils.AesEncrypt([]byte(rawStr), key)
	if err != nil {
		// 加密失败时返回空字符串，避免生成结构错误、可被利用的 token。
		return ""
	}

	rawResult := randStr + encryptedStr
	// 把rawResult 进行 m5加密, 用于校验
	md5Str := md5.Sum([]byte(rawResult)) //nolint:gosec // G401: reviewed and safe
	// 取md5的前8位
	rawResult = hex.EncodeToString(md5Str[:])[:8] + rawResult

	return rawResult
}

func GetAuthTokenData(tokenStr string, encKey string) (tokenData *AuthTokenData, err error) {
	// 检查 token 长度
	if len(tokenStr) < 16 {
		err = errors.New("invalid token length")

		return tokenData, err
	}

	// 取出校验码 token 的前 8 位
	md5Str := tokenStr[:8]
	// 取出校验码后的 8位随机字符串
	randStr := tokenStr[8:16]
	// 取出加密后的字符串
	encryptedStr := tokenStr[16:]

	// 校验 token（使用常量时间比较，防止时序攻击）
	md5Bytes := md5.Sum([]byte(randStr + encryptedStr)) //nolint:gosec // G401: reviewed and safe

	checkMd5Str := hex.EncodeToString(md5Bytes[:])[:8]
	if subtle.ConstantTimeCompare([]byte(md5Str), []byte(checkMd5Str[:8])) != 1 {
		err = errors.New("invalid token check code")

		return tokenData, err
	}

	// 解密
	decryptedStr, err := utils.AesDecrypt(encryptedStr, encKey)
	if err != nil {
		err = errors.Wrap(err, "invalid token encrypted data")

		return tokenData, err
	}

	// 取出随机字符串
	innerRandStr := string(decryptedStr[:8])

	// 判断随机字符串是否正确（使用常量时间比较，防止时序攻击）
	if subtle.ConstantTimeCompare([]byte(innerRandStr), []byte(randStr)) != 1 {
		err = errors.New("invalid token rand str")

		return tokenData, err
	}

	// 取出真实的数据
	rawData := decryptedStr[8:]

	// 解析数据
	tokenData = &AuthTokenData{}

	err = json.Unmarshal(rawData, tokenData)
	if err != nil {
		err = errors.Wrap(err, "invalid token data")

		return tokenData, err
	}

	return tokenData, err
}

func NewAuthToken(userId uint64, authType uint8, appId uint64, permissions uint64, scopes *ApplicationScope, expiredIn int, encKey string) (tokenRow *AuthToken, tokenStr string) {
	now := time.Now()

	var expiredAt *time.Time

	if expiredIn > 0 {
		expiredAt = &time.Time{}
		*expiredAt = now.Add(time.Second * time.Duration(expiredIn))
	}

	var expiredAtUnix int64
	if expiredAt != nil {
		expiredAtUnix = expiredAt.Unix()
	}

	tokenData := &AuthTokenData{
		UserId:        userId,
		AuthType:      authType,
		Permissions:   permissions,
		Scopes:        scopes,
		ApplicationId: appId,
		CreatedAt:     now.Unix(),
		ExpiredAt:     expiredAtUnix,
	}

	if scopes != nil {
		tokenData.Scope = scopes.OAuthScopeString()
	}

	if tokenData.ExpiredAt > 0 {
		tokenData.ExpiredAt = expiredAtUnix
	}

	tokenStr = tokenData.GetTokenStr(encKey)

	// 进行sha256加密存储
	sha256Token := sha256.Sum256([]byte(tokenStr))
	sha256TokenStr := hex.EncodeToString(sha256Token[:])
	tokenRow = &AuthToken{
		UserId:        userId,
		Type:          authType,
		Token:         sha256TokenStr,
		ExpiredAt:     expiredAt,
		ApplicationId: appId,
	}

	return tokenRow, tokenStr
}

// RevokeTokenByTokenStr marks the stored row for a raw bearer token revoked.
// A missing row is not an error (RFC 7009 treats unknown tokens as revoked).
// The access/refresh partner (PairHash) is revoked too.
func RevokeTokenByTokenStr(conn *gorm.DB, tokenStr string) (pairHash string, err error) {
	sum := sha256.Sum256([]byte(tokenStr))
	hash := hex.EncodeToString(sum[:])
	now := time.Now()

	var row AuthToken
	if err := conn.Where("token = ?", hash).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}

		return "", errors.Wrap(err, "find token")
	}

	hashes := []string{hash}
	if row.PairHash != "" {
		hashes = append(hashes, row.PairHash)
	}

	if err := conn.Model(&AuthToken{}).Where("token IN ? AND revoke IS NULL", hashes).Update("revoke", now).Error; err != nil {
		return row.PairHash, errors.Wrap(err, "mark token revoked")
	}

	return row.PairHash, nil
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

// RevokeReasonInactive marks a token expired by the inactivity rule.
const RevokeReasonInactive = "inactive"
