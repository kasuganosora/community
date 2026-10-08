package scripts

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRepairMatrixHSTables_DropsOnlyEmptyLegacyTables(t *testing.T) {
	gdb, err := pgtest.GormOpen(t, sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Legacy DDL shape: user_id as primary key (conflicts with the model's id PK).
	require.NoError(t, gdb.Exec(`CREATE TABLE matrix_presence (user_id VARCHAR(255) PRIMARY KEY, presence VARCHAR(32))`).Error)
	require.NoError(t, gdb.Exec(`CREATE TABLE matrix_events (event_id VARCHAR(255) PRIMARY KEY, room_id VARCHAR(255))`).Error)
	require.NoError(t, gdb.Exec(`INSERT INTO matrix_events VALUES ('$e1', '!r:example.org')`).Error)
	require.NoError(t, gdb.Exec(`CREATE TABLE unrelated (id INTEGER PRIMARY KEY)`).Error)

	require.NoError(t, RepairMatrixHSTablesScript{}.Up(gdb))

	m := gdb.Migrator()
	assert.False(t, m.HasTable("matrix_presence"), "empty legacy table must be dropped")
	assert.True(t, m.HasTable("matrix_events"), "non-empty table must be kept")
	assert.True(t, m.HasTable("unrelated"))

	// Idempotent.
	require.NoError(t, RepairMatrixHSTablesScript{}.Up(gdb))
}

func TestMigrateMatrixHSScript_UpIsNoOp(t *testing.T) {
	gdb, err := pgtest.GormOpen(t, sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)

	require.NoError(t, MigrateMatrixHSScript{}.Up(gdb))
	assert.False(t, gdb.Migrator().HasTable("matrix_events"))
}
