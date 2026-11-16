package models

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/pkg/errors"
)

const (
	// PersistentTokenPrefix identifies encrypted persistent login credentials
	// and keeps them domain-separated from web session and OAuth bearer tokens.
	PersistentTokenPrefix = "pl1."
	// PersistentTokenVersion is the wire format version of PersistentTokenData.
	PersistentTokenVersion uint8 = 1
	// PersistentDevicePrefix namespaces auth_tokens.external_client for device-scoped web sessions.
	PersistentDevicePrefix = "device:"

	persistentKeyDomain = "community:persistent-login:v1"
	maxPersistentLen    = 2048
	maxDeviceIDLen      = 64
)

// ErrInvalidTokenGeneration indicates that session token serialization failed.
var ErrInvalidTokenGeneration = errors.New("failed to generate session token")

// PersistentTokenData is encrypted inside a persistent login credential with
// AES-256-GCM using a key derived from config.C.EncryptedKey.
type PersistentTokenData struct {
	Version   uint8  `json:"v"`
	UserId    uint64 `json:"u"`
	DeviceId  string `json:"d"`
	Nonce     string `json:"n"`
	CreatedAt int64  `json:"c"`
}

func derivePersistentKey(encKey string) []byte {
	mac := hmac.New(sha256.New, []byte(encKey))
	_, _ = mac.Write([]byte(persistentKeyDomain))

	return mac.Sum(nil)
}

// GeneratePersistentDeviceID generates a cryptographically random 128-bit hex device identifier.
func GeneratePersistentDeviceID() (string, error) {
	var buf [16]byte
	if _, err := io.ReadFull(rand.Reader, buf[:]); err != nil {
		return "", errors.Wrap(err, "generate device id")
	}

	return hex.EncodeToString(buf[:]), nil
}

// PersistentTokenHash returns the SHA-256 hex digest stored in auth_tokens.token.
func PersistentTokenHash(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))

	return hex.EncodeToString(sum[:])
}

// PersistentDeviceClient formats the device identifier stored in auth_tokens.external_client.
func PersistentDeviceClient(deviceID string) string {
	return PersistentDevicePrefix + deviceID
}

// NewPersistentToken mints a non-expiring, server-encrypted persistent login credential
// paired to sessionHash for a single device.
func NewPersistentToken(userId uint64, deviceId string, sessionHash string, encKey string) (*AuthToken, string, error) {
	if userId == 0 {
		return nil, "", errors.New("invalid persistent token user id")
	}

	deviceId = strings.TrimSpace(deviceId)
	if deviceId == "" {
		var err error
		deviceId, err = GeneratePersistentDeviceID()
		if err != nil {
			return nil, "", err
		}
	}
	if len(deviceId) > maxDeviceIDLen {
		return nil, "", errors.New("invalid persistent token device id")
	}

	var nonceBytes [16]byte
	if _, err := io.ReadFull(rand.Reader, nonceBytes[:]); err != nil {
		return nil, "", errors.Wrap(err, "generate persistent token nonce")
	}

	payload := PersistentTokenData{
		Version:   PersistentTokenVersion,
		UserId:    userId,
		DeviceId:  deviceId,
		Nonce:     hex.EncodeToString(nonceBytes[:]),
		CreatedAt: time.Now().Unix(),
	}
	rawJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, "", errors.Wrap(err, "marshal persistent token")
	}

	block, err := aes.NewCipher(derivePersistentKey(encKey))
	if err != nil {
		return nil, "", errors.Wrap(err, "init persistent cipher")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, "", errors.Wrap(err, "init persistent gcm")
	}

	gcmNonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, gcmNonce); err != nil {
		return nil, "", errors.Wrap(err, "generate persistent gcm nonce")
	}

	sealed := gcm.Seal(gcmNonce, gcmNonce, rawJSON, []byte(persistentKeyDomain))
	rawToken := PersistentTokenPrefix + base64.RawURLEncoding.EncodeToString(sealed)
	tokenHash := PersistentTokenHash(rawToken)

	row := &AuthToken{
		UserId:         userId,
		Type:           AuthTokenTypePersistent,
		Token:          tokenHash,
		ExternalClient: PersistentDeviceClient(deviceId),
		PairHash:       sessionHash,
		ExpiredAt:      nil,
	}

	return row, rawToken, nil
}

// ParsePersistentToken authenticates and decrypts a persistent login credential.
func ParsePersistentToken(rawToken string, encKey string) (*PersistentTokenData, error) {
	rawToken = strings.TrimSpace(rawToken)
	if !strings.HasPrefix(rawToken, PersistentTokenPrefix) || len(rawToken) > maxPersistentLen {
		return nil, errors.New("invalid persistent token format")
	}

	encoded := strings.TrimPrefix(rawToken, PersistentTokenPrefix)
	sealed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("invalid persistent token encoding")
	}

	block, err := aes.NewCipher(derivePersistentKey(encKey))
	if err != nil {
		return nil, errors.New("persistent cipher error")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("persistent gcm error")
	}

	nonceSize := gcm.NonceSize()
	if len(sealed) <= nonceSize {
		return nil, errors.New("invalid persistent token ciphertext")
	}

	plain, err := gcm.Open(nil, sealed[:nonceSize], sealed[nonceSize:], []byte(persistentKeyDomain))
	if err != nil {
		return nil, errors.New("persistent token authentication failed")
	}

	var data PersistentTokenData
	if err := json.Unmarshal(plain, &data); err != nil {
		return nil, errors.New("invalid persistent token payload")
	}
	if data.Version != PersistentTokenVersion || data.UserId == 0 || data.DeviceId == "" || len(data.DeviceId) > maxDeviceIDLen || data.Nonce == "" {
		return nil, errors.New("invalid persistent token claims")
	}

	return &data, nil
}
