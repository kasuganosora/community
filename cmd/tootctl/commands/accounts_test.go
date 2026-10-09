package commands

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/migrate/scripts"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/kasuganosora/community/utils"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
)

func setupAccountsTest(t *testing.T) *gorm.DB {
	t.Helper()

	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(
		&models.User{}, &models.UserSecretData{}, &models.Account{}, &models.AccountExtra{}, &models.Emoji{},
		&models.Role{}, &models.RoleUser{}, &models.AuthToken{},
	))
	require.NoError(t, scripts.AddRoles{}.Up(conn))
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)

	return conn
}

type cliResult struct {
	out, errOut string
	err         error
}

func runAccounts(stdin string, args ...string) cliResult {
	var out, errOut bytes.Buffer

	app := &cli.App{
		Name:      "tootctl",
		Commands:  []*cli.Command{AccountsCommand()},
		Reader:    strings.NewReader(stdin),
		Writer:    &out,
		ErrWriter: &errOut,
	}
	err := app.Run(NormalizeAccountArgs(append([]string{"tootctl", "accounts"}, args...)))

	return cliResult{out: out.String(), errOut: errOut.String(), err: err}
}

var newPasswordLine = regexp.MustCompile(`(?m)^New password: ([0-9a-f]{32})$`)

func generatedPassword(t *testing.T, out string) string {
	t.Helper()

	m := newPasswordLine.FindStringSubmatch(out)
	require.NotNil(t, m, "no generated password in output:\n%s", out)

	return m[1]
}

func loadUser(t *testing.T, conn *gorm.DB, username string) (models.User, models.UserSecretData) {
	t.Helper()

	var user models.User
	require.NoError(t, conn.Where("username = ?", username).First(&user).Error)

	var secret models.UserSecretData
	require.NoError(t, conn.Where("user_id = ?", user.Id).First(&secret).Error)

	return user, secret
}

func decryptEmail(t *testing.T, secret models.UserSecretData) string {
	t.Helper()

	if config.C.EncryptedKey == "" {
		return secret.Email
	}

	plain, err := utils.AesDecrypt(secret.Email, utils.GetEncryptKey(config.C.EncryptedKey, secret.Salt))
	require.NoError(t, err)

	return string(plain)
}

func userRoleNames(t *testing.T, conn *gorm.DB, userID uint64) []string {
	t.Helper()

	roles, err := models.GetUserRoles(conn, userID)
	require.NoError(t, err)

	names := make([]string, 0, len(roles))
	for _, role := range roles {
		names = append(names, role.Name)
	}

	return names
}

func TestAccountsCreateAdminWithPasswordStdin(t *testing.T) {
	conn := setupAccountsTest(t)

	res := runAccounts("Sup3r-secret pass\n", "create", "--username", "boss", "--email", "boss@example.com",
		"--admin", "--confirmed", "--password-stdin")
	require.NoError(t, res.err, res.errOut)
	require.Contains(t, res.out, "Account created: boss")
	require.NotContains(t, res.out, "New password")

	user, secret := loadUser(t, conn, "boss")
	require.True(t, user.IsAdmin)
	require.True(t, user.IsMailVerified)
	require.False(t, user.IsBan)
	require.Equal(t, models.UserStatusNormal, user.Status)
	require.Equal(t, models.GetEmailHash("boss@example.com"), user.EmailHash)
	require.Equal(t, "boss@example.com", decryptEmail(t, secret))
	require.Equal(t, []string{"owner"}, userRoleNames(t, conn, user.Id))

	require.NoError(t, secret.CheckPassword("Sup3r-secret pass"))
	require.Error(t, secret.CheckPassword("changeme"))

	bits, err := models.EffectiveAdminPermissions(conn, &user)
	require.NoError(t, err)
	require.Equal(t, models.SupportedAdminPermissions(), bits)

	var accounts int64
	require.NoError(t, conn.Model(&models.Account{}).Where("user_id = ? AND domain IS NULL", user.Id).Count(&accounts).Error)
	require.Equal(t, int64(1), accounts)
}

func TestAccountsCreateGeneratesPassword(t *testing.T) {
	conn := setupAccountsTest(t)

	// Positional username and case-insensitive role names, as in Mastodon.
	res := runAccounts("", "create", "mod", "--email", "mod@example.com", "--role", "Moderator")
	require.NoError(t, res.err, res.errOut)
	password := generatedPassword(t, res.out)

	user, secret := loadUser(t, conn, "mod")
	require.False(t, user.IsAdmin)
	require.False(t, user.IsMailVerified)
	require.Equal(t, models.UserStatusNormal, user.Status)
	require.Equal(t, []string{"moderator"}, userRoleNames(t, conn, user.Id))
	require.NoError(t, secret.CheckPassword(password))
	require.Error(t, secret.CheckPassword("changeme"))

	// A second account gets a different password.
	res = runAccounts("", "create", "other", "--email", "other@example.com", "--approve")
	require.NoError(t, res.err, res.errOut)
	require.NotEqual(t, password, generatedPassword(t, res.out))
}

func TestAccountsCreateInlinePasswordWarns(t *testing.T) {
	conn := setupAccountsTest(t)

	res := runAccounts("", "create", "inline", "--email", "inline@example.com", "--password", "inline-pass-1")
	require.NoError(t, res.err)
	require.Contains(t, res.errOut, "Warning: --password is visible")

	_, secret := loadUser(t, conn, "inline")
	require.NoError(t, secret.CheckPassword("inline-pass-1"))
}

func TestAccountsCreateRejectsInvalidInput(t *testing.T) {
	conn := setupAccountsTest(t)
	require.NoError(t, runAccounts("", "create", "taken", "--email", "taken@example.com").err)

	for name, tc := range map[string]struct {
		stdin string
		args  []string
		want  string
	}{
		"unknown role":    {"", []string{"create", "a1", "--email", "a1@example.com", "--role", "superuser"}, "available: admin, moderator, owner"},
		"short password":  {"short\n", []string{"create", "a2", "--email", "a2@example.com", "--password-stdin"}, "too short"},
		"empty stdin":     {"", []string{"create", "a3", "--email", "a3@example.com", "--password-stdin"}, "no password"},
		"two sources":     {"x\n", []string{"create", "a4", "--email", "a4@example.com", "--password-stdin", "--password", "longenough"}, "only one"},
		"bad username":    {"", []string{"create", "../etc", "--email", "a5@example.com"}, "username may only contain"},
		"bad email":       {"", []string{"create", "a6", "--email", "not-an-email"}, "email is invalid"},
		"duplicate user":  {"", []string{"create", "taken", "--email", "a7@example.com"}, "already exists"},
		"duplicate email": {"", []string{"create", "a8", "--email", "taken@example.com"}, "already in use"},
		"no username":     {"", []string{"create", "--email", "a9@example.com"}, "username is required"},
	} {
		t.Run(name, func(t *testing.T) {
			res := runAccounts(tc.stdin, tc.args...)
			require.Error(t, res.err)
			require.Contains(t, res.err.Error(), tc.want)
		})
	}

	var users int64
	require.NoError(t, conn.Model(&models.User{}).Count(&users).Error)
	require.Equal(t, int64(1), users, "failed creates must not leave users behind")
}

func TestAccountsModifyPasswordAndRoles(t *testing.T) {
	conn := setupAccountsTest(t)
	require.NoError(t, runAccounts("first-password\n", "create", "alice", "--email", "alice@example.com", "--password-stdin").err)

	user, _ := loadUser(t, conn, "alice")
	require.NoError(t, conn.Create(&models.AuthToken{Token: "digest-1", UserId: user.Id}).Error)
	require.NoError(t, conn.Model(&user).Updates(map[string]any{"status": models.UserStatusUnapproved}).Error)

	// --reset-password prints a new password and revokes sessions.
	res := runAccounts("", "modify", "alice", "--reset-password")
	require.NoError(t, res.err, res.errOut)
	reset := generatedPassword(t, res.out)
	require.Contains(t, res.out, "Revoked sessions/tokens: 1")

	_, secret := loadUser(t, conn, "alice")
	require.NoError(t, secret.CheckPassword(reset))
	require.Error(t, secret.CheckPassword("first-password"))

	var token models.AuthToken
	require.NoError(t, conn.Where("token = ?", "digest-1").First(&token).Error)
	require.NotNil(t, token.Revoke)

	// A chosen password from stdin.
	res = runAccounts("chosen-password\n", "modify", "--username", "alice", "--password-stdin")
	require.NoError(t, res.err, res.errOut)
	require.NotContains(t, res.out, "New password")
	_, secret = loadUser(t, conn, "alice")
	require.NoError(t, secret.CheckPassword("chosen-password"))

	// Approve, confirm, admin.
	res = runAccounts("", "modify", "alice", "--approve", "--confirm", "--admin", "--email", "new@example.com")
	require.NoError(t, res.err, res.errOut)
	user, _ = loadUser(t, conn, "alice")
	require.Equal(t, models.UserStatusNormal, user.Status)
	require.True(t, user.IsMailVerified)
	require.True(t, user.IsAdmin)
	require.Equal(t, models.GetEmailHash("new@example.com"), user.EmailHash)
	_, secret = loadUser(t, conn, "alice")
	require.Equal(t, "new@example.com", decryptEmail(t, secret))
	require.NoError(t, secret.CheckPassword("chosen-password"))
	require.Equal(t, []string{"owner"}, userRoleNames(t, conn, user.Id))

	// --unadmin drops the flag and the owner role but keeps other roles.
	require.NoError(t, runAccounts("", "modify", "alice", "--role", "moderator").err)
	require.NoError(t, runAccounts("", "modify", "alice", "--unadmin").err)
	user, _ = loadUser(t, conn, "alice")
	require.False(t, user.IsAdmin)
	require.Equal(t, []string{"moderator"}, userRoleNames(t, conn, user.Id))

	require.NoError(t, runAccounts("", "modify", "alice", "--remove-role", "--role", "admin").err)
	require.Equal(t, []string{"admin"}, userRoleNames(t, conn, user.Id))

	// Invalid combinations and passwords are rejected without changes.
	require.ErrorContains(t, runAccounts("x\n", "modify", "alice", "--reset-password", "--password-stdin").err, "cannot be combined")
	require.ErrorContains(t, runAccounts("short\n", "modify", "alice", "--password-stdin").err, "too short")
	require.ErrorContains(t, runAccounts("", "modify", "nobody", "--confirm").err, "account not found")
	_, secret = loadUser(t, conn, "alice")
	require.NoError(t, secret.CheckPassword("chosen-password"))
}

func TestAccountsDelete(t *testing.T) {
	conn := setupAccountsTest(t)
	require.NoError(t, runAccounts("", "create", "gone", "--email", "gone@example.com").err)
	require.NoError(t, runAccounts("", "create", "gone2", "--email", "gone2@example.com").err)

	res := runAccounts("", "delete", "gone")
	require.NoError(t, res.err, res.errOut)
	require.Contains(t, res.out, "Account deleted: gone")

	res = runAccounts("", "delete", "--email", "gone2@example.com")
	require.NoError(t, res.err, res.errOut)
	require.Contains(t, res.out, "Account deleted: gone2")

	var users int64
	require.NoError(t, conn.Model(&models.User{}).Count(&users).Error)
	require.Equal(t, int64(0), users)
}

func TestNormalizeAccountArgs(t *testing.T) {
	for _, tc := range []struct{ in, want []string }{
		{[]string{"tootctl", "accounts", "create", "alice", "--email", "a@x"}, []string{"tootctl", "accounts", "create", "--username", "alice", "--email", "a@x"}},
		{[]string{"tootctl", "-v", "accounts", "modify", "bob", "--admin"}, []string{"tootctl", "-v", "accounts", "modify", "--username", "bob", "--admin"}},
		{[]string{"tootctl", "accounts", "create", "--username", "alice"}, []string{"tootctl", "accounts", "create", "--username", "alice"}},
		{[]string{"tootctl", "accounts", "create", "--email", "a@x", "alice"}, []string{"tootctl", "accounts", "create", "--email", "a@x", "alice"}},
		{[]string{"tootctl", "media", "accounts", "create", "x"}, []string{"tootctl", "media", "accounts", "create", "x"}},
		{[]string{"tootctl", "accounts", "help", "create"}, []string{"tootctl", "accounts", "help", "create"}},
		{[]string{"tootctl", "accounts", "create"}, []string{"tootctl", "accounts", "create"}},
	} {
		require.Equal(t, tc.want, NormalizeAccountArgs(tc.in), "%v", tc.in)
	}
}
