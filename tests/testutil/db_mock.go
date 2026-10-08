package testutil

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/kasuganosora/sqlexec/pkg/api"
	sqlexecgorm "github.com/kasuganosora/sqlexec/pkg/api/gorm"
	"github.com/kasuganosora/sqlexec/pkg/resource/domain"
	"github.com/kasuganosora/sqlexec/pkg/resource/memory"
	"gorm.io/gorm"
)

// TestDB holds the test database connection.
type TestDB struct {
	DB      *api.DB
	GormDB  *gorm.DB
	Session *api.Session

	// ownsDB controls whether Close() closes the underlying *api.DB. Borrowed
	// views (e.g. those returned by SetupSharedTestDB, which share the global
	// singleton) set this to false so they only close their own Session and
	// never tear down the shared database out from under other tests.
	ownsDB bool

	// closeFn releases a PostgreSQL test schema (see postgres.go).
	closeFn func()
}

// DBMode defines the test database mode.
type DBMode int

const (
	// ModeIsolated creates a new isolated database for each test (recommended for parallel tests).
	ModeIsolated DBMode = iota
	// ModeShared uses a shared database with data cleanup between tests (better performance).
	ModeShared
)

var (
	sharedDB     *TestDB
	sharedDBOnce sync.Once
	errSharedDB  error
	sharedDBMu   sync.Mutex
)

// NewTestDB creates a new in-memory test database using SQLExec.
//
// Deprecated: Use NewIsolatedTestDB instead, which provides proper isolation
// with unique data source names and automatic cleanup via t.Cleanup.
func NewTestDB() (*TestDB, error) {
	db, err := api.NewDB(nil)
	if err != nil {
		return nil, err
	}

	memDS := memory.NewMVCCDataSource(&domain.DataSourceConfig{
		Type:     domain.DataSourceTypeMemory,
		Name:     "default",
		Writable: true,
	})
	if err := memDS.Connect(context.Background()); err != nil {
		_ = db.Close()

		return nil, err
	}

	if err := db.RegisterDataSource("default", memDS); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("failed to register datasource: %w", err)
	}

	if err := db.SetDefaultDataSource("default"); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("failed to set default datasource: %w", err)
	}

	session := db.Session()

	gormDB, err := gorm.Open(
		sqlexecgorm.NewDialector(session),
		&gorm.Config{SkipDefaultTransaction: true},
	)
	if err != nil {
		_ = session.Close()
		_ = db.Close()

		return nil, err
	}

	return &TestDB{
		DB:      db,
		GormDB:  gormDB,
		Session: session,
		ownsDB:  true,
	}, nil
}

// NewIsolatedTestDB creates a new isolated test database with unique name.
// This is the recommended approach for tests (including parallel tests),
// following sqlexec testing best practices (Approach 3: GORM Integration).
func NewIsolatedTestDB(t interface {
	Helper()
	Cleanup(func())
	Fatalf(format string, args ...any)
}) (*TestDB, error) {
	t.Helper()

	if UsePostgres() {
		gdb, cleanup, err := openPostgresTestDB(&gorm.Config{SkipDefaultTransaction: true, DisableForeignKeyConstraintWhenMigrating: true})
		if err != nil {
			return nil, err
		}

		t.Cleanup(cleanup)

		return &TestDB{GormDB: gdb}, nil
	}

	dsName := "test_" + uuid.New().String()[:8]

	db, err := api.NewDB(nil)
	if err != nil {
		return nil, err
	}

	memDS := memory.NewMVCCDataSource(&domain.DataSourceConfig{
		Type:     domain.DataSourceTypeMemory,
		Name:     dsName,
		Writable: true,
	})
	if err := memDS.Connect(context.Background()); err != nil {
		_ = db.Close()

		return nil, err
	}

	if err := db.RegisterDataSource(dsName, memDS); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("failed to register datasource: %w", err)
	}

	if err := db.SetDefaultDataSource(dsName); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("failed to set default datasource: %w", err)
	}

	session := db.Session()

	gormDB, err := gorm.Open(
		sqlexecgorm.NewDialector(session),
		&gorm.Config{SkipDefaultTransaction: true},
	)
	if err != nil {
		_ = session.Close()
		_ = db.Close()

		return nil, err
	}

	testDB := &TestDB{
		DB:      db,
		GormDB:  gormDB,
		Session: session,
		ownsDB:  true,
	}

	t.Cleanup(func() {
		testDB.Close()
	})

	return testDB, nil
}

// newTestDBNoCleanup creates an isolated test database without registering t.Cleanup.
// Used internally by NewTestSetup which manages cleanup itself.
func newTestDBNoCleanup() (*TestDB, error) {
	if UsePostgres() {
		gdb, cleanup, err := openPostgresTestDB(&gorm.Config{SkipDefaultTransaction: true, DisableForeignKeyConstraintWhenMigrating: true})
		if err != nil {
			return nil, err
		}

		return &TestDB{GormDB: gdb, closeFn: cleanup}, nil
	}

	dsName := "test_" + uuid.New().String()[:8]

	db, err := api.NewDB(nil)
	if err != nil {
		return nil, err
	}

	memDS := memory.NewMVCCDataSource(&domain.DataSourceConfig{
		Type:     domain.DataSourceTypeMemory,
		Name:     dsName,
		Writable: true,
	})
	if err := memDS.Connect(context.Background()); err != nil {
		_ = db.Close()

		return nil, err
	}

	if err := db.RegisterDataSource(dsName, memDS); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("failed to register datasource: %w", err)
	}

	if err := db.SetDefaultDataSource(dsName); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("failed to set default datasource: %w", err)
	}

	session := db.Session()

	gormDB, err := gorm.Open(
		sqlexecgorm.NewDialector(session),
		&gorm.Config{SkipDefaultTransaction: true},
	)
	if err != nil {
		_ = session.Close()
		_ = db.Close()

		return nil, err
	}

	return &TestDB{
		DB:      db,
		GormDB:  gormDB,
		Session: session,
		ownsDB:  true,
	}, nil
}

// GetSharedTestDB returns a shared test database.
// Data should be cleaned between tests using TruncateTables.
// Not safe for parallel tests.
func GetSharedTestDB() (*TestDB, error) {
	sharedDBOnce.Do(func() {
		sharedDB, errSharedDB = NewTestDB()
	})

	return sharedDB, errSharedDB
}

// SetupSharedTestDB sets up a shared test database with automatic cleanup.
// Truncates the specified tables before each test to ensure data isolation.
// Not safe for parallel tests.
func SetupSharedTestDB(t *testing.T, tables ...string) (*TestDB, error) {
	t.Helper()
	sharedDBMu.Lock()
	defer sharedDBMu.Unlock()

	db, err := GetSharedTestDB()
	if err != nil {
		return nil, err
	}

	// Truncate tables to ensure clean state (per sqlexec doc Approach 2)
	if len(tables) > 0 {
		err := db.TruncateTables(tables...)
		if err != nil {
			return nil, fmt.Errorf("failed to truncate tables: %w", err)
		}
	}

	// Create a new session for this test
	session := db.DB.Session()

	t.Cleanup(func() {
		_ = session.Close()
	})

	return &TestDB{
		DB:      db.DB,
		GormDB:  db.GormDB,
		Session: session,
		// ownsDB stays false: this is a borrowed view over the shared
		// singleton; Close() must not tear down the shared database.
	}, nil
}

// Close closes the test database.
//
// For a borrowed (shared) view, only the session owned by this view is closed;
// the underlying *api.DB is left open because it is shared with other tests.
// Close is safe to call multiple times.
func (t *TestDB) Close() {
	if t.closeFn != nil {
		t.closeFn()
		t.closeFn = nil
	}

	if t.Session != nil {
		_ = t.Session.Close()
		t.Session = nil
	}

	if t.ownsDB && t.DB != nil {
		_ = t.DB.Close()
		t.DB = nil
	}
}

// AutoMigrate runs auto migration for given models.
func (t *TestDB) AutoMigrate(models ...any) error {
	return t.GormDB.AutoMigrate(models...)
}

// TruncateTables clears data from specified tables.
func (t *TestDB) TruncateTables(tables ...string) error {
	if t.Session == nil {
		for _, table := range tables {
			if err := t.GormDB.Exec("TRUNCATE TABLE " + table).Error; err != nil {
				return err
			}
		}

		return nil
	}

	for _, table := range tables {
		_, err := t.Session.Execute("TRUNCATE TABLE " + table)
		if err != nil {
			return err
		}
	}

	return nil
}

// CreateTable creates a table using raw SQL.
func (t *TestDB) CreateTable(sql string) error {
	if t.Session == nil {
		return t.GormDB.Exec(sql).Error
	}

	_, err := t.Session.Execute(sql)

	return err
}

// CreateTables creates multiple tables.
func (t *TestDB) CreateTables(sqls ...string) error {
	for _, sql := range sqls {
		err := t.CreateTable(sql)
		if err != nil {
			return err
		}
	}

	return nil
}

// Exec executes a raw SQL statement.
func (t *TestDB) Exec(sql string, args ...any) error {
	if t.Session == nil {
		return t.GormDB.Exec(sql, args...).Error
	}

	_, err := t.Session.Execute(sql, args...)

	return err
}

// QueryOne executes a query and returns one row.
func (t *TestDB) QueryOne(sql string, args ...any) (map[string]any, error) {
	if t.Session == nil {
		row := map[string]any{}
		err := t.GormDB.Raw(sql, args...).Take(&row).Error

		return row, err
	}

	return t.Session.QueryOne(sql, args...)
}

// QueryAll executes a query and returns all rows.
func (t *TestDB) QueryAll(sql string, args ...any) ([]map[string]any, error) {
	if t.Session == nil {
		var rows []map[string]any
		err := t.GormDB.Raw(sql, args...).Scan(&rows).Error

		return rows, err
	}

	rows, err := t.Session.QueryAll(sql, args...)
	if err != nil {
		return nil, err
	}

	result := make([]map[string]any, len(rows))
	for i, row := range rows {
		result[i] = make(map[string]any)
		maps.Copy(result[i], row)
	}

	return result, nil
}
