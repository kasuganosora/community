package twofactor

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BackupCodeCount = 10
	// MaxFailures failed codes in a row lock verification for LockDuration.
	MaxFailures  = 5
	LockDuration = 15 * time.Minute
	pendingTTL   = 30 * time.Minute
	ChallengeTTL = 5 * time.Minute
)

var (
	ErrNotEnabled     = errors.New("two-factor authentication is not enabled")
	ErrAlreadyEnabled = errors.New("two-factor authentication is already enabled")
	ErrNoPending      = errors.New("no pending two-factor setup")
	ErrInvalidCode    = errors.New("invalid two-factor code")
	ErrLocked         = errors.New("too many invalid two-factor codes, try again later")
	ErrBadChallenge   = errors.New("invalid or expired two-factor challenge")
)

// now is replaceable in tests.
var now = time.Now

// bcryptCost is lowered in tests.
var bcryptCost = bcrypt.DefaultCost

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

// Load returns the user's 2FA row or nil.
func Load(db *gorm.DB, userID uint64) (*models.UserTwoFactor, error) {
	var row models.UserTwoFactor
	err := db.Where("user_id = ?", userID).Limit(1).Find(&row).Error
	if err != nil {
		return nil, err
	}

	if row.UserId == 0 {
		return nil, nil //nolint:nilnil
	}

	return &row, nil
}

// IsEnabled reports whether the user must pass a second factor. Errors fail closed.
func IsEnabled(db *gorm.DB, userID uint64) (bool, error) {
	row, err := Load(db, userID)
	if err != nil {
		return true, err
	}

	return row.Enabled(), nil
}

func encrypt(secret, salt string) (string, error) { return models.EncryptSecretValue(secret, salt) }

// BeginSetup creates (or replaces) a pending secret and returns it in plain
// base32. TOTP stays disabled until ConfirmSetup.
func BeginSetup(db *gorm.DB, userID uint64) (string, error) {
	row, err := Load(db, userID)
	if err != nil {
		return "", err
	}

	if row.Enabled() {
		return "", ErrAlreadyEnabled
	}

	if row == nil {
		row = &models.UserTwoFactor{UserId: userID}
	}

	if row.Salt == "" {
		row.Salt = randomHex(16)
	}

	secret, err := GenerateSecret()
	if err != nil {
		return "", err
	}

	enc, err := encrypt(secret, row.Salt)
	if err != nil {
		return "", err
	}

	t := now()
	row.PendingSecret, row.PendingCreatedAt = enc, &t

	return secret, db.Save(row).Error
}

// ConfirmSetup activates the pending secret when code matches and returns
// freshly generated backup codes (shown to the user once).
func ConfirmSetup(db *gorm.DB, userID uint64, code string) ([]string, error) {
	var codes []string

	err := db.Transaction(func(tx *gorm.DB) error {
		row, err := Load(tx.Clauses(clause.Locking{Strength: "UPDATE"}), userID)
		if err != nil {
			return err
		}

		if row.Enabled() {
			return ErrAlreadyEnabled
		}

		if row == nil || row.PendingSecret == "" || row.PendingCreatedAt == nil || now().Sub(*row.PendingCreatedAt) > pendingTTL {
			return ErrNoPending
		}

		secret, err := models.DecryptSecretValue(row.PendingSecret, row.Salt)
		if err != nil {
			return err
		}

		step, ok := Validate(secret, code, now(), 0)
		if !ok {
			return ErrInvalidCode
		}

		t := now()
		row.Secret, row.PendingSecret, row.PendingCreatedAt = row.PendingSecret, "", nil
		row.EnabledAt, row.LastUsedStep, row.FailedAttempts, row.LockedUntil = &t, step, 0, nil

		if err = tx.Save(row).Error; err != nil {
			return err
		}

		codes, err = replaceBackupCodes(tx, userID, nil)

		return err
	})

	return codes, err
}

// GenerateBackupCodes returns n random codes formatted xxxxx-xxxxx.
func GenerateBackupCodes(n int) []string {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	out := make([]string, n)
	buf := make([]byte, 10)

	for i := range out {
		_, _ = rand.Read(buf)
		var sb strings.Builder
		for j, b := range buf {
			if j == 5 {
				sb.WriteByte('-')
			}
			sb.WriteByte(alphabet[int(b)%len(alphabet)])
		}
		out[i] = sb.String()
	}

	return out
}

// NormalizeBackupCode strips separators and whitespace and lowercases.
func NormalizeBackupCode(code string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", "-", "", "\t", "").Replace(strings.TrimSpace(code)))
}

// replaceBackupCodes deletes existing codes and stores hashes of codes
// (generating BackupCodeCount new ones when codes is nil).
func replaceBackupCodes(tx *gorm.DB, userID uint64, codes []string) ([]string, error) {
	if codes == nil {
		codes = GenerateBackupCodes(BackupCodeCount)
	}

	if err := tx.Where("user_id = ?", userID).Delete(&models.UserBackupCode{}).Error; err != nil {
		return nil, err
	}

	rows := make([]models.UserBackupCode, 0, len(codes))
	for _, c := range codes {
		n := NormalizeBackupCode(c)
		if n == "" {
			continue
		}

		h, err := bcrypt.GenerateFromPassword([]byte(n), bcryptCost)
		if err != nil {
			return nil, err
		}

		rows = append(rows, models.UserBackupCode{UserId: userID, CodeHash: string(h)})
	}

	if len(rows) > 0 {
		if err := tx.Create(&rows).Error; err != nil {
			return nil, err
		}
	}

	return codes, nil
}

// RegenerateBackupCodes invalidates all old codes and returns new ones.
func RegenerateBackupCodes(db *gorm.DB, userID uint64) ([]string, error) {
	var codes []string

	err := db.Transaction(func(tx *gorm.DB) error {
		row, err := Load(tx, userID)
		if err != nil {
			return err
		}

		if !row.Enabled() {
			return ErrNotEnabled
		}

		codes, err = replaceBackupCodes(tx, userID, nil)

		return err
	})

	return codes, err
}

// RemainingBackupCodes counts unused codes.
func RemainingBackupCodes(db *gorm.DB, userID uint64) (int64, error) {
	var n int64
	err := db.Model(&models.UserBackupCode{}).Where("user_id = ? AND used_at IS NULL", userID).Count(&n).Error

	return n, err
}

// Disable removes TOTP and backup codes (caller verifies password + code).
func Disable(db *gorm.DB, userID uint64) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&models.UserBackupCode{}).Error; err != nil {
			return err
		}

		return tx.Where("user_id = ?", userID).Delete(&models.UserTwoFactor{}).Error
	})
}

// AdminReset is Disable for operators; it does not require any code.
func AdminReset(db *gorm.DB, userID uint64) error { return Disable(db, userID) }

// Verify checks a TOTP code (6 digits) or a backup code. It enforces
// replay protection, single use for backup codes and lockout after
// MaxFailures consecutive failures.
func Verify(db *gorm.DB, userID uint64, code string) error {
	row, err := Load(db, userID)
	if err != nil {
		return err
	}

	if !row.Enabled() {
		return ErrNotEnabled
	}

	t := now()
	if row.LockedUntil != nil && t.Before(*row.LockedUntil) {
		return ErrLocked
	}

	trimmed := strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if isDigits(trimmed) && len(trimmed) == Digits {
		secret, derr := models.DecryptSecretValue(row.Secret, row.Salt)
		if derr != nil {
			return derr
		}

		if step, ok := Validate(secret, trimmed, t, row.LastUsedStep); ok {
			res := db.Model(&models.UserTwoFactor{}).
				Where("user_id = ? AND last_used_step < ?", userID, step).
				Updates(map[string]any{"last_used_step": step, "failed_attempts": 0, "locked_until": nil})
			if res.Error != nil {
				return res.Error
			}

			if res.RowsAffected == 1 {
				return nil
			}
		}
	} else if ok, uerr := useBackupCode(db, userID, trimmed); uerr != nil {
		return uerr
	} else if ok {
		return db.Model(&models.UserTwoFactor{}).Where("user_id = ?", userID).
			Updates(map[string]any{"failed_attempts": 0, "locked_until": nil}).Error
	}

	return recordFailure(db, row, t)
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return s != ""
}

func useBackupCode(db *gorm.DB, userID uint64, code string) (bool, error) {
	n := NormalizeBackupCode(code)
	if n == "" || len(n) > 64 {
		return false, nil
	}

	var rows []models.UserBackupCode
	if err := db.Where("user_id = ? AND used_at IS NULL", userID).Find(&rows).Error; err != nil {
		return false, err
	}

	for _, r := range rows {
		if bcrypt.CompareHashAndPassword([]byte(r.CodeHash), []byte(n)) == nil {
			res := db.Model(&models.UserBackupCode{}).Where("id = ? AND used_at IS NULL", r.Id).Update("used_at", now())
			if res.Error != nil {
				return false, res.Error
			}

			return res.RowsAffected == 1, nil
		}
	}

	return false, nil
}

func recordFailure(db *gorm.DB, row *models.UserTwoFactor, t time.Time) error {
	fails := row.FailedAttempts + 1
	updates := map[string]any{"failed_attempts": gorm.Expr("failed_attempts + 1")}

	if fails >= MaxFailures {
		lock := t.Add(LockDuration)
		updates = map[string]any{"failed_attempts": 0, "locked_until": lock}
	}

	if err := db.Model(&models.UserTwoFactor{}).Where("user_id = ?", row.UserId).Updates(updates).Error; err != nil {
		return err
	}

	return ErrInvalidCode
}

// ImportSecret installs an already-active TOTP secret plus plaintext backup
// codes (hashed here), e.g. from a Misskey migration. Existing state is
// replaced.
func ImportSecret(tx *gorm.DB, userID uint64, secret string, backupCodes []string) error {
	norm, err := NormalizeSecret(secret)
	if err != nil {
		return err
	}

	salt := randomHex(16)
	enc, err := encrypt(norm, salt)
	if err != nil {
		return err
	}

	t := now()
	row := &models.UserTwoFactor{UserId: userID, Salt: salt, Secret: enc, EnabledAt: &t}
	if err = tx.Where("user_id = ?", userID).Delete(&models.UserTwoFactor{}).Error; err != nil {
		return err
	}

	if err = tx.Create(row).Error; err != nil {
		return err
	}

	if backupCodes == nil {
		backupCodes = []string{}
	}

	_, err = replaceBackupCodes(tx, userID, backupCodes)

	return err
}

// --- login challenges --------------------------------------------------

var (
	fallbackKeyOnce sync.Once
	fallbackKey     []byte
)

func challengeKey() []byte {
	if config.C.EncryptedKey != "" {
		m := hmac.New(sha256.New, []byte(config.C.EncryptedKey))
		m.Write([]byte("community-2fa-challenge-v1"))

		return m.Sum(nil)
	}

	fallbackKeyOnce.Do(func() {
		fallbackKey = make([]byte, 32)
		_, _ = rand.Read(fallbackKey)
	})

	return fallbackKey
}

// IssueChallenge returns an opaque, signed token proving the password step
// succeeded for userID. It is valid for ChallengeTTL.
func IssueChallenge(userID uint64) string {
	buf := make([]byte, 8+8+8)
	binary.BigEndian.PutUint64(buf[0:], userID)
	binary.BigEndian.PutUint64(buf[8:], uint64(now().Add(ChallengeTTL).Unix())) //nolint:gosec
	_, _ = rand.Read(buf[16:])

	m := hmac.New(sha256.New, challengeKey())
	m.Write(buf)

	return base64.RawURLEncoding.EncodeToString(append(buf, m.Sum(nil)...))
}

// ParseChallenge validates a token from IssueChallenge and returns the user id.
func ParseChallenge(token string) (uint64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 24+sha256.Size {
		return 0, ErrBadChallenge
	}

	m := hmac.New(sha256.New, challengeKey())
	m.Write(raw[:24])
	if !hmac.Equal(m.Sum(nil), raw[24:]) {
		return 0, ErrBadChallenge
	}

	if int64(binary.BigEndian.Uint64(raw[8:16])) < now().Unix() { //nolint:gosec
		return 0, ErrBadChallenge
	}

	return binary.BigEndian.Uint64(raw[0:8]), nil
}
