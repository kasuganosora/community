package commands

import (
	"bytes"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/twofactor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestTwoFactorResetCLI(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, _ := conn.DB()
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, conn.AutoMigrate(&models.User{}, &models.Account{}, &models.AccountExtra{}, &models.UserTwoFactor{}, &models.UserBackupCode{}))
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)
	require.NoError(t, conn.Create(&models.User{Id: 9, UUID: "u9", UserName: "qa", EmailHash: "h9"}).Error)
	require.NoError(t, conn.Create(&models.Account{UserId: 9, Username: "qa", DisplayName: "QA"}).Error)
	require.NoError(t, twofactor.ImportSecret(conn, 9, "JBSWY3DPEHPK3PXP", []string{"aaaa-bbbb"}))

	run := func(args ...string) string {
		var out bytes.Buffer
		app := &cli.App{Name: "tootctl", Commands: []*cli.Command{TwoFactorCommand()}, Writer: &out, ErrWriter: &out, ExitErrHandler: func(*cli.Context, error) {}}
		require.NoError(t, app.Run(append([]string{"tootctl", "two-factor"}, args...)))
		return out.String()
	}
	assert.Contains(t, run("status", "qa"), "enabled since")
	assert.Contains(t, run("reset", "qa"), "reset for qa")
	assert.Contains(t, run("status", "--username", "qa"), "disabled")
	on, _ := twofactor.IsEnabled(conn, 9)
	assert.False(t, on)
}

func TestTwoFactorScrubPreserved(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, _ := conn.DB()
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, conn.AutoMigrate(&datatransfer.StoredPreservedRecord{}))
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)
	require.NoError(t, conn.Create(&datatransfer.StoredPreservedRecord{Scope: "site", Namespace: "n", Kind: "misskey:user_profile", SourceID: "1",
		Data: []byte(`{"userId":"1","twoFactorSecret":"JBSWY3DPEHPK3PXP","twoFactorBackupSecret":["abc"]}`)}).Error)
	require.NoError(t, conn.Create(&datatransfer.StoredPreservedRecord{Scope: "site", Namespace: "n", Kind: "misskey:note", SourceID: "2", Data: []byte(`{"id":"2"}`)}).Error)
	var out bytes.Buffer
	app := &cli.App{Name: "tootctl", Commands: []*cli.Command{TwoFactorCommand()}, Writer: &out, ExitErrHandler: func(*cli.Context, error) {}}
	require.NoError(t, app.Run([]string{"tootctl", "two-factor", "scrub-preserved", "--dry-run"}))
	assert.Contains(t, out.String(), "would redact 1")
	require.NoError(t, app.Run([]string{"tootctl", "two-factor", "scrub-preserved"}))
	assert.Contains(t, out.String(), "redacted 1")
	var rec datatransfer.StoredPreservedRecord
	require.NoError(t, conn.First(&rec, "source_id = ?", "1").Error)
	assert.NotContains(t, string(rec.Data), "JBSWY3DPEHPK3PXP")
	assert.NotContains(t, string(rec.Data), "abc")
	assert.NotContains(t, out.String(), "JBSWY3DPEHPK3PXP")
}
