package twofactor

import (
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func init() { bcryptCost = 4 }

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err = db.AutoMigrate(&models.UserTwoFactor{}, &models.UserBackupCode{}); err != nil {
		t.Fatal(err)
	}
	old := config.C.EncryptedKey
	config.C.EncryptedKey = "unit-test-key-0123456789abcdef"
	t.Cleanup(func() { config.C.EncryptedKey = old })

	return db
}

func setClock(t *testing.T, ts time.Time) func(time.Duration) {
	t.Helper()
	cur := ts
	now = func() time.Time { return cur }
	t.Cleanup(func() { now = time.Now })

	return func(d time.Duration) { cur = cur.Add(d) }
}

func enable(t *testing.T, db *gorm.DB, uid uint64) (string, []string) {
	t.Helper()
	secret, err := BeginSetup(db, uid)
	if err != nil {
		t.Fatal(err)
	}
	if on, _ := IsEnabled(db, uid); on {
		t.Fatal("enabled before confirm")
	}
	if _, err = ConfirmSetup(db, uid, "000000x"); err != ErrInvalidCode {
		t.Fatalf("bad code: %v", err)
	}
	code, _ := Generate(secret, now())
	codes, err := ConfirmSetup(db, uid, code)
	if err != nil || len(codes) != BackupCodeCount {
		t.Fatalf("confirm: %v %d", err, len(codes))
	}

	return secret, codes
}

func TestSecretEncryptedAtRest(t *testing.T) {
	db := testDB(t)
	setClock(t, time.Unix(1_700_000_000, 0))
	secret, codes := enable(t, db, 7)
	row, _ := Load(db, 7)
	if row.Secret == "" || strings.Contains(row.Secret, secret) {
		t.Fatal("secret stored in plaintext")
	}
	var hashes []models.UserBackupCode
	db.Find(&hashes)
	for _, h := range hashes {
		for _, c := range codes {
			if strings.Contains(h.CodeHash, NormalizeBackupCode(c)) {
				t.Fatal("backup code stored in plaintext")
			}
		}
	}
}

func TestVerifyReplayAndBackupSingleUse(t *testing.T) {
	db := testDB(t)
	adv := setClock(t, time.Unix(1_700_000_000, 0))
	secret, codes := enable(t, db, 1)

	// the confirmation code itself cannot be replayed
	code, _ := Generate(secret, now())
	if err := Verify(db, 1, code); err != ErrInvalidCode {
		t.Fatalf("replay of setup code: %v", err)
	}
	adv(30 * time.Second)
	code, _ = Generate(secret, now())
	if err := Verify(db, 1, code); err != nil {
		t.Fatalf("fresh code: %v", err)
	}
	if err := Verify(db, 1, code); err != ErrInvalidCode {
		t.Fatalf("replay: %v", err)
	}

	if err := Verify(db, 1, strings.ToUpper(codes[0])); err != nil {
		t.Fatalf("backup code: %v", err)
	}
	if err := Verify(db, 1, codes[0]); err != ErrInvalidCode {
		t.Fatalf("backup reuse: %v", err)
	}
	if n, _ := RemainingBackupCodes(db, 1); n != BackupCodeCount-1 {
		t.Fatalf("remaining %d", n)
	}

	newCodes, err := RegenerateBackupCodes(db, 1)
	if err != nil || len(newCodes) != BackupCodeCount {
		t.Fatal(err)
	}
	if err := Verify(db, 1, codes[1]); err != ErrInvalidCode {
		t.Fatal("old backup code survived regeneration")
	}
	if err := Verify(db, 1, newCodes[1]); err != nil {
		t.Fatal(err)
	}
}

func TestLockout(t *testing.T) {
	db := testDB(t)
	adv := setClock(t, time.Unix(1_700_000_000, 0))
	secret, _ := enable(t, db, 2)
	adv(60 * time.Second)
	for range MaxFailures {
		if err := Verify(db, 2, "123456-bad"); err != ErrInvalidCode {
			t.Fatal(err)
		}
	}
	code, _ := Generate(secret, now())
	if err := Verify(db, 2, code); err != ErrLocked {
		t.Fatalf("expected lock, got %v", err)
	}
	adv(LockDuration + time.Second)
	code, _ = Generate(secret, now())
	if err := Verify(db, 2, code); err != nil {
		t.Fatalf("after lock: %v", err)
	}
}

func TestSecurityPINIsHashedOptionalAndSharesLockout(t *testing.T) {
	db := testDB(t)
	adv := setClock(t, time.Unix(1_700_000_000, 0))
	secret, codes := enable(t, db, 8)

	if _, err := NormalizeSecurityPIN("short"); err != ErrInvalidPIN {
		t.Fatalf("short pin: %v", err)
	}
	if _, err := NormalizeSecurityPIN("has space"); err != ErrInvalidPIN {
		t.Fatalf("spaces: %v", err)
	}
	if _, err := NormalizeSecurityPIN("pin-with-symbol"); err != ErrInvalidPIN {
		t.Fatalf("symbol: %v", err)
	}
	if err := SetSecurityPIN(db, 8, "nope"); err != ErrInvalidPIN {
		t.Fatalf("reject weak pin: %v", err)
	}
	if err := SetSecurityPIN(db, 99, "cafe42"); err != ErrNotEnabled {
		t.Fatalf("pin without 2FA: %v", err)
	}

	if err := SetSecurityPIN(db, 8, "cafe42"); err != nil {
		t.Fatal(err)
	}
	row, err := Load(db, 8)
	if err != nil || !row.Enabled() || !SecurityPINSet(row) {
		t.Fatal("setting a PIN must keep TOTP enabled", err)
	}
	if strings.Contains(row.PinHash, "cafe42") || !strings.HasPrefix(row.PinHash, "$2") {
		t.Fatal("PIN stored in plaintext")
	}

	adv(60 * time.Second)
	if err := Verify(db, 8, "cafe42"); err != nil {
		t.Fatalf("pin login: %v", err)
	}
	if err := Verify(db, 8, "  cafe42  "); err != nil {
		t.Fatalf("trimmed pin: %v", err)
	}
	if err := Verify(db, 8, "CAFE42"); err != ErrInvalidCode {
		t.Fatalf("pin is case-sensitive: %v", err)
	}
	// a 6-digit PIN is accepted when it is not the current TOTP code
	if err := SetSecurityPIN(db, 8, "135790"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Generate(secret, now()); got == "135790" {
		t.Fatal("fixture PIN collided with the current TOTP code")
	}
	if err := Verify(db, 8, "cafe42"); err != ErrInvalidCode {
		t.Fatal("old PIN still worked")
	}
	if err := Verify(db, 8, "135790"); err != nil {
		t.Fatalf("numeric pin: %v", err)
	}
	adv(30 * time.Second)
	code, _ := Generate(secret, now())
	if code == "135790" {
		t.Fatal("fixture PIN collided with the current TOTP code")
	}
	if err := Verify(db, 8, code); err != nil {
		t.Fatalf("totp still works: %v", err)
	}
	if err := Verify(db, 8, codes[0]); err != nil {
		t.Fatalf("backup still works: %v", err)
	}
	if err := Verify(db, 8, codes[0]); err != ErrInvalidCode {
		t.Fatal("backup code became reusable")
	}
	stepBefore, _ := Load(db, 8)
	if err := Verify(db, 8, "135790"); err != nil {
		t.Fatal(err)
	}
	stepAfter, _ := Load(db, 8)
	if stepAfter.LastUsedStep != stepBefore.LastUsedStep {
		t.Fatal("PIN use must not consume a TOTP step")
	}

	for range MaxFailures - 1 {
		if err := Verify(db, 8, "notthepin"); err != ErrInvalidCode {
			t.Fatal(err)
		}
	}
	if err := Verify(db, 8, "135790"); err != nil {
		t.Fatalf("pin before lockout: %v", err)
	}
	for range MaxFailures {
		if err := Verify(db, 8, "wrongpin"); err != ErrInvalidCode {
			t.Fatal(err)
		}
	}
	if err := Verify(db, 8, "135790"); err != ErrLocked {
		t.Fatalf("pin must share the 2FA lockout, got %v", err)
	}
	adv(LockDuration + time.Second)
	if err := RemoveSecurityPIN(db, 8); err != nil {
		t.Fatal(err)
	}
	if row, _ = Load(db, 8); !row.Enabled() || SecurityPINSet(row) {
		t.Fatal("removing the PIN disabled TOTP or left the hash")
	}
	if err := Verify(db, 8, "135790"); err != ErrInvalidCode {
		t.Fatalf("removed pin: %v", err)
	}
	if err := RemoveSecurityPIN(db, 8); err != ErrPINNotSet {
		t.Fatalf("second remove: %v", err)
	}
	if err := AdminReset(db, 8); err != nil {
		t.Fatal(err)
	}
	if on, _ := IsEnabled(db, 8); on {
		t.Fatal("reset left 2FA enabled")
	}
}

func TestDisableImportAndChallenge(t *testing.T) {
	db := testDB(t)
	setClock(t, time.Unix(1_700_000_000, 0))
	enable(t, db, 3)
	if err := AdminReset(db, 3); err != nil {
		t.Fatal(err)
	}
	if on, _ := IsEnabled(db, 3); on {
		t.Fatal("still enabled")
	}

	if err := ImportSecret(db, 4, "jbswy3dpehpk3pxp", []string{"abcd1234"}); err != nil {
		t.Fatal(err)
	}
	code, _ := Generate("JBSWY3DPEHPK3PXP", now())
	if err := Verify(db, 4, code); err != nil {
		t.Fatal(err)
	}
	if err := Verify(db, 4, "ABCD-1234"); err != nil {
		t.Fatal(err)
	}

	tok := IssueChallenge(99)
	if uid, err := ParseChallenge(tok); err != nil || uid != 99 {
		t.Fatal(uid, err)
	}
	if _, err := ParseChallenge(tok[:len(tok)-2] + "AA"); err == nil {
		t.Fatal("tampered challenge accepted")
	}
	now = func() time.Time { return time.Unix(1_700_000_000, 0).Add(ChallengeTTL + time.Second) }
	if _, err := ParseChallenge(tok); err == nil {
		t.Fatal("expired challenge accepted")
	}
}
