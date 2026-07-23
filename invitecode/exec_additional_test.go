package invitecode

import (
	"testing"

	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// initSystemsettingsForTest ensures systemsettings is initialized for testing.
// It sets database.DB to a test DB so Load() won't panic, then sets key-value pairs.
// If db is nil, a fresh test DB is created.
func initSystemsettingsForTest(t *testing.T, db *gorm.DB, keyvals map[string]any) {
	t.Helper()

	if db == nil {
		testDB := testutil.QuickDBSetup(t)
		db = testDB.GormDB

		t.Cleanup(func() {
			database.DB = nil
		})
	}

	database.DB = db

	for k, v := range keyvals {
		systemsettings.Set(k, v)
	}
}

// TestExecAfterRegisterAction_NilInviteCode tests that nil invite code returns immediately.
func TestExecAfterRegisterAction_NilInviteCode(t *testing.T) {
	assert.NotPanics(t, func() {
		ExecAfterRegisterAction(nil, nil, nil, nil)
	})
}

// TestExecAfterRegisterAction_EmptyAction tests that empty action returns immediately.
func TestExecAfterRegisterAction_EmptyAction(t *testing.T) {
	inv := &models.InviteCode{
		AfterRegisterAction: "",
	}

	assert.NotPanics(t, func() {
		ExecAfterRegisterAction(nil, inv, nil, nil)
	})
}

// TestExecAfterRegisterAction_UnknownAction tests that unknown action is handled gracefully.
func TestExecAfterRegisterAction_UnknownAction(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})

	initSystemsettingsForTest(t, db.GormDB, map[string]any{
		systemsettings.KeyRegistrationApprovalRequired: false,
		systemsettings.KeyRegistrationCheckEmail:       false,
	})

	inv := &models.InviteCode{
		Id:                  1,
		AfterRegisterAction: "unknown_action",
	}
	user := &models.User{
		Id:     1,
		Status: models.UserStatusNormal,
	}

	// Should not panic on unknown action - logs error in default case
	assert.NotPanics(t, func() {
		ExecAfterRegisterAction(db.GormDB, inv, user, nil)
	})
}

// TestExecAfterRegisterAction_UserUnapproved tests that unapproved user skips execution.
func TestExecAfterRegisterAction_UserUnapproved(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})

	initSystemsettingsForTest(t, db.GormDB, map[string]any{
		systemsettings.KeyRegistrationApprovalRequired: true,
		systemsettings.KeyRegistrationCheckEmail:       false,
	})

	inv := &models.InviteCode{
		Id:                  1,
		AfterRegisterAction: InviteCodeAfterRegisterActionFollowMe,
	}
	user := &models.User{
		Id:     1,
		Status: models.UserStatusUnapproved,
	}

	// Should skip because user is not approved
	assert.NotPanics(t, func() {
		ExecAfterRegisterAction(db.GormDB, inv, user, nil)
	})
}

// TestExecAfterRegisterAction_UserUnverifiedMail tests that unverified email user skips execution.
func TestExecAfterRegisterAction_UserUnverifiedMail(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})

	initSystemsettingsForTest(t, db.GormDB, map[string]any{
		systemsettings.KeyRegistrationApprovalRequired: false,
		systemsettings.KeyRegistrationCheckEmail:       true,
	})

	inv := &models.InviteCode{
		Id:                  1,
		AfterRegisterAction: InviteCodeAfterRegisterActionFollowMe,
	}
	user := &models.User{
		Id:     1,
		Status: models.UserStatusUnverifiedMail,
	}

	// Should skip because email is not verified
	assert.NotPanics(t, func() {
		ExecAfterRegisterAction(db.GormDB, inv, user, nil)
	})
}

// TestExecAfterRegisterAction_UserUnapproved_ApprovalDisabled tests that unapproved user
// passes through when approval is not required, reaching followMe where owner is not found.
func TestExecAfterRegisterAction_UserUnapproved_ApprovalDisabled(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})

	initSystemsettingsForTest(t, db.GormDB, map[string]any{
		systemsettings.KeyRegistrationApprovalRequired: false,
		systemsettings.KeyRegistrationCheckEmail:       false,
	})

	inv := &models.InviteCode{
		Id:                  1,
		AfterRegisterAction: InviteCodeAfterRegisterActionFollowMe,
		UserId:              9999, // non-existent owner
	}
	user := &models.User{
		Id:     1,
		Status: models.UserStatusUnapproved,
	}
	account := &models.Account{
		Id: 1,
	}

	// Should not panic even if owner is not found (followMe handles the error)
	assert.NotPanics(t, func() {
		ExecAfterRegisterAction(db.GormDB, inv, user, account)
	})
}

// TestExecAfterRegisterAction_FollowMe_OwnerNotFound tests followMe when owner account not found.
func TestExecAfterRegisterAction_FollowMe_OwnerNotFound(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})

	initSystemsettingsForTest(t, db.GormDB, map[string]any{
		systemsettings.KeyRegistrationApprovalRequired: false,
		systemsettings.KeyRegistrationCheckEmail:       false,
	})

	inv := &models.InviteCode{
		Id:                  1,
		AfterRegisterAction: InviteCodeAfterRegisterActionFollowMe,
		UserId:              9999, // non-existent user
	}
	user := &models.User{
		Id:     1,
		Status: models.UserStatusNormal,
	}
	account := &models.Account{
		Id: 1,
	}

	// Should not panic - owner not found, should log error and return
	assert.NotPanics(t, func() {
		ExecAfterRegisterAction(db.GormDB, inv, user, account)
	})
}

// TestExecAfterRegisterAction_FollowMe_OwnerIsBot tests followMe when owner is a bot account.
func TestExecAfterRegisterAction_FollowMe_OwnerIsBot(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})

	initSystemsettingsForTest(t, db.GormDB, map[string]any{
		systemsettings.KeyRegistrationApprovalRequired: false,
		systemsettings.KeyRegistrationCheckEmail:       false,
	})

	fixtures := testutil.NewFixtureBuilder(t, db.GormDB)
	ownerUser := fixtures.CreateUser(func(u *models.User) {
		u.UserName = "ownerbot"
		u.EmailHash = "ownerbot_hash"
	})
	fixtures.CreateAccount(ownerUser, func(a *models.Account) {
		a.Username = "ownerbot"
		a.DisplayName = "Owner Bot"
		a.Bot = true
	})

	inv := &models.InviteCode{
		Id:                  1,
		AfterRegisterAction: InviteCodeAfterRegisterActionFollowMe,
		UserId:              ownerUser.Id,
	}
	user := &models.User{
		Id:     2,
		Status: models.UserStatusNormal,
	}
	account := &models.Account{
		Id: 2,
	}

	// Should not panic - bot account is filtered out, owner not found
	assert.NotPanics(t, func() {
		ExecAfterRegisterAction(db.GormDB, inv, user, account)
	})
}

// TestExecAfterRegisterAction_FollowMe_Success tests successful follow creation.
func TestExecAfterRegisterAction_FollowMe_Success(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})

	initSystemsettingsForTest(t, db.GormDB, map[string]any{
		systemsettings.KeyRegistrationApprovalRequired: false,
		systemsettings.KeyRegistrationCheckEmail:       false,
	})

	fixtures := testutil.NewFixtureBuilder(t, db.GormDB)
	ownerUser := fixtures.CreateUser(func(u *models.User) {
		u.UserName = "owneruser"
		u.EmailHash = "owneruser_hash"
	})
	ownerAccount := fixtures.CreateAccount(ownerUser, func(a *models.Account) {
		a.Username = "owneruser"
		a.DisplayName = "Owner User" //nolint:goconst
		a.Bot = false
	})

	newUser := fixtures.CreateUser(func(u *models.User) {
		u.UserName = "newuser" //nolint:goconst
		u.EmailHash = "newuser_hash"
	})
	newAccount := fixtures.CreateAccount(newUser, func(a *models.Account) {
		a.Username = "newuser"
		a.DisplayName = "New User"
	})

	inv := &models.InviteCode{
		Id:                  1,
		AfterRegisterAction: InviteCodeAfterRegisterActionFollowMe,
		UserId:              ownerUser.Id,
	}

	// Execute the after register action
	ExecAfterRegisterAction(db.GormDB, inv, newUser, newAccount)

	// Verify follow relationship was created
	var follow models.Follow

	err := db.GormDB.Where("account_id = ? AND follow_id = ?", newAccount.Id, ownerAccount.Id).First(&follow).Error
	require.NoError(t, err, "follow relationship should have been created")
	assert.Equal(t, newAccount.Id, follow.AccountId)
	assert.Equal(t, ownerAccount.Id, follow.FollowId)
}

// TestExecAfterRegisterAction_FollowMe_NilDB tests followMe with nil database
// This confirms that a nil DB panics when followMe is reached.
func TestExecAfterRegisterAction_FollowMe_NilDB(t *testing.T) {
	initSystemsettingsForTest(t, nil, map[string]any{
		systemsettings.KeyRegistrationApprovalRequired: false,
		systemsettings.KeyRegistrationCheckEmail:       false,
	})

	inv := &models.InviteCode{
		Id:                  1,
		AfterRegisterAction: InviteCodeAfterRegisterActionFollowMe,
		UserId:              1,
	}
	user := &models.User{
		Id:     1,
		Status: models.UserStatusNormal,
	}
	account := &models.Account{
		Id: 1,
	}

	// nil DB will panic when followMe tries to call db.Where()
	assert.Panics(t, func() {
		ExecAfterRegisterAction(nil, inv, user, account)
	})
}

// TestExecAfterRegisterAction_ApprovalEnabled_UserNormal tests that normal user passes
// the approval check when approval is required.
func TestExecAfterRegisterAction_ApprovalEnabled_UserNormal(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})

	initSystemsettingsForTest(t, db.GormDB, map[string]any{
		systemsettings.KeyRegistrationApprovalRequired: true,
		systemsettings.KeyRegistrationCheckEmail:       false,
	})

	inv := &models.InviteCode{
		Id:                  1,
		AfterRegisterAction: InviteCodeAfterRegisterActionFollowMe,
		UserId:              9999,
	}
	user := &models.User{
		Id:     1,
		Status: models.UserStatusNormal,
	}
	account := &models.Account{
		Id: 1,
	}

	// User is normal (approved), should proceed to followMe which will handle owner not found
	assert.NotPanics(t, func() {
		ExecAfterRegisterAction(db.GormDB, inv, user, account)
	})
}

// TestExecAfterRegisterAction_EmailCheckEnabled_UserNormal tests that normal user passes
// the email check when email verification is required.
func TestExecAfterRegisterAction_EmailCheckEnabled_UserNormal(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})

	initSystemsettingsForTest(t, db.GormDB, map[string]any{
		systemsettings.KeyRegistrationApprovalRequired: false,
		systemsettings.KeyRegistrationCheckEmail:       true,
	})

	inv := &models.InviteCode{
		Id:                  1,
		AfterRegisterAction: InviteCodeAfterRegisterActionFollowMe,
		UserId:              9999,
	}
	user := &models.User{
		Id:     1,
		Status: models.UserStatusNormal,
	}
	account := &models.Account{
		Id: 1,
	}

	// User is normal (verified), should proceed to followMe
	assert.NotPanics(t, func() {
		ExecAfterRegisterAction(db.GormDB, inv, user, account)
	})
}

// TestExecAfterRegisterAction_UserRejected tests that rejected user is still processed
// when approval is not required.
func TestExecAfterRegisterAction_UserRejected(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})

	initSystemsettingsForTest(t, db.GormDB, map[string]any{
		systemsettings.KeyRegistrationApprovalRequired: false,
		systemsettings.KeyRegistrationCheckEmail:       false,
	})

	inv := &models.InviteCode{
		Id:                  1,
		AfterRegisterAction: InviteCodeAfterRegisterActionFollowMe,
		UserId:              9999,
	}
	user := &models.User{
		Id:     1,
		Status: models.UserStatusRejected,
	}
	account := &models.Account{
		Id: 1,
	}

	// Rejected user with no approval required should proceed to followMe
	assert.NotPanics(t, func() {
		ExecAfterRegisterAction(db.GormDB, inv, user, account)
	})
}

// TestExecAfterRegisterAction_BothChecksEnabled tests with both approval and email checks enabled.
func TestExecAfterRegisterAction_BothChecksEnabled(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})

	initSystemsettingsForTest(t, db.GormDB, map[string]any{
		systemsettings.KeyRegistrationApprovalRequired: true,
		systemsettings.KeyRegistrationCheckEmail:       true,
	})

	inv := &models.InviteCode{
		Id:                  1,
		AfterRegisterAction: InviteCodeAfterRegisterActionFollowMe,
		UserId:              9999,
	}
	user := &models.User{
		Id:     1,
		Status: models.UserStatusNormal,
	}

	// Normal user passes both checks
	assert.NotPanics(t, func() {
		ExecAfterRegisterAction(db.GormDB, inv, user, nil)
	})
}

// TestExecAfterRegisterAction_FollowMe_CreateFollowError tests when creating follow fails
// by using a DB without the Follow table migrated.
func TestExecAfterRegisterAction_FollowMe_CreateFollowError(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}) // No Follow table

	initSystemsettingsForTest(t, db.GormDB, map[string]any{
		systemsettings.KeyRegistrationApprovalRequired: false,
		systemsettings.KeyRegistrationCheckEmail:       false,
	})

	fixtures := testutil.NewFixtureBuilder(t, db.GormDB)
	ownerUser := fixtures.CreateUser(func(u *models.User) {
		u.UserName = "ownernofollow"
		u.EmailHash = "ownernofollow_hash"
	})
	fixtures.CreateAccount(ownerUser, func(a *models.Account) {
		a.Username = "ownernofollow"
		a.DisplayName = "Owner No Follow"
		a.Bot = false
	})

	inv := &models.InviteCode{
		Id:                  1,
		AfterRegisterAction: InviteCodeAfterRegisterActionFollowMe,
		UserId:              ownerUser.Id,
	}
	user := &models.User{
		Id:     2,
		Status: models.UserStatusNormal,
	}
	account := &models.Account{
		Id: 2,
	}

	// Owner found but follow creation fails due to missing table - should not panic
	assert.NotPanics(t, func() {
		ExecAfterRegisterAction(db.GormDB, inv, user, account)
	})
}

// TestFollowMe_DBError tests the followMe function when DB query fails
// by passing a DB with no tables.
func TestFollowMe_DBError(t *testing.T) {
	db := testutil.QuickDBSetup(t) // No tables migrated

	initSystemsettingsForTest(t, db.GormDB, map[string]any{
		systemsettings.KeyRegistrationApprovalRequired: false,
		systemsettings.KeyRegistrationCheckEmail:       false,
	})

	inv := &models.InviteCode{
		Id:                  1,
		AfterRegisterAction: InviteCodeAfterRegisterActionFollowMe,
		UserId:              1,
	}
	user := &models.User{
		Id:     1,
		Status: models.UserStatusNormal,
	}
	account := &models.Account{
		Id: 1,
	}

	// Should not panic even when DB operations fail
	assert.NotPanics(t, func() {
		ExecAfterRegisterAction(db.GormDB, inv, user, account)
	})
}

// TestExecAfterRegisterAction_UserNormal tests various user status combinations.
func TestExecAfterRegisterAction_UserNormal(t *testing.T) {
	tests := []struct {
		name          string
		approvalReq   bool
		emailCheck    bool
		userStatus    int
		shouldProceed bool
	}{
		{
			name:          "normal user, no checks",
			approvalReq:   false,
			emailCheck:    false,
			userStatus:    models.UserStatusNormal,
			shouldProceed: true,
		},
		{
			name:          "normal user, approval required",
			approvalReq:   true,
			emailCheck:    false,
			userStatus:    models.UserStatusNormal,
			shouldProceed: true,
		},
		{
			name:          "normal user, email check required",
			approvalReq:   false,
			emailCheck:    true,
			userStatus:    models.UserStatusNormal,
			shouldProceed: true,
		},
		{
			name:          "unapproved user, approval required",
			approvalReq:   true,
			emailCheck:    false,
			userStatus:    models.UserStatusUnapproved,
			shouldProceed: false,
		},
		{
			name:          "unverified email user, email check required",
			approvalReq:   false,
			emailCheck:    true,
			userStatus:    models.UserStatusUnverifiedMail,
			shouldProceed: false,
		},
		{
			name:          "rejected user, no checks",
			approvalReq:   false,
			emailCheck:    false,
			userStatus:    models.UserStatusRejected,
			shouldProceed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a real DB so followMe doesn't panic when shouldProceed is true
			db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})
			initSystemsettingsForTest(t, db.GormDB, map[string]any{
				systemsettings.KeyRegistrationApprovalRequired: tt.approvalReq,
				systemsettings.KeyRegistrationCheckEmail:       tt.emailCheck,
			})

			inv := &models.InviteCode{
				Id:                  1,
				AfterRegisterAction: InviteCodeAfterRegisterActionFollowMe,
				UserId:              9999, // non-existent to avoid DB dependency
			}
			user := &models.User{
				Id:     1,
				Status: tt.userStatus,
			}

			assert.NotPanics(t, func() {
				ExecAfterRegisterAction(db.GormDB, inv, user, nil)
			})
		})
	}
}

// TestExecAfterRegisterAction_UnknownAction_WithDB tests unknown action with real DB.
func TestExecAfterRegisterAction_UnknownAction_WithDB(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.User{}, &models.Account{}, &models.AccountExtra{}, &models.Follow{})

	initSystemsettingsForTest(t, db.GormDB, map[string]any{
		systemsettings.KeyRegistrationApprovalRequired: false,
		systemsettings.KeyRegistrationCheckEmail:       false,
	})

	inv := &models.InviteCode{
		Id:                  42,
		AfterRegisterAction: "some_invalid_action",
	}
	user := &models.User{
		Id:     1,
		Status: models.UserStatusNormal,
	}

	// Unknown action should be handled via default switch case (logs error)
	assert.NotPanics(t, func() {
		ExecAfterRegisterAction(db.GormDB, inv, user, nil)
	})
}

// TestExecAfterRegisterAction_FollowMe_MultipleAccounts tests followMe when owner
// has multiple accounts (bot and non-bot) — should find the non-bot one.
func TestExecAfterRegisterAction_FollowMe_MultipleAccounts(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})

	initSystemsettingsForTest(t, db.GormDB, map[string]any{
		systemsettings.KeyRegistrationApprovalRequired: false,
		systemsettings.KeyRegistrationCheckEmail:       false,
	})

	fixtures := testutil.NewFixtureBuilder(t, db.GormDB)
	ownerUser := fixtures.CreateUser(func(u *models.User) {
		u.UserName = "multiowner"
		u.EmailHash = "multiowner_hash"
	})
	// Create bot account first
	fixtures.CreateAccount(ownerUser, func(a *models.Account) {
		a.Username = "multiowner_bot"
		a.DisplayName = "Multi Owner Bot"
		a.Bot = true
	})
	// Create non-bot account
	ownerAccount := fixtures.CreateAccount(ownerUser, func(a *models.Account) {
		a.Username = "multiowner_real"
		a.DisplayName = "Multi Owner Real"
		a.Bot = false
	})

	newUser := fixtures.CreateUser(func(u *models.User) {
		u.UserName = "newmultiuser"
		u.EmailHash = "newmultiuser_hash"
	})
	newAccount := fixtures.CreateAccount(newUser, func(a *models.Account) {
		a.Username = "newmultiuser"
		a.DisplayName = "New Multi User"
	})

	inv := &models.InviteCode{
		Id:                  10,
		AfterRegisterAction: InviteCodeAfterRegisterActionFollowMe,
		UserId:              ownerUser.Id,
	}

	ExecAfterRegisterAction(db.GormDB, inv, newUser, newAccount)

	// Verify follow was created with the non-bot owner account
	var follow models.Follow

	err := db.GormDB.Where("account_id = ? AND follow_id = ?", newAccount.Id, ownerAccount.Id).First(&follow).Error
	require.NoError(t, err, "follow relationship should have been created with non-bot owner")
	assert.Equal(t, newAccount.Id, follow.AccountId)
	assert.Equal(t, ownerAccount.Id, follow.FollowId)
}
