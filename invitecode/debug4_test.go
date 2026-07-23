package invitecode

import (
	"testing"

	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/testutil"
)

// TestFollowMeDebug4 tries altering the bot column type to integer.
func TestFollowMeDebug4(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})
	database.DB = db.GormDB

	systemsettings.Set(systemsettings.KeyRegistrationApprovalRequired, false)
	systemsettings.Set(systemsettings.KeyRegistrationCheckEmail, false)

	// Try altering the bot column to integer
	err := db.GormDB.Exec("ALTER TABLE accounts ALTER COLUMN bot TYPE integer USING bot::integer").Error
	t.Logf("Alter column error (PostgreSQL syntax): %v", err)

	err2 := db.GormDB.Exec("ALTER TABLE accounts MODIFY COLUMN bot INTEGER").Error
	t.Logf("Alter column error (MySQL syntax): %v", err2)

	err3 := db.GormDB.Exec("ALTER TABLE accounts ADD COLUMN bot_int INTEGER DEFAULT 0").Error
	t.Logf("Add bot_int column error: %v", err3)

	// Create user and account using fixtures
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
	t.Logf("ownerAccount.Id=%d, UserId=%d, Bot=%v", ownerAccount.Id, ownerAccount.UserId, ownerAccount.Bot)

	// Update bot column using raw SQL integer
	db.GormDB.Exec("UPDATE accounts SET bot = 0 WHERE id = ?", ownerAccount.Id)

	// Now check
	var cnt int64

	db.GormDB.Model(&models.Account{}).Where("user_id = ?", ownerUser.Id).Where("bot = ?", false).Count(&cnt)
	t.Logf("Count after raw update with bot=false: %d", cnt)

	// Try querying the same way followMe does
	var found models.Account

	qerr := db.GormDB.Where("user_id = ?", ownerUser.Id).Where("bot = ?", false).First(&found).Error
	t.Logf("followMe-style query: err=%v", qerr)

	if qerr == nil {
		t.Logf("Found account! Id=%d, Bot=%v", found.Id, found.Bot)
	}
}
