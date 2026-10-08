package scripts

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSplitSQLStatements(t *testing.T) {
	stmts := splitSQLStatements(`
	-- comment line
	CREATE INDEX IF NOT EXISTS idx_a ON a(x); -- trailing
	ALTER TABLE a ADD COLUMN IF NOT EXISTS y INT;

	`)
	assert.Equal(t, []string{
		"CREATE INDEX IF NOT EXISTS idx_a ON a(x)",
		"ALTER TABLE a ADD COLUMN IF NOT EXISTS y INT",
	}, stmts)
}

func TestIndexColumnNames(t *testing.T) {
	assert.Equal(t, []string{"account_id", "created_at"}, indexColumnNames("account_id, created_at DESC"))
	assert.Equal(t, []string{"x"}, indexColumnNames("`x`"))
}

type ddlThing struct {
	Id        uint64 `gorm:"primaryKey"`
	AccountId uint64
}

func TestApplyPortableDDL_SkipsMissingAndIsIdempotent(t *testing.T) {
	gdb, err := pgtest.GormOpen(t, sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&ddlThing{}))

	sql := `
	CREATE INDEX IF NOT EXISTS idx_ddl_things_account_id ON ddl_things(account_id);
	-- column does not exist: must be skipped, not fail
	CREATE INDEX IF NOT EXISTS idx_ddl_things_nope ON ddl_things(nope);
	-- table does not exist: must be skipped, not fail
	CREATE INDEX IF NOT EXISTS idx_missing ON missing_table(account_id);
	ALTER TABLE ddl_things ADD COLUMN IF NOT EXISTS blurhash VARCHAR(50);
	ALTER TABLE missing_table ADD COLUMN IF NOT EXISTS blurhash VARCHAR(50);
	`

	require.NoError(t, applyPortableDDL(gdb, sql))
	// running twice must be a no-op
	require.NoError(t, applyPortableDDL(gdb, sql))

	m := gdb.Migrator()
	assert.True(t, m.HasIndex("ddl_things", "idx_ddl_things_account_id"))
	assert.False(t, m.HasIndex("ddl_things", "idx_ddl_things_nope"))
	assert.True(t, m.HasColumn("ddl_things", "blurhash"))
	assert.False(t, m.HasTable("missing_table"))
}

func TestGroupTablesSQL_QuotesReservedWordAndMatchesAccountIdType(t *testing.T) {
	// "groups" is a reserved word since MySQL 8.0.2 and must be quoted.
	assert.NotContains(t, groupTablesSQL, "EXISTS groups (")
	assert.NotContains(t, groupTablesSQL, "REFERENCES groups(")
	// accounts.id is a signed BIGINT; FK columns pointing at it must match.
	for _, col := range []string{"owner_id", "sender_id", "contact_account_id", "from_account_id", "to_account_id"} {
		assert.NotContains(t, groupTablesSQL, col+" BIGINT UNSIGNED", col)
	}
	// MySQL 8 rejects literal defaults on TEXT columns.
	assert.NotContains(t, groupTablesSQL, "TEXT DEFAULT ''")
}
