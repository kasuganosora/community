package models

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPersistentToken_RoundTripAndValidation(t *testing.T) {
	encKey := "0123456789abcdef0123456789abcdef"

	row, rawToken, err := NewPersistentToken(42, "", "paired-session-hash", encKey)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.True(t, strings.HasPrefix(rawToken, PersistentTokenPrefix))
	assert.Equal(t, uint8(AuthTokenTypePersistent), row.Type)
	assert.Equal(t, uint64(42), row.UserId)
	assert.Nil(t, row.ExpiredAt, "persistent login credentials must not expire by time")
	assert.Equal(t, "paired-session-hash", row.PairHash)
	assert.Equal(t, PersistentTokenHash(rawToken), row.Token)
	assert.True(t, strings.HasPrefix(row.ExternalClient, PersistentDevicePrefix))

	parsed, err := ParsePersistentToken(rawToken, encKey)
	require.NoError(t, err)
	assert.Equal(t, PersistentTokenVersion, parsed.Version)
	assert.Equal(t, uint64(42), parsed.UserId)
	assert.Equal(t, strings.TrimPrefix(row.ExternalClient, PersistentDevicePrefix), parsed.DeviceId)
	assert.NotEmpty(t, parsed.Nonce)

	// Preserving deviceId across rotations produces a distinct ciphertext and hash on the same device.
	row2, rawToken2, err := NewPersistentToken(42, parsed.DeviceId, "next-session-hash", encKey)
	require.NoError(t, err)
	assert.NotEqual(t, rawToken, rawToken2)
	assert.NotEqual(t, row.Token, row2.Token)
	assert.Equal(t, row.ExternalClient, row2.ExternalClient)

	// Wrong encryption key must fail authentication.
	_, err = ParsePersistentToken(rawToken, "wrong-server-secret-key")
	require.Error(t, err)

	// Tampered ciphertext must fail GCM authentication.
	tampered := rawToken[:len(rawToken)-2] + "AA"
	if tampered == rawToken {
		tampered = rawToken[:len(rawToken)-2] + "BB"
	}
	_, err = ParsePersistentToken(tampered, encKey)
	require.Error(t, err)

	// Domain separation: web/OAuth tokens cannot parse as persistent tokens and vice versa.
	_, webRaw := NewAuthToken(42, AuthTokenTypeWeb, 0, 0, nil, 3600, encKey)
	_, err = ParsePersistentToken(webRaw, encKey)
	require.Error(t, err)
	_, err = GetAuthTokenData(rawToken, encKey)
	require.Error(t, err)
}

func TestRevokeSingleTokenByTokenStr_PreservesPair(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(&AuthToken{}))

	encKey := "test-enc-key-123456"
	webRow, webRaw := NewAuthToken(7, AuthTokenTypeWeb, 0, 0, nil, 3600, encKey)
	persistRow, _, err := NewPersistentToken(7, "dev-1", webRow.Token, encKey)
	require.NoError(t, err)
	webRow.PairHash = persistRow.Token

	require.NoError(t, conn.Create(webRow).Error)
	require.NoError(t, conn.Create(persistRow).Error)

	require.NoError(t, RevokeSingleTokenByTokenStr(conn, webRaw))

	var gotWeb, gotPersist AuthToken
	require.NoError(t, conn.Where("token = ?", webRow.Token).First(&gotWeb).Error)
	require.NoError(t, conn.Where("token = ?", persistRow.Token).First(&gotPersist).Error)
	assert.NotNil(t, gotWeb.Revoke)
	assert.Nil(t, gotPersist.Revoke, "paired persistent token must stay valid when switching accounts")
}
