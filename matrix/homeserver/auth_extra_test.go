package homeserver

import (
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== generateAccessToken / generateRefreshToken / generateDeviceID tests =====

func TestGenerateAccessToken(t *testing.T) {
	token1 := generateAccessToken()
	token2 := generateAccessToken()
	assert.NotEmpty(t, token1)
	assert.True(t, len(token1) > 10, "access token should be reasonably long")
	assert.Contains(t, token1, "syt_")
	assert.NotEqual(t, token1, token2, "access tokens should be unique")
}

func TestGenerateRefreshToken(t *testing.T) {
	token1 := generateRefreshToken()
	token2 := generateRefreshToken()
	assert.NotEmpty(t, token1)
	assert.Contains(t, token1, "rtr_")
	assert.NotEqual(t, token1, token2, "refresh tokens should be unique")
}

func TestGenerateDeviceID(t *testing.T) {
	id1 := generateDeviceID()
	id2 := generateDeviceID()
	assert.NotEmpty(t, id1)
	assert.NotEqual(t, id1, id2, "device IDs should be unique")
}

// ===== HashPassword tests =====

func TestHashPassword(t *testing.T) {
	hashed, err := HashPassword("mypassword", "mysalt")
	require.NoError(t, err)
	assert.NotEmpty(t, hashed)
	assert.NotEqual(t, "mypassword", hashed, "hashed password should differ from plaintext")
}

func TestHashPassword_DifferentInputProducesDifferentHash(t *testing.T) {
	hashed1, err := HashPassword("password1", "salt")
	require.NoError(t, err)
	hashed2, err := HashPassword("password2", "salt")
	require.NoError(t, err)
	assert.NotEqual(t, hashed1, hashed2, "different passwords should produce different hashes")
}

func TestHashPassword_DifferentSaltProducesDifferentHash(t *testing.T) {
	hashed1, err := HashPassword("mypassword", "salt1")
	require.NoError(t, err)
	hashed2, err := HashPassword("mypassword", "salt2")
	require.NoError(t, err)
	assert.NotEqual(t, hashed1, hashed2, "different salts should produce different hashes")
}

// ===== AuthService.GetLoginFlows tests =====

func TestAuthService_GetLoginFlows(t *testing.T) {
	hs := minimalHS("test.local")
	auth := NewAuthService(hs)
	flows := auth.GetLoginFlows()
	require.NotNil(t, flows)
	assert.NotEmpty(t, flows.Flows)

	types := make(map[string]bool)
	for _, f := range flows.Flows {
		types[f.Type] = true
	}
	assert.True(t, types["m.login.password"], "should support m.login.password")
	assert.True(t, types["m.login.token"], "should support m.login.token")
}

// ===== AuthService.GetServerVersion tests =====

func TestAuthService_GetServerVersion(t *testing.T) {
	hs := minimalHS("test.local")
	auth := NewAuthService(hs)
	v := auth.GetServerVersion()
	require.NotNil(t, v)
	assert.Equal(t, "Community Matrix Homeserver", v.Server.Name)
	assert.Equal(t, "1.0.0", v.Server.Version)
}

// ===== AuthService.GetWellKnown tests =====

func TestAuthService_GetWellKnown(t *testing.T) {
	hs := minimalHS("myhs.test")
	auth := NewAuthService(hs)
	wk := auth.GetWellKnown()
	require.NotNil(t, wk)
	assert.Equal(t, "https://myhs.test", wk.Homeserver.BaseURL)
}

// ===== AuthService.UserExists tests =====

func TestAuthService_UserExists_RemoteUser(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	auth := NewAuthService(hs)

	// A user from a different domain is always "not exists".
	assert.False(t, auth.UserExists("@alice:other.server"))
}

func TestAuthService_UserExists_LocalNonExistent(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	auth := NewAuthService(hs)

	// Local user that doesn't exist → false.
	assert.False(t, auth.UserExists("@ghost:test.local"))
}

func TestAuthService_UserExists_BareUsername(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	auth := NewAuthService(hs)

	// A bare username (no @ prefix) is treated as a localpart lookup.
	assert.False(t, auth.UserExists("nobody"))
}

// ===== AuthService.VerifyUserPassword tests =====

func TestAuthService_VerifyUserPassword_InvalidUserID(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	auth := NewAuthService(hs)

	assert.False(t, auth.VerifyUserPassword("invalid", "pass"))
}

func TestAuthService_VerifyUserPassword_UserNotFound(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	auth := NewAuthService(hs)

	assert.False(t, auth.VerifyUserPassword("@ghost:test.local", "pass"))
}

// ===== AuthService.GetLocalAccountID tests =====

func TestAuthService_GetLocalAccountID_InvalidUserID(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	auth := NewAuthService(hs)

	id, err := auth.GetLocalAccountID("invalid")
	require.Error(t, err)
	assert.Equal(t, uint64(0), id)
	assert.Contains(t, err.Error(), "invalid matrix user id")
}

func TestAuthService_GetLocalAccountID_NotFound(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	auth := NewAuthService(hs)

	id, err := auth.GetLocalAccountID("@ghost:test.local")
	require.Error(t, err)
	assert.Equal(t, uint64(0), id)
}

// ===== AuthService.GetDeviceByToken tests =====

func TestAuthService_GetDeviceByToken_EmptyToken(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}))
	hs := minimalHS("test.local")
	auth := NewAuthService(hs)

	result, matErr := auth.GetDeviceByToken("")
	assert.Nil(t, result)
	assert.NotNil(t, matErr)
}

func TestAuthService_GetDeviceByToken_InvalidToken(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}))
	hs := minimalHS("test.local")
	auth := NewAuthService(hs)

	result, matErr := auth.GetDeviceByToken("nonexistent-token")
	assert.Nil(t, result)
	assert.NotNil(t, matErr)
}

func TestAuthService_GetDeviceByToken_ValidToken(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}))
	hs := minimalHS("test.local")
	auth := NewAuthService(hs)

	sess := &MatrixUserSession{
		UserID:      "@alice:test.local",
		DeviceID:    "DEVFOUND",
		AccessToken: "valid-token-xyz",
	}
	require.NoError(t, db.GetDB().Create(sess).Error)

	result, matErr := auth.GetDeviceByToken("valid-token-xyz")
	require.Nil(t, matErr)
	require.NotNil(t, result)
	assert.Equal(t, "@alice:test.local", result.UserID)
	assert.Equal(t, "DEVFOUND", result.DeviceID)
}

// ===== ParseMatrixUserID / FormatMatrixUserID tests =====

func TestParseMatrixUserID_Valid(t *testing.T) {
	localpart, domain, ok := ParseMatrixUserID("@alice:example.com")
	require.True(t, ok)
	assert.Equal(t, "alice", localpart)
	assert.Equal(t, "example.com", domain)
}

func TestParseMatrixUserID_Invalid(t *testing.T) {
	_, _, ok := ParseMatrixUserID("invalid")
	assert.False(t, ok)
}

func TestParseMatrixUserID_Empty(t *testing.T) {
	_, _, ok := ParseMatrixUserID("")
	assert.False(t, ok)
}

func TestFormatMatrixUserID_RoundTrip(t *testing.T) {
	assert.Equal(t, "@bob:test.local", FormatMatrixUserID("bob", "test.local"))
}

// ===== getUnixMilliPtr tests =====

func TestGetUnixMilliPtr(t *testing.T) {
	t1 := time.Now()
	ptr := getUnixMilliPtr(t1)
	require.NotNil(t, ptr)
	assert.NotZero(t, *ptr)
}

// ===== AuthService.Logout / LogoutAll tests =====

func TestAuthService_Logout_EmptyToken(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}))
	hs := minimalHS("test.local")
	auth := NewAuthService(hs)

	// Logout with empty token should not panic.
	assert.NotPanics(t, func() {
		auth.Logout("")
	})
}

func TestAuthService_LogoutAll_EmptyToken(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}))
	hs := minimalHS("test.local")
	auth := NewAuthService(hs)

	assert.NotPanics(t, func() {
		auth.LogoutAll("")
	})
}

func TestAuthService_Logout_ValidToken(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}))
	hs := minimalHS("test.local")
	auth := NewAuthService(hs)

	sess := &MatrixUserSession{
		UserID:      "@alice:test.local",
		DeviceID:    "DEVLOGOUT",
		AccessToken: "logout-token",
	}
	require.NoError(t, db.GetDB().Create(sess).Error)

	// Logout should remove the session.
	auth.Logout("logout-token")

	// Token should no longer be valid.
	result, matErr := auth.GetDeviceByToken("logout-token")
	assert.Nil(t, result)
	assert.NotNil(t, matErr)
}
