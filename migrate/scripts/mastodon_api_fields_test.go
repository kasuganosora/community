package scripts

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMastodonAPIFieldsUpgradePreservesExistingChoices(t *testing.T) {
	conn, err := pgtest.GormOpen(t, sqlite.Open(filepath.Join(t.TempDir(), "legacy.db")), &gorm.Config{})
	require.NoError(t, err)
	for _, ddl := range []string{
		"CREATE TABLE lists (id integer primary key)",
		"CREATE TABLE mute_accounts (id integer primary key, account_id integer, target_id integer)",
		"CREATE TABLE accounts (id integer primary key, discoverable boolean)",
		"CREATE TABLE applications (id integer primary key)",
		"CREATE TABLE application_user_auths (id integer primary key, scopes text)",
		"INSERT INTO accounts VALUES (1, true)",
		"INSERT INTO lists VALUES (1)",
		"INSERT INTO mute_accounts VALUES (1, 1, 2)",
		"INSERT INTO applications VALUES (1)",
		"INSERT INTO application_user_auths VALUES (1, 'read')",
	} {
		require.NoError(t, conn.Exec(ddl).Error)
	}
	migration := MastodonAPIFieldsScript{}
	require.NoError(t, migration.Up(conn))
	check := func(query string) {
		t.Helper()
		var count int
		require.NoError(t, conn.Raw(query).Scan(&count).Error)
		require.Equal(t, 1, count, query)
	}
	check("SELECT COUNT(*) FROM accounts WHERE discoverable AND NOT discoverable_configured AND NOT collections_configured")
	check("SELECT COUNT(*) FROM lists WHERE NOT exclusive")
	check("SELECT COUNT(*) FROM mute_accounts WHERE target_id = 2 AND NOT notifications")
	check("SELECT COUNT(*) FROM applications WHERE COALESCE(registered_scopes, '') = ''")
	check("SELECT COUNT(*) FROM application_user_auths WHERE scopes = 'read' AND NOT scopes_exact")
	require.NoError(t, conn.Exec("UPDATE application_user_auths SET scopes = 'read:follows', scopes_exact = true").Error)
	require.NoError(t, conn.Exec("UPDATE lists SET exclusive = true").Error)
	require.NoError(t, migration.Up(conn))
	check("SELECT COUNT(*) FROM application_user_auths WHERE scopes = 'read:follows' AND scopes_exact")
	check("SELECT COUNT(*) FROM lists WHERE exclusive")
}
