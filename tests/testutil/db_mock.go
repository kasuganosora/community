package testutil

import (
	"context"

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

// NewTestDB creates a new in-memory test database using SQLExec
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

// Close closes the test database
func (t *TestDB) Close() {
	if t.DB != nil {
		t.DB.Close()
	}
}

// AutoMigrate runs auto migration for given models
func (t *TestDB) AutoMigrate(models ...interface{}) error {
	return t.GormDB.AutoMigrate(models...)
}
