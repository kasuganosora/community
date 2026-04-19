package invitecode

import (
	"testing"

	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
)

// TestFollowMeDebug2 tries direct GORM query patterns
func TestFollowMeDebug2(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})
	database.DB = db.GormDB
	systemsettings.Set(systemsettings.KeyRegistrationApprovalRequired, false)
	systemsettings.Set(systemsettings.KeyRegistrationCheckEmail, false)

	fixtures := testutil.NewFixtureBuilder(t, db.GormDB)
	ownerUser := fixtures.CreateUser(func(u *models.User) {
		u.UserName = "owneruser"
		u.EmailHash = "owner_hash"
	})

	// Try direct raw SQL insert for the account
	now := "2026-01-01 00:00:00"
	result := db.GormDB.Exec(
		"INSERT INTO accounts (user_id, username, display_name, acct, bot, locked, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		ownerUser.Id, "rawowner", "Raw Owner", "rawowner@__LOCAL__", 0, false, now, now,
	)
	assert.NoError(t, result.Error)
	t.Logf("Raw insert result: affected=%d", result.RowsAffected)

	// Now query with bot = false
	var found models.Account
	err := db.GormDB.Where("user_id = ?", ownerUser.Id).Where("bot = ?", false).First(&found).Error
	t.Logf("Query with bot=false: err=%v", err)

	// Try with integer 0
	err2 := db.GormDB.Where("user_id = ?", ownerUser.Id).Where("bot = ?", 0).First(&found).Error
	t.Logf("Query with bot=0: err=%v", err2)

	// Try with string
	err3 := db.GormDB.Where("user_id = ?", ownerUser.Id).Where("bot = ?", "false").First(&found).Error
	t.Logf("Query with bot='false': err=%v", err3)

	// Check what type bot actually is
	row := db.GormDB.Raw("SELECT bot FROM accounts WHERE user_id = ?", ownerUser.Id).Row()
	var botVal interface{}
	if err := row.Scan(&botVal); err != nil {
		t.Logf("Scan error: %v", err)
	} else {
		t.Logf("Raw bot value: %v (type=%T)", botVal, botVal)
	}
}
