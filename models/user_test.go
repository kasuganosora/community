package models

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestEncryptPassword_And_CompareHashAndPassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		salt     string
	}{
		{
			name:     "simple password",
			password: "password123",
			salt:     "randomsalt",
		},
		{
			name:     "complex password",
			password: "P@ssw0rd!#$%",
			salt:     "abc123xyz",
		},
		{
			name:     "long password",
			password: "this_is_a_very_long_password_that_should_still_work_correctly",
			salt:     "short",
		},
		{
			name:     "empty password",
			password: "",
			salt:     "somesalt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test encryption
			hash, err := EncryptPassword(tt.password, tt.salt)
			require.NoError(t, err)
			assert.NotEmpty(t, hash)

			// Test correct password comparison
			err = CompareHashAndPassword(hash, tt.password, tt.salt)
			require.NoError(t, err, "Correct password should match")

			// Test wrong password comparison
			wrongPassword := tt.password + "wrong"
			if wrongPassword == tt.password {
				wrongPassword = "definitelywrong"
			}

			err = CompareHashAndPassword(hash, wrongPassword, tt.salt)
			require.Error(t, err, "Wrong password should not match")

			// Test wrong salt comparison
			wrongSalt := tt.salt + "wrong"
			if wrongSalt == tt.salt {
				wrongSalt = "definitelywrong"
			}

			err = CompareHashAndPassword(hash, tt.password, wrongSalt)
			assert.Error(t, err, "Wrong salt should not match")
		})
	}
}

func TestEncryptPassword_DifferentSalts(t *testing.T) {
	password := "samepassword"
	salt1 := "salt1"
	salt2 := "salt2"

	hash1, err := EncryptPassword(password, salt1)
	require.NoError(t, err)

	hash2, err := EncryptPassword(password, salt2)
	require.NoError(t, err)

	// Different salts should produce different hashes
	assert.NotEqual(t, hash1, hash2, "Different salts should produce different hashes")
}

func TestEncryptPassword_SameInputsSameHash(t *testing.T) {
	password := "samepassword"
	salt := "samesalt"

	hash1, err := EncryptPassword(password, salt)
	require.NoError(t, err)

	hash2, err := EncryptPassword(password, salt)
	require.NoError(t, err)

	// bcrypt generates different hashes even for same inputs due to salt
	// So we need to verify by comparing
	err = CompareHashAndPassword(hash1, password, salt)
	require.NoError(t, err)

	err = CompareHashAndPassword(hash2, password, salt)
	require.NoError(t, err)
}

// Production salts are 64 characters long (see register.go / tootctl), which
// used to push salt+password over bcrypt's 72-byte limit.
func TestEncryptPassword_ProductionSaltLength(t *testing.T) {
	salt := strings.Repeat("s", 64)

	for _, pw := range []string{"Passw0rd!123", "密码很长很长很长很长很长很长", strings.Repeat("x", 200)} {
		hash, err := EncryptPassword(pw, salt)
		require.NoError(t, err, "password %q", pw)
		require.NoError(t, CompareHashAndPassword(hash, pw, salt))
	}

	// Passwords that only differ after the 8th byte must not collide.
	hash, err := EncryptPassword("abcdefgh-first", salt)
	require.NoError(t, err)
	assert.Error(t, CompareHashAndPassword(hash, "abcdefgh-second", salt))
}

func TestCompareHashAndPassword_LegacyHash(t *testing.T) {
	salt := "legacysalt"
	pw := "oldpassword"

	legacy, err := bcrypt.GenerateFromPassword([]byte(salt+pw), bcrypt.MinCost)
	require.NoError(t, err)

	stored := base64.StdEncoding.EncodeToString(legacy)
	require.NoError(t, CompareHashAndPassword(stored, pw, salt))
	assert.Error(t, CompareHashAndPassword(stored, "wrong", salt))
}
