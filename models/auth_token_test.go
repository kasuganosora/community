package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAuthTokenData_GetTokenStr(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef" // 32 bytes key

	tokenData := &AuthTokenData{
		UserId:        1,
		AuthType:      AuthTokenTypeWeb,
		ApplicationId: 0,
		Permissions:   0,
		Scopes:        nil,
		CreatedAt:     time.Now().Unix(),
		ExpiredAt:     0,
	}

	tokenStr := tokenData.GetTokenStr(key)
	assert.NotEmpty(t, tokenStr)
	assert.GreaterOrEqual(t, len(tokenStr), 16)
}

func TestGetAuthTokenData(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef" // 32 bytes key

	t.Run("Valid token", func(t *testing.T) {
		tokenData := &AuthTokenData{
			UserId:        123,
			AuthType:      AuthTokenTypeWeb,
			ApplicationId: 0,
			Permissions:   0,
			Scopes:        nil,
			CreatedAt:     time.Now().Unix(),
			ExpiredAt:     0,
		}

		tokenStr := tokenData.GetTokenStr(key)

		parsedData, err := GetAuthTokenData(tokenStr, key)
		assert.NoError(t, err)
		assert.NotNil(t, parsedData)
		assert.Equal(t, tokenData.UserId, parsedData.UserId)
		assert.Equal(t, tokenData.AuthType, parsedData.AuthType)
		assert.Equal(t, tokenData.ApplicationId, parsedData.ApplicationId)
	})

	t.Run("Invalid token length", func(t *testing.T) {
		_, err := GetAuthTokenData("short", key)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid token length")
	})

	t.Run("Invalid token check code", func(t *testing.T) {
		// 16个字符 + 一些额外字符，但校验码错误
		invalidToken := "12345678123456789" + "someinvalidstring"
		_, err := GetAuthTokenData(invalidToken, key)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid token check code")
	})

	t.Run("Wrong key", func(t *testing.T) {
		tokenData := &AuthTokenData{
			UserId:        1,
			AuthType:      AuthTokenTypeWeb,
			ApplicationId: 0,
			Permissions:   0,
			Scopes:        nil,
			CreatedAt:     time.Now().Unix(),
			ExpiredAt:     0,
		}

		tokenStr := tokenData.GetTokenStr(key)
		wrongKey := "abcdef0123456789abcdef0123456789"

		// Using wrong key should result in error or panic
		defer func() {
			if r := recover(); r == nil {
				// If no panic, we expect an error
				// The function may return error in some cases
			}
		}()
		_, err := GetAuthTokenData(tokenStr, wrongKey)
		// If we get here without panic, should have error
		assert.Error(t, err)
	})
}

func TestNewAuthToken(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"

	t.Run("Token without expiration", func(t *testing.T) {
		tokenRow, tokenStr := NewAuthToken(1, AuthTokenTypeWeb, 0, 0, nil, 0, key)

		assert.NotNil(t, tokenRow)
		assert.NotEmpty(t, tokenStr)
		assert.Equal(t, uint64(1), tokenRow.UserId)
		assert.Equal(t, uint8(AuthTokenTypeWeb), tokenRow.Type)
		assert.NotEmpty(t, tokenRow.Token)
		assert.Nil(t, tokenRow.ExpiredAt)
	})

	t.Run("Token with expiration", func(t *testing.T) {
		tokenRow, tokenStr := NewAuthToken(2, AuthTokenTypeOauth, 100, 0, nil, 3600, key)

		assert.NotNil(t, tokenRow)
		assert.NotEmpty(t, tokenStr)
		assert.Equal(t, uint64(2), tokenRow.UserId)
		assert.Equal(t, uint8(AuthTokenTypeOauth), tokenRow.Type)
		assert.NotNil(t, tokenRow.ExpiredAt)
	})

	t.Run("Token with scopes", func(t *testing.T) {
		scopes := &ApplicationScope{
			ReadScope:  AppScopeRead,
			WriteScope: AppScopeWrite,
			AdminScope: 0,
			OtherScope: 0,
		}

		tokenRow, tokenStr := NewAuthToken(3, AuthTokenTypeOauth, 200, 0, scopes, 7200, key)

		assert.NotNil(t, tokenRow)
		assert.NotEmpty(t, tokenStr)
		assert.Equal(t, uint64(3), tokenRow.UserId)
		assert.Equal(t, uint8(AuthTokenTypeOauth), tokenRow.Type)
	})

	t.Run("Token round-trip", func(t *testing.T) {
		userId := uint64(999)
		authType := uint8(AuthTokenTypeWeb)
		appId := uint64(123)
		permissions := uint64(0x1 | 0x2 | 0x4)

		tokenRow, tokenStr := NewAuthToken(userId, authType, appId, permissions, nil, 0, key)

		// Parse token
		parsedData, err := GetAuthTokenData(tokenStr, key)
		assert.NoError(t, err)

		assert.Equal(t, userId, parsedData.UserId)
		assert.Equal(t, authType, parsedData.AuthType)
		assert.Equal(t, appId, parsedData.ApplicationId)
		assert.Equal(t, permissions, parsedData.Permissions)

		// Verify stored token hash
		assert.NotEmpty(t, tokenRow.Token)
		assert.Equal(t, 64, len(tokenRow.Token)) // SHA256 hex encoded = 64 chars
	})
}

func TestAuthToken_TableName(t *testing.T) {
	token := AuthToken{}
	assert.Equal(t, "auth_tokens", token.TableName())
}
