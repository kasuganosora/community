package invitecode

import (
	"testing"

	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
)

// TestFollowMeDebug diagnoses why owner account is not found
func TestFollowMeDebug(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})
	database.DB = db.GormDB
	systemsettings.Set(systemsettings.KeyRegistrationApprovalRequired, false)
	systemsettings.Set(systemsettings.KeyRegistrationCheckEmail, false)

	fixtures := testutil.NewFixtureBuilder(t, db.GormDB)
	ownerUser := fixtures.CreateUser(func(u *models.User) {
		u.UserName = "owneruser"
		u.EmailHash = "owner_hash"
	})
	t.Logf("ownerUser.Id = %d", ownerUser.Id)

	ownerAccount := fixtures.CreateAccount(ownerUser, func(a *models.Account) {
		a.Username = "owneruser"
		a.DisplayName = "Owner User"
		a.Bot = false
	})
	t.Logf("ownerAccount.Id = %d, UserId = %d, Bot = %v", ownerAccount.Id, ownerAccount.UserId, ownerAccount.Bot)

	// Directly query to verify
	var allAccounts []models.Account
	db.GormDB.Find(&allAccounts)
	t.Logf("Total accounts in DB: %d", len(allAccounts))
	for _, acc := range allAccounts {
		t.Logf("  Account: Id=%d, UserId=%d, Bot=%v, Username=%s", acc.Id, acc.UserId, acc.Bot, acc.Username)
	}

	// Query the same way followMe does
	var found models.Account
	err := db.GormDB.Where("user_id = ?", ownerUser.Id).Where("bot = ?", false).First(&found).Error
	t.Logf("Query error: %v", err)
	if err == nil {
		t.Logf("Found: Id=%d, UserId=%d, Bot=%v", found.Id, found.UserId, found.Bot)
	}

	// Try without bot filter
	err2 := db.GormDB.Where("user_id = ?", ownerUser.Id).First(&found).Error
	t.Logf("Query without bot filter error: %v", err2)
	if err2 == nil {
		t.Logf("Found without bot filter: Id=%d, UserId=%d, Bot=%v", found.Id, found.UserId, found.Bot)
	}

	// Test ExecAfterRegisterAction
	inv := &models.InviteCode{
		Id:                  1,
		AfterRegisterAction: InviteCodeAfterRegisterActionFollowMe,
		UserId:              ownerUser.Id,
	}
	newUser := fixtures.CreateUser(func(u *models.User) {
		u.UserName = "newuser"
		u.EmailHash = "newuser_hash"
	})
	newAccount := fixtures.CreateAccount(newUser, func(a *models.Account) {
		a.Username = "newuser"
		a.DisplayName = "New User"
	})
	inv.UserId = ownerUser.Id

	ExecAfterRegisterAction(db.GormDB, inv, newUser, newAccount)

	// Check if follow was created
	var follow models.Follow
	err3 := db.GormDB.Where("account_id = ? AND follow_id = ?", newAccount.Id, ownerAccount.Id).First(&follow).Error
	assert.NoError(t, err3, "follow should exist")
}
