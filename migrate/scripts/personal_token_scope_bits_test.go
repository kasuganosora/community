package scripts

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPersonalTokenScopeBitsScript(t *testing.T) {
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.Exec(`CREATE TABLE personal_access_tokens (id varchar(36) primary key, user_id bigint, name varchar(100), token_hash varchar(128), scopes text, created_at timestamp, expires_at timestamp)`).Error)
	require.NoError(t, conn.Exec(`INSERT INTO personal_access_tokens (id, user_id, name, token_hash, scopes) VALUES ('a', 1, 'tbtest', 'h1', '["read"]'), ('b', 1, 'odd', 'h2', '["bogus"]'), ('c', 1, 'none', 'h3', NULL)`).Error)
	require.NoError(t, PersonalTokenScopeBitsScript{}.Up(conn))
	require.NoError(t, PersonalTokenScopeBitsScript{}.Up(conn), "idempotent")
	var rows []models.PersonalAccessToken
	require.NoError(t, conn.Order("id").Find(&rows).Error)
	require.Equal(t, uint64(models.AppScopeRead), rows[0].ScopeBits.ReadScope)
	require.Zero(t, rows[0].ScopeBits.WriteScope)
	require.Equal(t, models.ScopeSourceMigration, rows[0].ScopeSource)
	require.Nil(t, rows[1].ScopeBits, "unparsable list waits for the blob")
	require.Nil(t, rows[2].ScopeBits)
}
