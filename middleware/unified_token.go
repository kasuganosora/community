package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
)

// UnifiedToken is a Mastodon API / OAuth access token accepted on another
// protocol surface (the Matrix client API). Hash is the token's DB key
// (SHA-256 of the raw token); the raw token itself is never kept.
type UnifiedToken struct {
	Hash      string
	AppID     uint64
	AppName   string
	Scopes    models.ApplicationScope
	User      *models.User
	Account   *models.Account
	ExpiresAt *time.Time
}

// ErrUnifiedTokenInvalid means the string is not a currently valid API token.
var ErrUnifiedTokenInvalid = errors.New("invalid api token")

// ResolveUnifiedToken validates a raw Mastodon API / OAuth access token exactly
// like AuthMiddleware does (check code + decrypt, payload expiry, DB row via
// the shared cache, revoke, expiry, inactivity, owner user/account active)
// and returns its current scopes. Personal tokens use their edited scopes.
// Web session tokens are rejected: only API tokens can be extended.
func ResolveUnifiedToken(ctx context.Context, raw string) (*UnifiedToken, error) {
	data, err := models.GetAuthTokenData(raw, config.C.EncryptedKey)
	if err != nil || data == nil {
		return nil, ErrUnifiedTokenInvalid
	}
	if data.AuthType != models.AuthTokenTypeOauth || tokenDataExpired(data, time.Now()) {
		return nil, ErrUnifiedTokenInvalid
	}
	token, err := getTokenWithCacheContext(ctx, raw)
	if err != nil {
		return nil, ErrUnifiedTokenInvalid
	}
	if token.Revoke != nil || (token.ExpiredAt != nil && token.ExpiredAt.Unix() > 0 && token.ExpiredAt.Before(time.Now())) {
		return nil, ErrUnifiedTokenInvalid
	}
	scopes := data.Scopes
	if token.ExternalClient == models.PersonalTokenClient && token.EffectiveScopes != nil {
		scopes = token.EffectiveScopes
	}
	out := &UnifiedToken{Hash: hashToken(raw), AppID: data.ApplicationId}
	if scopes != nil {
		out.Scopes = *scopes
	}
	if data.ExpiredAt > 0 {
		t := time.Unix(data.ExpiredAt, 0)
		out.ExpiresAt = &t
	}
	conn := db.GetDB().WithContext(ctx)
	user := &models.User{}
	if err := conn.Where("id = ?", data.UserId).First(user).Error; err != nil {
		return nil, ErrUnifiedTokenInvalid
	}
	account, err := user.GetAccountBase(conn)
	if err != nil || account == nil {
		return nil, ErrUnifiedTokenInvalid
	}
	if user.IsBan || user.Status != models.UserStatusNormal || account.IsBan {
		return nil, ErrUnifiedTokenInvalid
	}
	if data.ApplicationId > 0 {
		var app models.Application
		if conn.Select("id", "name").Where("id = ?", data.ApplicationId).Limit(1).Find(&app).Error == nil {
			out.AppName = app.Name
		}
	}
	out.User, out.Account = user, account
	touchTokenLastUsed(ctx, token, time.Now())
	return out, nil
}

// UnifiedTokenHash returns the DB key of a raw token.
func UnifiedTokenHash(raw string) string { return hashToken(raw) }

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
