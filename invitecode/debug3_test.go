package invitecode

import (
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
)

// TestFollowMeDebug3 tests boolean handling with literal SQL
func TestFollowMeDebug3(t *testing.T) {
	db := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.User{}, &models.Follow{})

	// Create a user first
	now := "2026-01-01 00:00:00"
	db.GormDB.Exec(
		"INSERT INTO users (uuid, user_name, email_hash, status, is_mail_verified, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"uuid1", "testuser", "hash1", 0, true, now, now,
	)

	// Insert account with literal false
	db.GormDB.Exec(
		"INSERT INTO accounts (user_id, username, display_name, acct, bot, locked, created_at, updated_at) VALUES (1, 'rawowner', 'Raw Owner', 'rawowner@__LOCAL__', false, false, '2026-01-01 00:00:00', '2026-01-01 00:00:00')",
	)

	// Check what's in the table
	type Row struct {
		Id       uint64
		UserId   uint64
		BotVal   interface{}
		Username string
	}
	var rows []Row
	db.GormDB.Raw("SELECT id, user_id, bot as bot_val, username FROM accounts").Scan(&rows)
	for _, r := range rows {
		t.Logf("Row: Id=%d, UserId=%d, Bot=%v (%T), Username=%s", r.Id, r.UserId, r.BotVal, r.BotVal, r.Username)
	}

	// Try different query approaches
	var cnt int64
	db.GormDB.Model(&models.Account{}).Where("user_id = ? AND bot = false", 1).Count(&cnt)
	t.Logf("Count with 'bot = false' literal: %d", cnt)

	db.GormDB.Model(&models.Account{}).Where("user_id = 1 AND bot = false").Count(&cnt)
	t.Logf("Count with inline literal: %d", cnt)

	db.GormDB.Model(&models.Account{}).Where("user_id = 1").Where("bot = ?", false).Count(&cnt)
	t.Logf("Count with param false: %d", cnt)

	db.GormDB.Model(&models.Account{}).Where("user_id = 1").Count(&cnt)
	t.Logf("Count without bot filter: %d", cnt)
}
