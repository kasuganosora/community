// Package twofactor implements TOTP (RFC 6238) two-factor authentication
// with single-use backup codes.
package twofactor

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 default; required by authenticator apps
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	Digits = 6
	Period = 30
	// Skew is the number of steps accepted either side of the current one.
	Skew = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret returns a new random 160-bit secret, base32 without padding.
func GenerateSecret() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	return b32.EncodeToString(buf), nil
}

// DecodeSecret accepts base32 with or without padding, spaces or lowercase.
func DecodeSecret(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "=", "").Replace(strings.TrimSpace(secret)))
	if s == "" {
		return nil, fmt.Errorf("empty secret")
	}

	key, err := b32.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid base32 secret")
	}

	return key, nil
}

// NormalizeSecret re-encodes a secret into canonical form.
func NormalizeSecret(secret string) (string, error) {
	key, err := DecodeSecret(secret)
	if err != nil {
		return "", err
	}

	return b32.EncodeToString(key), nil
}

// Step returns the RFC 6238 time step for t.
func Step(t time.Time) int64 { return t.Unix() / Period }

// CodeAt computes the HOTP value for a step (RFC 4226 dynamic truncation).
func CodeAt(key []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // step is non-negative

	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[off])&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])

	return fmt.Sprintf("%0*d", Digits, bin%1000000)
}

// Generate returns the code for the secret at time t.
func Generate(secret string, t time.Time) (string, error) {
	key, err := DecodeSecret(secret)
	if err != nil {
		return "", err
	}

	return CodeAt(key, Step(t)), nil
}

// Validate checks code against secret at time now within ±Skew steps and
// rejects any step <= lastStep (replay protection). It returns the matched step.
func Validate(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != Digits {
		return 0, false
	}

	key, err := DecodeSecret(secret)
	if err != nil {
		return 0, false
	}

	cur := Step(now)
	for d := int64(-Skew); d <= Skew; d++ {
		s := cur + d
		if s <= lastStep {
			continue
		}

		if subtle.ConstantTimeCompare([]byte(CodeAt(key, s)), []byte(code)) == 1 {
			return s, true
		}
	}

	return 0, false
}

// ProvisioningURI builds an otpauth:// URI for QR codes.
func ProvisioningURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(Digits))
	q.Set("period", fmt.Sprint(Period))

	return "otpauth://totp/" + label + "?" + q.Encode()
}
