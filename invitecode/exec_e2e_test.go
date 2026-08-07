package invitecode

import (
	"testing"

	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
)

// TestExecAfterRegisterAction_FollowMe_EndToEnd verifies the full follow-me
// flow end-to-end: registering a new user via an invite code with a follow_me
// action results in a follow relationship from the new user's account to the
// invite-code owner's (non-bot) account.
//
// This consolidates the former ad-hoc TestFollowMeDebug into a proper,
// assertion-based regression test (no debug logging, no raw SQL probing).
func TestExecAfterRegisterAction_FollowMe_EndToEnd(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})
	database.DB = db.GormDB

	systemsettings.Set(systemsettings.KeyRegistrationApprovalRequired, false)
	systemsettings.Set(systemsettings.KeyRegistrationCheckEmail, false)

	fixtures := testutil.NewFixtureBuilder(t, db.GormDB)

	ownerUser := fixtures.CreateUser(func(u *models.User) {
		u.UserName = "owneruser"
		u.EmailHash = "owner_hash"
	})
	ownerAccount := fixtures.CreateAccount(ownerUser, func(a *models.Account) {
		a.Username = "owneruser"
		a.DisplayName = "Owner User"
		a.Bot = false
	})

	newUser := fixtures.CreateUser(func(u *models.User) {
		u.UserName = "newuser"
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

	ExecAfterRegisterAction(db.GormDB, inv, newUser, newAccount)

	// The follow relationship must have been created.
	var follow models.Follow
	err := db.GormDB.Where("account_id = ? AND follow_id = ?", newAccount.Id, ownerAccount.Id).First(&follow).Error
	assert.NoError(t, err, "follow relationship should exist after follow_me action")
}
