package testutil

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/kasuganosora/sqlexec/pkg/api"
	sqlexecgorm "github.com/kasuganosora/sqlexec/pkg/api/gorm"
	"github.com/kasuganosora/sqlexec/pkg/resource/domain"
	"github.com/kasuganosora/sqlexec/pkg/resource/memory"
	"gorm.io/gorm"
)

// TestDB holds the test database connection
type TestDB struct {
	DB      *api.DB
	GormDB  *gorm.DB
	Session *api.Session
}

// DBMode defines the test database mode
type DBMode int

const (
	// ModeIsolated creates a new isolated database for each test (recommended for parallel tests)
	ModeIsolated DBMode = iota
	// ModeShared uses a shared database with data cleanup between tests (better performance)
	ModeShared
)

var (
	sharedDB     *TestDB
	sharedDBOnce sync.Once
	sharedDBMu   sync.Mutex
)

// NewTestDB creates a new in-memory test database using SQLExec
// This creates an isolated database for each test
func NewTestDB() (*TestDB, error) {
	// 1. 创建 SQLExec 数据库
	db, err := api.NewDB(nil)
	if err != nil {
		return nil, err
	}

	// 2. 注册内存数据源
	memDS := memory.NewMVCCDataSource(&domain.DataSourceConfig{
		Type:     domain.DataSourceTypeMemory,
		Name:     "default",
		Writable: true,
	})
	if err := memDS.Connect(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	db.RegisterDataSource("default", memDS)

	// 3. 创建 Session
	session := db.Session()

	// 4. 创建 GORM 连接
	gormDB, err := gorm.Open(
		sqlexecgorm.NewDialector(session),
		&gorm.Config{SkipDefaultTransaction: true},
	)
	if err != nil {
		db.Close()
		return nil, err
	}

	return &TestDB{
		DB:      db,
		GormDB:  gormDB,
		Session: session,
	}, nil
}

// NewIsolatedTestDB creates a new isolated test database with unique name
// This is recommended for parallel tests as each test gets its own database
func NewIsolatedTestDB(t interface{ Cleanup(func()) }) (*TestDB, error) {
	// Use unique name for complete isolation
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
		db.Close()
		return nil, err
	}
	db.RegisterDataSource(dsName, memDS)
	db.SetDefaultDataSource(dsName)

	session := db.Session()

	gormDB, err := gorm.Open(
		sqlexecgorm.NewDialector(session),
		&gorm.Config{SkipDefaultTransaction: true},
	)
	if err != nil {
		session.Close()
		db.Close()
		return nil, err
	}

	testDB := &TestDB{
		DB:      db,
		GormDB:  gormDB,
		Session: session,
	}

	t.Cleanup(func() {
		testDB.Close()
	})

	return testDB, nil
}

// GetSharedTestDB returns a shared test database
// Data should be cleaned between tests using TruncateTables
// Not safe for parallel tests
func GetSharedTestDB() (*TestDB, error) {
	var initErr error
	sharedDBOnce.Do(func() {
		sharedDB, initErr = NewTestDB()
	})
	if initErr != nil {
		return nil, initErr
	}
	return sharedDB, nil
}

// SetupSharedTestDB sets up a shared test database with automatic cleanup
// Returns a session that should be used for the test
// Not safe for parallel tests
func SetupSharedTestDB(t interface{ Cleanup(func()) }) (*TestDB, error) {
	sharedDBMu.Lock()
	defer sharedDBMu.Unlock()

	db, err := GetSharedTestDB()
	if err != nil {
		return nil, err
	}

	// Create a new session for this test
	session := db.DB.Session()

	t.Cleanup(func() {
		session.Close()
	})

	return &TestDB{
		DB:      db.DB,
		GormDB:  db.GormDB,
		Session: session,
	}, nil
}

// Close closes the test database
func (t *TestDB) Close() {
	if t.Session != nil {
		t.Session.Close()
	}
	if t.DB != nil {
		t.DB.Close()
	}
}

// AutoMigrate runs auto migration for given models
func (t *TestDB) AutoMigrate(models ...interface{}) error {
	return t.GormDB.AutoMigrate(models...)
}

// TruncateTables clears data from specified tables
func (t *TestDB) TruncateTables(tables ...string) error {
	for _, table := range tables {
		_, err := t.Session.Execute("DELETE FROM " + table)
		if err != nil {
			return err
		}
	}
	return nil
}

// CreateTable creates a table using raw SQL
func (t *TestDB) CreateTable(sql string) error {
	_, err := t.Session.Execute(sql)
	return err
}

// CreateTables creates multiple tables
func (t *TestDB) CreateTables(sqls ...string) error {
	for _, sql := range sqls {
		if err := t.CreateTable(sql); err != nil {
			return err
		}
	}
	return nil
}

// Exec executes a raw SQL statement
func (t *TestDB) Exec(sql string, args ...interface{}) error {
	_, err := t.Session.Execute(sql, args...)
	return err
}

// QueryOne executes a query and returns one row
func (t *TestDB) QueryOne(sql string, args ...interface{}) (map[string]interface{}, error) {
	return t.Session.QueryOne(sql, args...)
}

// QueryAll executes a query and returns all rows
func (t *TestDB) QueryAll(sql string, args ...interface{}) ([]map[string]interface{}, error) {
	rows, err := t.Session.QueryAll(sql, args...)
	if err != nil {
		return nil, err
	}
	// Convert domain.Row to map[string]interface{}
	result := make([]map[string]interface{}, len(rows))
	for i, row := range rows {
		result[i] = make(map[string]interface{})
		for k, v := range row {
			result[i][k] = v
		}
	}
	return result, nil
}
