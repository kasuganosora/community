package commands

import (
	"bytes"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
)

func runMatrix(args ...string) cliResult {
	var out, errOut bytes.Buffer
	app := &cli.App{Name: "tootctl", Commands: []*cli.Command{MatrixCommand()}, Writer: &out, ErrWriter: &errOut,
		ExitErrHandler: func(*cli.Context, error) {}}
	err := app.Run(append([]string{"tootctl", "matrix", "invalidate-default-passwords"}, args...))
	return cliResult{out: out.String(), errOut: errOut.String(), err: err}
}

func TestMatrixInvalidateDefaultPasswords(t *testing.T) {
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, _ := conn.DB()
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, conn.AutoMigrate(&models.User{}, &models.UserSecretData{}, &models.Account{}, &models.AccountExtra{}))
	require.NoError(t, conn.Exec(`CREATE TABLE matrix_user_sessions (id integer primary key, user_id text, access_token text, device_id text, local_account_id integer)`).Error)
	require.NoError(t, conn.Exec(`CREATE TABLE matrix_sync_to_device_batches (id integer primary key, user_id text)`).Error)
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)

	mk := func(id uint64, name, hashPrefix, pw string) {
		salt := "salt-" + name
		h, err := models.EncryptPassword(pw, salt)
		require.NoError(t, err)
		require.NoError(t, conn.Create(&models.User{Id: id, UUID: name, UserName: name, EmailHash: hashPrefix + name}).Error)
		require.NoError(t, conn.Exec(`INSERT INTO user_secret_data (user_id, salt, password, email, phone, password_scheme) VALUES (?,?,?,'','','')`, id, salt, h).Error)
		require.NoError(t, conn.Create(&models.Account{Id: id, UserId: id, Username: name}).Error)
		require.NoError(t, conn.Exec(`INSERT INTO matrix_user_sessions (user_id, access_token, device_id, local_account_id) VALUES (?,?,?,?)`, "@"+name+":hs", "tok-"+name, "D", id).Error)
		require.NoError(t, conn.Exec(`INSERT INTO matrix_sync_to_device_batches (user_id) VALUES (?)`, "@"+name+":hs").Error)
	}
	mk(1, "vic_tim", "matrix_", legacyMatrixDefaultPassword)
	mk(2, "vicxtim", "matrix_", "a-real-password")
	mk(3, "web", "web_", legacyMatrixDefaultPassword)

	count := func(table, uid string) int64 {
		var n int64
		conn.Table(table).Where("user_id = ?", uid).Count(&n)
		return n
	}

	res := runMatrix("--dry-run")
	require.NoError(t, res.err)
	assert.Contains(t, res.out, "user_id=1 username=vic_tim")
	assert.NotContains(t, res.out, "user_id=3")
	assert.Contains(t, res.out, "checked 2 accounts, would invalidate (dry run) 1")
	assert.NotContains(t, res.out, legacyMatrixDefaultPassword)
	assert.EqualValues(t, 1, count("matrix_user_sessions", "@vic_tim:hs"))

	res = runMatrix()
	require.NoError(t, res.err)
	assert.Contains(t, res.out, "invalidated 1")
	var s models.UserSecretData
	require.NoError(t, conn.Table("user_secret_data").Select("user_id, salt, password, password_scheme").Where("user_id = 1").Scan(&s).Error)
	assert.Error(t, s.CheckPassword(legacyMatrixDefaultPassword))
	assert.Error(t, s.CheckPassword(""))
	assert.EqualValues(t, 0, count("matrix_user_sessions", "@vic_tim:hs"))
	assert.EqualValues(t, 0, count("matrix_sync_to_device_batches", "@vic_tim:hs"))
	// LIKE wildcard in "vic_tim" must not touch "vicxtim".
	assert.EqualValues(t, 1, count("matrix_user_sessions", "@vicxtim:hs"))

	res = runMatrix("--all-users")
	require.NoError(t, res.err)
	assert.Contains(t, res.out, "user_id=3 username=web")
	assert.Contains(t, res.out, "checked 3 accounts, invalidated 1")
}
