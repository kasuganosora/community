package database

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// detectTestMode
// ---------------------------------------------------------------------------

func TestDetectTestMode_TestArgsPresent(t *testing.T) {
	// In a test binary os.Args contains -test.* flags; detectTestMode must
	// return true even without the GO_TEST_MODE env var.
	t.Setenv("GO_TEST_MODE", "")
	assert.True(t, detectTestMode(), "should detect test mode via -test.* args")
}

func TestDetectTestMode_EnvVarOnly(t *testing.T) {
	// Simulate an environment without -test.* args but with GO_TEST_MODE=1.
	oldArgs := os.Args
	os.Args = []string{"some-binary"}
	t.Cleanup(func() { os.Args = oldArgs })

	t.Setenv("GO_TEST_MODE", "1")
	assert.True(t, detectTestMode(), "should detect test mode via GO_TEST_MODE env var")
}

func TestDetectTestMode_NoIndicators(t *testing.T) {
	oldArgs := os.Args
	os.Args = []string{"some-binary"}
	t.Cleanup(func() { os.Args = oldArgs })

	t.Setenv("GO_TEST_MODE", "0")
	assert.False(t, detectTestMode(), "should return false when no test indicators present")
}

// ---------------------------------------------------------------------------
// getDsnType
// ---------------------------------------------------------------------------

func TestGetDsnType_AllBranches(t *testing.T) {
	original := config.C.DbConnection
	t.Cleanup(func() { config.C.DbConnection = original })

	cases := []struct {
		name string
		conn string
		want string
	}{
		{"mysql", "mysql", "mysql"},
		{"sqlite", "sqlite", "sqlite"},
		{"xml", "xml", "xml"},
		{"empty", "", ""},
		{"unknown", "postgres", ""},
		{"uppercase", "MySQL", ""}, // case-sensitive
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config.C.DbConnection = tc.conn
			assert.Equal(t, tc.want, getDsnType())
		})
	}
}

// ---------------------------------------------------------------------------
// closeGormSilently
// ---------------------------------------------------------------------------

func TestCloseGormSilently_Nil(t *testing.T) {
	err := closeGormSilently(nil)
	assert.NoError(t, err, "closing nil should be a no-op returning nil")
}

func TestCloseGormSilently_RealDB(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Closing a freshly opened DB should succeed and return nil.
	err = closeGormSilently(gdb)
	assert.NoError(t, err)

	// The underlying *sql.DB should now report as closed.
	sqlDB, dbErr := gdb.DB()
	if dbErr == nil {
		assert.True(t, sqlDB.Stats().OpenConnections == 0,
			"after close, there should be no open connections")
	}
}

// ---------------------------------------------------------------------------
// initGormLocked
//
// initGormLocked assumes the caller holds dbMu and returns a non-empty string
// when a fatal error is needed (instead of calling os.Exit). This lets us test
// every branch without risking process termination.
// ---------------------------------------------------------------------------

// withNonTestModeGlobally sets isTestMode=false and DB=nil so that
// initGormLocked() actually executes its body. State is restored via Cleanup.
func withNonTestModeGlobally(t *testing.T) {
	t.Helper()

	dbMu.Lock()
	savedMode := isTestMode
	isTestMode = false
	DB = nil
	dbMu.Unlock()

	savedConn := config.C.DbConnection
	savedHost := config.C.DbHost
	savedDBName := config.C.DbDatabase
	savedDevMode := config.C.DevMode

	t.Cleanup(func() {
		// Close whatever DB may have been opened during the test.
		dbMu.Lock()
		opened := DB
		DB = nil
		isTestMode = savedMode
		dbMu.Unlock()

		if opened != nil {
			_ = closeGormSilently(opened)
		}

		config.C.DbConnection = savedConn
		config.C.DbHost = savedHost
		config.C.DbDatabase = savedDBName
		config.C.DevMode = savedDevMode
	})
}

func TestInitGormLocked_TestModeSkips(t *testing.T) {
	// By default in a test binary isTestMode is true → initGormLocked returns "".
	dbMu.Lock()
	originalMode := isTestMode
	isTestMode = true
	msg := initGormLocked()
	isTestMode = originalMode
	dbMu.Unlock()

	assert.Empty(t, msg, "initGormLocked should skip in test mode")
}

func TestInitGormLocked_AlreadyInitializedSkips(t *testing.T) {
	withNonTestModeGlobally(t)

	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	dbMu.Lock()
	DB = gdb
	msg := initGormLocked()
	// DB is already set, so it should skip without doing anything.
	dbMu.Unlock()

	assert.Empty(t, msg, "should skip when DB is already initialized")
}

func TestInitGormLocked_EmptyDsn(t *testing.T) {
	withNonTestModeGlobally(t)
	config.C.DbConnection = ""

	dbMu.Lock()
	msg := initGormLocked()
	dbMu.Unlock()

	assert.Equal(t, "Config DSN is invalid", msg)
}

func TestInitGormLocked_UnsupportedDsn(t *testing.T) {
	// getDsnType() returns "" for any value other than mysql/sqlite/xml, and
	// initGormLocked() treats an empty dsnType as "Config DSN is invalid"
	// (the switch default branch is therefore unreachable in practice).
	withNonTestModeGlobally(t)
	config.C.DbConnection = "postgres"

	dbMu.Lock()
	msg := initGormLocked()
	dbMu.Unlock()

	assert.Equal(t, "Config DSN is invalid", msg)
}

func TestInitGormLocked_SqliteSuccess(t *testing.T) {
	withNonTestModeGlobally(t)

	dbPath := filepath.Join(t.TempDir(), "test_init.db")
	config.C.DbConnection = "sqlite"
	config.C.DbHost = dbPath
	config.C.DevMode = "false"

	dbMu.Lock()
	msg := initGormLocked()
	result := DB
	dbMu.Unlock()

	assert.Empty(t, msg, "sqlite init should succeed without fatal message")
	require.NotNil(t, result, "DB should be set after successful sqlite init")

	// The file should exist on disk.
	_, err := os.Stat(dbPath)
	assert.NoError(t, err, "sqlite database file should have been created")
}

func TestInitGormLocked_SqliteDevMode(t *testing.T) {
	withNonTestModeGlobally(t)

	config.C.DbConnection = "sqlite"
	config.C.DbHost = filepath.Join(t.TempDir(), "test_dev.db")
	config.C.DevMode = "true" // should trigger DB.Debug()

	dbMu.Lock()
	msg := initGormLocked()
	result := DB
	dbMu.Unlock()

	assert.Empty(t, msg, "sqlite init with DevMode should succeed")
	require.NotNil(t, result)
}

// ---------------------------------------------------------------------------
// xml_engine helpers (resolveTableName, quoteDefault, etc.)
// ---------------------------------------------------------------------------

// xmlTestModel is a simple struct for testing the XML migrator.
type xmlTestModel struct {
	ID       uint   `gorm:"primaryKey"`
	Username string `gorm:"size:64;not null"`
	Email    string `gorm:"size:128"`
}

func (*xmlTestModel) TableName() string { return "xml_test_models" }

func TestResolveTableName_Model(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	m := &xmlMigratorWrapper{db: gdb}
	name := m.resolveTableName(&xmlTestModel{})
	assert.Equal(t, "xml_test_models", name)
}

func TestResolveTableName_ParseError(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	m := &xmlMigratorWrapper{db: gdb}
	// Passing a non-struct value causes Parse to fail → returns "".
	name := m.resolveTableName("not-a-struct")
	assert.Empty(t, name)
}

func TestQuoteDefault(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	m := &xmlMigratorWrapper{db: gdb}

	cases := []struct {
		name string
		val  string
		want string
	}{
		{"numeric", "42", "42"},
		{"negative number", "-1", "-1"},
		{"decimal", "3.14", "3.14"},
		{"true", "true", "true"},
		{"false", "false", "false"},
		{"null", "null", "null"},
		{"NULL", "NULL", "NULL"},
		{"string", "hello", "'hello'"},
		{"string with quote", "it's", "'it''s'"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, m.quoteDefault(tc.val))
		})
	}
}

// ---------------------------------------------------------------------------
// resolveColumnType (xml_engine)
// ---------------------------------------------------------------------------

func TestResolveColumnType(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	m := &xmlMigratorWrapper{db: gdb}

	cases := []struct {
		name  string
		model any
		// field DBName whose resolved type we assert on.
		dbName string
		want   string
	}{
		// explicit gorm:"type:..." tag takes priority and is upper-cased.
		// This exercises the first branch of resolveColumnType.
		{"explicit tag type", &xmlTaggedModel{}, "level", "TINYINT"},
		// Without an explicit tag, resolveColumnType delegates to
		// m.db.DataTypeOf(f). For a real SQLite dialector that returns
		// SQLite-native types, which we assert here to lock in the behaviour.
		{"id via dialector", &xmlTestModel{}, "id", "integer PRIMARY KEY AUTOINCREMENT"},
		{"username via dialector", &xmlTestModel{}, "username", "text"},
		{"bool via dialector", &xmlTypeModel{}, "active", "numeric"},
		{"float via dialector", &xmlTypeModel{}, "score", "real"},
		{"bytes via dialector", &xmlTypeModel{}, "data", "blob"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmt := &gorm.Statement{DB: gdb}
			require.NoError(t, stmt.Parse(tc.model))

			for _, f := range stmt.Schema.Fields {
				if f.DBName == tc.dbName {
					assert.Equal(t, tc.want, m.resolveColumnType(f))
					return
				}
			}
			t.Fatalf("field %q not found in model", tc.dbName)
		})
	}
}

// xmlTaggedModel exercises the gorm:"type:" tag path in resolveColumnType.
type xmlTaggedModel struct {
	ID    uint   `gorm:"primaryKey"`
	Level int    `gorm:"type:TINYINT"`
	Name  string `gorm:"size:32"`
}

func (*xmlTaggedModel) TableName() string { return "xml_tagged_models" }

// xmlTypeModel covers the fallback type switch branches.
type xmlTypeModel struct {
	ID        uint `gorm:"primaryKey"`
	Active    bool `gorm:"column:active"`
	Score     float64
	CreatedAt string `gorm:"column:created_at"`
	Data      []byte `gorm:"column:data"`
}

func (*xmlTypeModel) TableName() string { return "xml_type_models" }

// ---------------------------------------------------------------------------
// buildColumnDef (xml_engine)
// ---------------------------------------------------------------------------

func TestBuildColumnDef(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	m := &xmlMigratorWrapper{db: gdb}

	stmt := &gorm.Statement{DB: gdb}
	require.NoError(t, stmt.Parse(&xmlTestModel{}))

	// Username field: size:64, not null.
	for _, f := range stmt.Schema.Fields {
		if f.DBName == "username" {
			def := m.buildColumnDef(f)
			assert.Contains(t, def, "`username`")
			assert.Contains(t, def, "NOT NULL")
			return
		}
	}
	t.Fatalf("username field not found")
}

// ---------------------------------------------------------------------------
// xml migrator no-op ALTER methods
// ---------------------------------------------------------------------------

func TestXmlMigrator_NoOpAlterMethods(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	m := &xmlMigratorWrapper{db: gdb}

	// These three are intentionally no-ops (sqlexec doesn't support ALTER TABLE).
	assert.NoError(t, m.AddColumn(&xmlTestModel{}, "email"))
	assert.NoError(t, m.DropColumn(&xmlTestModel{}, "email"))
	assert.NoError(t, m.AlterColumn(&xmlTestModel{}, "username"))
}

// ---------------------------------------------------------------------------
// GetDBStats / CloseDB / PingDB with a real DB
// ---------------------------------------------------------------------------

// withInjectedDB installs a fresh in-memory SQLite DB into the global DB
// handle and restores the original state on cleanup.
func withInjectedDB(t *testing.T) *gorm.DB {
	t.Helper()

	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	dbMu.Lock()
	savedDB := DB
	savedMode := isTestMode
	DB = gdb
	dbMu.Unlock()

	t.Cleanup(func() {
		dbMu.Lock()
		// If a test replaced DB, close that one; otherwise close ours.
		toClose := DB
		DB = savedDB
		isTestMode = savedMode
		dbMu.Unlock()

		if toClose != nil {
			_ = closeGormSilently(toClose)
		}
	})

	return gdb
}

func TestGetDBStats_RealDB(t *testing.T) {
	withInjectedDB(t)

	stats, err := GetDBStats()
	require.NoError(t, err)
	require.NotNil(t, stats)

	// Verify the stats map contains the documented keys.
	for _, key := range []string{
		"open_connections", "in_use", "idle",
		"wait_count", "wait_duration_ms",
		"max_idle_closed", "max_lifetime_closed",
	} {
		assert.Contains(t, stats, key, "stats should contain key %q", key)
	}
}

func TestPingDB_RealDB(t *testing.T) {
	withInjectedDB(t)

	// An in-memory SQLite DB responds to Ping immediately.
	err := PingDB()
	assert.NoError(t, err)
}

func TestCloseDB_RealDB(t *testing.T) {
	gdb := withInjectedDB(t)

	err := CloseDB()
	assert.NoError(t, err)

	// After CloseDB, the global handle is cleared; subsequent PingDB returns
	// os.ErrInvalid.
	assert.Equal(t, os.ErrInvalid, PingDB())

	// The underlying pool should now be closed.
	sqlDB, dbErr := gdb.DB()
	if dbErr == nil {
		assert.True(t, sqlDB.Stats().OpenConnections == 0,
			"after CloseDB there should be no open connections")
	}
}
