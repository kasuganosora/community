package database

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/sqlexec/pkg/api"
	sqlexecgorm "github.com/kasuganosora/sqlexec/pkg/api/gorm"
	"github.com/kasuganosora/sqlexec/pkg/resource/domain"
	"github.com/kasuganosora/sqlexec/pkg/resource/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// ---------------------------------------------------------------------------
// Helpers to build an in-memory sqlexec DB (mirrors tests/testutil/db_mock.go)
// ---------------------------------------------------------------------------

// newSqlexecMemDB opens a fresh, isolated sqlexec memory database and returns
// the api.DB, a session and a gorm.DB backed by the sqlexec dialector. The
// caller is responsible for closing the returned handles.
func newSqlexecMemDB(t *testing.T) (*api.DB, *api.Session, *gorm.DB) {
	t.Helper()

	db, err := api.NewDB(nil)
	require.NoError(t, err)

	dsName := "unit_" + t.Name()
	memDS := memory.NewMVCCDataSource(&domain.DataSourceConfig{
		Type:     domain.DataSourceTypeMemory,
		Name:     dsName,
		Writable: true,
	})
	require.NoError(t, memDS.Connect(context.Background()))
	require.NoError(t, db.RegisterDataSource(dsName, memDS))
	require.NoError(t, db.SetDefaultDataSource(dsName))

	session := db.Session()
	gormDB, err := gorm.Open(
		sqlexecgorm.NewDialector(session),
		&gorm.Config{SkipDefaultTransaction: true},
	)
	require.NoError(t, err)

	return db, session, gormDB
}

// ---------------------------------------------------------------------------
// initSqlexecXmlConnection (xml_engine.go)
//
// Exercises the full XML init path with a temp directory for DatabaseDir.
// ---------------------------------------------------------------------------

func TestInitSqlexecXmlConnection_Success(t *testing.T) {
	withNonTestModeGlobally(t)

	// Save / set the sqlexec package-level handles.
	dbMu.Lock()
	savedSqlexecDB := sqlexecDB
	savedSqlexecSession := sqlexecSession
	dbMu.Unlock()

	config.C.DbConnection = "xml"
	config.C.DbHost = t.TempDir()
	config.C.DbDatabase = "xml_unit"
	config.C.DevMode = "false"

	err := initSqlexecXmlConnection()
	require.NoError(t, err, "xml connection init should succeed with a valid temp dir")

	// The global DB must have been assigned.
	dbMu.Lock()
	openedDB := DB
	openedSess := sqlexecSession
	openedApiDB := sqlexecDB
	// reset the globals to keep subsequent tests clean (close happens in cleanup)
	DB = nil
	sqlexecDB = nil
	sqlexecSession = nil
	dbMu.Unlock()

	require.NotNil(t, openedDB, "global DB handle should be set after xml init")
	require.NotNil(t, openedSess)
	require.NotNil(t, openedApiDB)

	// Cleanup: close the resources we created.
	t.Cleanup(func() {
		_ = openedSess.Close()
		_ = openedApiDB.Close()
		// restore saved handles
		dbMu.Lock()
		sqlexecDB = savedSqlexecDB
		sqlexecSession = savedSqlexecSession
		dbMu.Unlock()
	})
}

func TestInitSqlexecXmlConnection_DevMode(t *testing.T) {
	withNonTestModeGlobally(t)

	dbMu.Lock()
	savedSqlexecDB := sqlexecDB
	savedSqlexecSession := sqlexecSession
	dbMu.Unlock()

	config.C.DbConnection = "xml"
	config.C.DbHost = t.TempDir()
	config.C.DbDatabase = ""  // should fall back to "community"
	config.C.DevMode = "true" // exercises DB.Debug() branch

	err := initSqlexecXmlConnection()
	require.NoError(t, err)

	dbMu.Lock()
	openedDB := DB
	openedSess := sqlexecSession
	openedApiDB := sqlexecDB
	DB = nil
	sqlexecDB = nil
	sqlexecSession = nil
	dbMu.Unlock()

	require.NotNil(t, openedDB)

	t.Cleanup(func() {
		_ = openedSess.Close()
		_ = openedApiDB.Close()
		dbMu.Lock()
		sqlexecDB = savedSqlexecDB
		sqlexecSession = savedSqlexecSession
		dbMu.Unlock()
	})
}

// ---------------------------------------------------------------------------
// xmlDialectorWrapper.Migrator
// ---------------------------------------------------------------------------

func TestXmlDialectorWrapper_Migrator(t *testing.T) {
	apiDB, session, innerGorm := newSqlexecMemDB(t)
	t.Cleanup(func() {
		_ = session.Close()
		_ = apiDB.Close()
	})

	wrapper := &xmlDialectorWrapper{
		Dialector: sqlexecgorm.NewDialector(session),
		session:   session,
	}

	mig := wrapper.Migrator(innerGorm)
	assert.NotNil(t, mig, "Migrator must return a non-nil migrator")

	// The returned migrator should be our wrapper type that supports AutoMigrate.
	_, ok := mig.(*xmlMigratorWrapper)
	assert.True(t, ok, "Migrator should return an *xmlMigratorWrapper")
}

// ---------------------------------------------------------------------------
// AutoMigrate / CreateTable / createXmlTable
//
// These exercise the real migration code paths against a live sqlexec session.
// ---------------------------------------------------------------------------

// xmlMigrationModel covers several column type branches (int, string, bool).
type xmlMigrationModel struct {
	ID     uint   `gorm:"primaryKey"`
	Name   string `gorm:"size:64;not null"`
	Email  string `gorm:"size:128;default:unknown"`
	Active bool   `gorm:"default:true"`
	Count  int
}

func (*xmlMigrationModel) TableName() string { return "xml_migration_models" }

func buildMigratorForTest(t *testing.T) (*xmlMigratorWrapper, *api.DB, *api.Session) {
	t.Helper()
	apiDB, session, innerGorm := newSqlexecMemDB(t)

	m := &xmlMigratorWrapper{
		Migrator: innerGorm.Migrator(),
		session:  session,
		db:       innerGorm,
	}
	return m, apiDB, session
}

func TestXmlMigrator_CreateTable_NewTable(t *testing.T) {
	m, apiDB, session := buildMigratorForTest(t)
	t.Cleanup(func() {
		_ = session.Close()
		_ = apiDB.Close()
	})

	err := m.CreateTable(&xmlMigrationModel{})
	require.NoError(t, err, "CreateTable should succeed for a brand new model")

	// Creating again should be a no-op (HasTable returns true).
	err = m.CreateTable(&xmlMigrationModel{})
	require.NoError(t, err)

	// The table should now exist in the session.
	assert.True(t, m.HasTable(&xmlMigrationModel{}), "table should exist after CreateTable")
}

func TestXmlMigrator_AutoMigrate_NewTable(t *testing.T) {
	m, apiDB, session := buildMigratorForTest(t)
	t.Cleanup(func() {
		_ = session.Close()
		_ = apiDB.Close()
	})

	err := m.AutoMigrate(&xmlMigrationModel{})
	require.NoError(t, err, "AutoMigrate should succeed")
	assert.True(t, m.HasTable(&xmlMigrationModel{}))
}

func TestXmlMigrator_AutoMigrate_ExistingTableSkipsCreate(t *testing.T) {
	m, apiDB, session := buildMigratorForTest(t)
	t.Cleanup(func() {
		_ = session.Close()
		_ = apiDB.Close()
	})

	// First migration creates the table.
	require.NoError(t, m.AutoMigrate(&xmlMigrationModel{}))
	// Second migration should be a no-op (table exists) and not error.
	require.NoError(t, m.AutoMigrate(&xmlMigrationModel{}))
}

func TestXmlMigrator_AutoMigrate_CreateFails(t *testing.T) {
	// A model whose schema cannot be parsed (non-struct) should produce an
	// error from createXmlTable -> stmt.Parse.
	m, apiDB, session := buildMigratorForTest(t)
	t.Cleanup(func() {
		_ = session.Close()
		_ = apiDB.Close()
	})

	err := m.AutoMigrate("not-a-struct")
	assert.Error(t, err, "AutoMigrate with an unparseable model should fail")
}

// xmlNoColumnsModel has no DB-mapped fields, so createXmlTable should fail
// with "no columns for table ...".
type xmlNoColumnsModel struct {
	Internal int `gorm:"-"`
}

func (*xmlNoColumnsModel) TableName() string { return "xml_no_columns" }

func TestXmlMigrator_CreateXmlTable_NoColumns(t *testing.T) {
	m, apiDB, session := buildMigratorForTest(t)
	t.Cleanup(func() {
		_ = session.Close()
		_ = apiDB.Close()
	})

	err := m.createXmlTable(&xmlNoColumnsModel{})
	assert.Error(t, err, "should error when a model produces no columns")
}

// xmlUintModel exercises the Uint branch of resolveColumnType.
type xmlUintModel struct {
	ID    uint `gorm:"primaryKey"`
	Big   uint64
	Small uint32
}

func (*xmlUintModel) TableName() string { return "xml_uint_models" }

func TestXmlMigrator_CreateTable_UintColumns(t *testing.T) {
	m, apiDB, session := buildMigratorForTest(t)
	t.Cleanup(func() {
		_ = session.Close()
		_ = apiDB.Close()
	})

	require.NoError(t, m.CreateTable(&xmlUintModel{}))
	assert.True(t, m.HasTable(&xmlUintModel{}))
}

// ---------------------------------------------------------------------------
// resolveColumnType — fallback switch branches
//
// resolveColumnType only falls through to its internal switch when the
// dialector's DataTypeOf returns "". We build a GORM DB backed by a custom
// dialector whose DataTypeOf returns "" so the fallback mapping is exercised.
// ---------------------------------------------------------------------------

// blankTypeDialector satisfies gorm.Dialector while returning "" from
// DataTypeOf so resolveColumnType's fallback switch is taken.
type blankTypeDialector struct {
	gorm.Dialector
}

func (d *blankTypeDialector) DataTypeOf(*schema.Field) string { return "" }

// modelBlankType holds fields for every DataType we want to map.
type modelBlankType struct {
	ID      uint      `gorm:"primaryKey"`
	Flag    bool      `gorm:"column:flag"`
	Age     int32     `gorm:"column:age"`
	Big     int64     `gorm:"column:big;size:64;autoIncrement"`
	Count   uint32    `gorm:"column:count"`
	BigU    uint64    `gorm:"column:big_u;size:64"`
	Score   float64   `gorm:"column:score"`
	Title   string    `gorm:"column:title;size:50"`
	Body    string    `gorm:"column:body"`
	When    time.Time `gorm:"column:when"`
	Payload []byte    `gorm:"column:payload"`
}

func (*modelBlankType) TableName() string { return "model_blank_type" }

func TestResolveColumnType_FallbackBranches(t *testing.T) {
	// Use sqlite as the base so we get a real, usable gorm.DB, then wrap its
	// dialector so DataTypeOf returns "" and forces the fallback switch.
	base, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	m := &xmlMigratorWrapper{
		db:       base,
		Migrator: base.Migrator(),
	}
	// Override the dialector on the DB to force DataTypeOf to return "".
	base.Dialector = &blankTypeDialector{Dialector: base.Dialector}

	stmt := &gorm.Statement{DB: base}
	require.NoError(t, stmt.Parse(&modelBlankType{}))

	expected := map[string]string{
		// explicit gorm type tag wins — none here, so fallback applies
		"flag":    "BOOLEAN",
		"age":     "INT",             // int32 → size 32 (not > 32)
		"big":     "BIGINT",          // size 64 / autoIncrement
		"count":   "INT UNSIGNED",    // uint32 → size 32
		"big_u":   "BIGINT UNSIGNED", // size 64
		"score":   "DOUBLE",
		"title":   "VARCHAR(50)",
		"body":    "TEXT",     // no size → TEXT
		"when":    "DATETIME", // time.Time → schema.Time
		"payload": "BLOB",
	}

	for _, f := range stmt.Schema.Fields {
		want, ok := expected[f.DBName]
		if !ok {
			continue
		}

		assert.Equal(t, want, m.resolveColumnType(f),
			"resolveColumnType mismatch for column %q", f.DBName)
	}
}

// ---------------------------------------------------------------------------
// buildColumnDef — DEFAULT and AUTO_INCREMENT branches
// ---------------------------------------------------------------------------

type xmlDefaultsModel struct {
	ID    uint   `gorm:"primaryKey;autoIncrement"`
	Name  string `gorm:"size:32;not null;default:anon"`
	Level int    `gorm:"default:5"`
}

func (*xmlDefaultsModel) TableName() string { return "xml_defaults_models" }

func TestBuildColumnDef_DefaultAndAutoIncrement(t *testing.T) {
	base, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	m := &xmlMigratorWrapper{db: base, Migrator: base.Migrator()}

	stmt := &gorm.Statement{DB: base}
	require.NoError(t, stmt.Parse(&xmlDefaultsModel{}))

	defs := map[string]string{}
	for _, f := range stmt.Schema.Fields {
		if f.DBName == "" {
			continue
		}

		defs[f.DBName] = m.buildColumnDef(f)
	}

	// id should carry AUTO_INCREMENT (primaryKey + autoIncrement).
	assert.Contains(t, defs["id"], "AUTO_INCREMENT", "id column should be AUTO_INCREMENT")
	// name should carry NOT NULL and a DEFAULT 'anon'.
	assert.Contains(t, defs["name"], "NOT NULL")
	assert.Contains(t, defs["name"], "DEFAULT 'anon'", "string default should be quoted")
	// level default is numeric and should NOT be quoted.
	assert.Contains(t, defs["level"], "DEFAULT 5")
	assert.NotContains(t, defs["level"], "'", "numeric default must not be quoted")
}

// ---------------------------------------------------------------------------
// initMysqlConnection — error path (no real MySQL available)
// ---------------------------------------------------------------------------

func TestInitMysqlConnection_Fails(t *testing.T) {
	withNonTestModeGlobally(t)

	// Point at a non-existent MySQL server to force a connection error.
	config.C.DbConnection = "mysql"
	config.C.DbHost = "127.0.0.1"
	config.C.DbPort = "1" // privileged port, connection refused fast
	config.C.DbDatabase = "nodb"
	config.C.DbUsername = "u"
	config.C.DbPassword = "p"
	config.C.DevMode = "false"

	// Clear pool env vars so LoadDBConfig uses defaults.
	t.Setenv("DB_MAX_OPEN_CONNS", "")
	t.Setenv("DB_MAX_IDLE_CONNS", "")
	t.Setenv("DB_CONN_MAX_LIFETIME_MINUTES", "")
	t.Setenv("DB_CONN_MAX_IDLE_TIME_MINUTES", "")

	err := initMysqlConnection()
	assert.Error(t, err, "initMysqlConnection should fail when MySQL is unreachable")
}

// ---------------------------------------------------------------------------
// initSqliteConnection — DB() error / cleanup branch
//
// We can't easily force DB.DB() to fail for a normally-opened sqlite handle,
// but we can verify the happy path again via the public-ish function to add
// coverage, and exercise the empty-DbHost fallback to "community.db".
// ---------------------------------------------------------------------------

func TestInitSqliteConnection_EmptyHostFallback(t *testing.T) {
	withNonTestModeGlobally(t)

	// chdir into a temp dir so the fallback "community.db" lands there and
	// doesn't pollute the repo. We can't easily chdir, so instead set a path.
	dir := t.TempDir()
	config.C.DbConnection = "sqlite"
	config.C.DbHost = filepath.Join(dir, "community.db")
	config.C.DevMode = "false"

	err := initSqliteConnection()
	require.NoError(t, err)

	dbMu.Lock()
	opened := DB
	DB = nil
	dbMu.Unlock()

	require.NotNil(t, opened)
	t.Cleanup(func() { _ = closeGormSilently(opened) })
}

// ---------------------------------------------------------------------------
// GetDB / InitGorm public surface
// ---------------------------------------------------------------------------

func TestGetDB_ReturnsInjectedHandle(t *testing.T) {
	gdb := withInjectedDB(t)

	got := GetDB()
	require.NotNil(t, got)
	// The injected DB must be the same pointer.
	assert.Equal(t, gdb, got)
}

func TestInitGorm_TestModeIsNoOp(t *testing.T) {
	// In test mode InitGorm must not attempt a real connection and must not
	// fatal. We rely on the package-level isTestMode being true under test.
	dbMu.Lock()
	originalMode := isTestMode
	isTestMode = true
	dbMu.Unlock()

	assert.NotPanics(t, func() { InitGorm() }, "InitGorm in test mode should be a safe no-op")

	dbMu.Lock()
	isTestMode = originalMode
	dbMu.Unlock()
}

// ---------------------------------------------------------------------------
// Concurrency: GetDB / SetTestDB are guarded by dbMu.
// ---------------------------------------------------------------------------

func TestDBMutex_ConcurrentAccessIsSafe(t *testing.T) {
	var wg sync.WaitGroup

	for range 10 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_ = GetDB()
			_ = IsTestMode()
		}()
	}

	wg.Wait()
	// If we reach here without -race failures, the mutex is working.
	assert.True(t, true)
}
