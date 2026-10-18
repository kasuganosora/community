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
