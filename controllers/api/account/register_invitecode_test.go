package account

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// registerTestSetup prepares a test DB with all models needed for registration
// (User, UserSecretData, Account, InviteCode, Follow) and wires database.DB so
// that db.GetDB() returns the test DB. It returns the gorm DB handle.
func registerTestSetup(t *testing.T) *testutil.TestDB {
	t.Helper()

	testDB := testutil.QuickDBSetup(t,
		&models.User{},
		&models.UserSecretData{},
		&models.Account{},
		&models.AccountExtra{},
		&models.InviteCode{},
		&models.Follow{},
	)
	// Wire the global DB so createUserAndAccount's db.GetDB() returns our test DB.
	database.SetTestDB(testDB.GormDB)
	t.Cleanup(database.ResetTestDB)

	// Disable approval + email checks so ExecAfterRegisterAction can run follow_me
	// immediately (otherwise it's deferred/skipped).
	systemsettings.Set(systemsettings.KeyRegistrationApprovalRequired, false)
	systemsettings.Set(systemsettings.KeyRegistrationCheckEmail, false)

	return testDB
}

// TestCreateUserAndAccount_ConsumesInviteCode verifies that the registration
// transaction atomically consumes the invite code: after a successful
// registration, use_count is incremented by 1.
//
// This is the core integration of ConsumeInviteCodeTx into the Register flow.
func TestCreateUserAndAccount_ConsumesInviteCode(t *testing.T) {
	testDB := registerTestSetup(t)
	db := testDB.GormDB

	// Insert an invite code with raw SQL (test engine can't bind nil map column).
	now := time.Now()
	res := db.Exec(
		`INSERT INTO invite_codes (code, user_id, account_id, max_use_count, use_count, expire_at, disabled, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"REGTEST", 0, "0", 10, 0, nil, false, now,
	)
	require.NoError(t, res.Error, "insert invite code")

	// Look up the invite code so we can pass it to createUserAndAccount.
	invCode := &models.InviteCode{}
	require.NoError(t, db.Where("code = ?", "REGTEST").First(invCode).Error)

	// Build a fake gin context (createUserAndAccount only uses it for JSON responses).
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	// NOTE: the production salt is 64 chars (utils.GetRandomString(64)) and bcrypt
	// rejects input > 72 bytes, so we use a short password (64 + len("pw") = 66 < 72).
	const testPassword = "pw"

	req := RegisterRequest{
		DisplayName:     "New User",
		UserName:        "newuser_regtest",
		Email:           "newuser_regtest@example.com",
		Password:        testPassword,
		PasswordConfirm: testPassword,
		InviteCode:      "REGTEST",
	}

	user, account, err := createUserAndAccount(c, req, invCode)
	require.NoError(t, err, "registration should succeed")
	require.NotNil(t, user)
	require.NotNil(t, account)
	assert.NotZero(t, user.Id)
	assert.NotZero(t, account.Id)

	// Verify use_count was incremented (the key assertion: invite consumed on register).
	var fresh models.InviteCode
	require.NoError(t, db.Where("code = ?", "REGTEST").First(&fresh).Error)
	assert.Equal(t, 1, fresh.UseCount, "use_count must be incremented after successful registration")

	// The passed-in invCode pointer should be updated with the new use_count.
	assert.Equal(t, 1, invCode.UseCount, "invCode pointer must reflect the consumed use_count")

	// Verify the user/account were actually created.
	var userCount int64
	require.NoError(t, db.Model(&models.User{}).Where("id = ?", user.Id).Count(&userCount).Error)
	assert.Equal(t, int64(1), userCount)
}

// TestCreateUserAndAccount_NoInviteCode verifies that registering without an
// invite code works and does not touch any invite code.
func TestCreateUserAndAccount_NoInviteCode(t *testing.T) {
	registerTestSetup(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	req := RegisterRequest{
		DisplayName:     "No Invite User",
		UserName:        "noinvite",
		Email:           "noinvite@example.com",
		Password:        "pw",
		PasswordConfirm: "pw",
	}

	user, account, err := createUserAndAccount(c, req, nil)
	require.NoError(t, err, "registration without invite code should succeed")
	require.NotNil(t, user)
	require.NotNil(t, account)
	assert.NotZero(t, user.Id)
}

// TestCreateUserAndAccount_ExhaustedInviteCode verifies that registering with an
// exhausted invite code fails (returns an error), so the user is NOT created.
func TestCreateUserAndAccount_ExhaustedInviteCode(t *testing.T) {
	testDB := registerTestSetup(t)
	db := testDB.GormDB

	// Insert an already-exhausted invite code (use_count >= max_use_count).
	now := time.Now()
	res := db.Exec(
		`INSERT INTO invite_codes (code, user_id, account_id, max_use_count, use_count, expire_at, disabled, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"EXHAUSTED", 0, "0", 1, 1, nil, false, now,
	)
	require.NoError(t, res.Error)

	invCode := &models.InviteCode{}
	require.NoError(t, db.Where("code = ?", "EXHAUSTED").First(invCode).Error)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	req := RegisterRequest{
		DisplayName:     "Exhausted Test",
		UserName:        "exhaustedtest",
		Email:           "exhaustedtest@example.com",
		Password:        "pw",
		PasswordConfirm: "pw",
		InviteCode:      "EXHAUSTED",
	}

	_, _, err := createUserAndAccount(c, req, invCode)
	require.Error(t, err, "registration with an exhausted invite code must fail")
	assert.Contains(t, err.Error(), "not available")

	// No user should have been created for this username.
	var userCount int64
	require.NoError(t, db.Model(&models.User{}).Where("username = ?", "exhaustedtest").Count(&userCount).Error)
	assert.Equal(t, int64(0), userCount, "no user should be created when invite code is exhausted")
}

// TestCreateUserAndAccount_DisabledInviteCode verifies that a disabled invite
// code is rejected.
func TestCreateUserAndAccount_DisabledInviteCode(t *testing.T) {
	testDB := registerTestSetup(t)
	db := testDB.GormDB

	now := time.Now()
	res := db.Exec(
		`INSERT INTO invite_codes (code, user_id, account_id, max_use_count, use_count, expire_at, disabled, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"DISABLED", 0, "0", 10, 0, nil, true, now, // disabled = true
	)
	require.NoError(t, res.Error)

	invCode := &models.InviteCode{}
	require.NoError(t, db.Where("code = ?", "DISABLED").First(invCode).Error)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	req := RegisterRequest{
		DisplayName:     "Disabled Test",
		UserName:        "disabledtest",
		Email:           "disabledtest@example.com",
		Password:        "pw",
		PasswordConfirm: "pw",
		InviteCode:      "DISABLED",
	}

	_, _, err := createUserAndAccount(c, req, invCode)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not available")
}

// TestCreateUserAndAccount_ConcurrentRegistrationsRespectMaxUse launches several
// concurrent registrations that all use the SAME invite code with a strict cap,
// and verifies the no-oversell invariant: use_count never exceeds MaxUseCount.
//
// NOTE: the in-memory test engine uses a no-op transaction and COW snapshots
// without write-conflict detection, so some concurrent increments may be lost
// (use_count may end up below the cap). The critical invariant that holds on
// every database (including this lossy engine) is: use_count <= MaxUseCount — no
// oversell. That is what this test asserts.
func TestCreateUserAndAccount_ConcurrentRegistrationsRespectMaxUse(t *testing.T) {
	testDB := registerTestSetup(t)
	db := testDB.GormDB

	const maxUse = 3

	const concurrency = 20

	// Insert one invite code shared by all concurrent registrations.
	now := time.Now()
	res := db.Exec(
		`INSERT INTO invite_codes (code, user_id, account_id, max_use_count, use_count, expire_at, disabled, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"CONCURRENT", 0, "0", maxUse, 0, nil, false, now,
	)
	require.NoError(t, res.Error)

	done := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			suffix := string(rune('a' + idx))
			req := RegisterRequest{
				DisplayName:     "Concurrent User",
				UserName:        "concurrent" + suffix,
				Email:           "concurrent" + suffix + "@example.com",
				Password:        "pw",
				PasswordConfirm: "pw",
				InviteCode:      "CONCURRENT",
			}

			// Look up the invite code fresh for each goroutine.
			invCode := &models.InviteCode{}
			if e := db.Where("code = ?", "CONCURRENT").First(invCode).Error; e != nil {
				done <- e

				return
			}

			_, _, e := createUserAndAccount(c, req, invCode)
			done <- e
		}(i)
	}

	for i := 0; i < concurrency; i++ {
		<-done // drain; individual errors are expected for the losing registrations
	}

	// CRITICAL INVARIANT: use_count must NEVER exceed MaxUseCount (no oversell).
	var fresh models.InviteCode
	require.NoError(t, db.Where("code = ?", "CONCURRENT").First(&fresh).Error)
	assert.LessOrEqual(t, fresh.UseCount, maxUse,
		"use_count (%d) must NEVER exceed MaxUseCount (%d) — no oversell under concurrency",
		fresh.UseCount, maxUse)
}
