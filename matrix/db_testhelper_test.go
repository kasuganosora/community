package matrix

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	sqlexecapi "github.com/kasuganosora/sqlexec/pkg/api"
	sqlexecgorm "github.com/kasuganosora/sqlexec/pkg/api/gorm"
	"github.com/kasuganosora/sqlexec/pkg/resource/domain"
	"github.com/kasuganosora/sqlexec/pkg/resource/memory"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// matrixDBCache caches a single shared in-memory sqlexec database for the
// matrix test package. We reuse one *gorm.DB across tests and migrate all
// models once, then truncate between tests.
type matrixDBCache struct {
	gormDB   *gorm.DB
	session  *sqlexecapi.Session
	apiDB    *sqlexecapi.DB
	once     sync.Once
	err      error
	migrated bool
}

var mDBCache matrixDBCache

// setupMatrixTestDB returns a gorm.DB backed by an in-memory sqlexec database
// and injects it into the global database package so that db.GetDB() returns
// it. It auto-migrates all matrix-relevant models on first use. Call
// truncateMatrixTables between tests for isolation.
func setupMatrixTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	mDBCache.once.Do(func() {
		if pgtest.Enabled() {
			mDBCache.gormDB, mDBCache.err = pgtest.OpenShared("t_matrix_pkg", &gorm.Config{SkipDefaultTransaction: true, DisableForeignKeyConstraintWhenMigrating: true})

			return
		}

		dsName := "matrix_test_" + uuid.New().String()[:8]

		db, err := sqlexecapi.NewDB(nil)
		if err != nil {
			mDBCache.err = err
			return
		}

		memDS := memory.NewMVCCDataSource(&domain.DataSourceConfig{
			Type:     domain.DataSourceTypeMemory,
			Name:     dsName,
			Writable: true,
		})
		if err := memDS.Connect(context.Background()); err != nil {
			_ = db.Close()
			mDBCache.err = err
			return
		}

		if err := db.RegisterDataSource(dsName, memDS); err != nil {
			_ = db.Close()
			mDBCache.err = fmt.Errorf("failed to register datasource: %w", err)
			return
		}

		if err := db.SetDefaultDataSource(dsName); err != nil {
			_ = db.Close()
			mDBCache.err = fmt.Errorf("failed to set default datasource: %w", err)
			return
		}

		session := db.Session()

		gormDB, err := gorm.Open(
			sqlexecgorm.NewDialector(session),
			&gorm.Config{SkipDefaultTransaction: true},
		)
		if err != nil {
			_ = session.Close()
			_ = db.Close()
			mDBCache.err = err
			return
		}

		mDBCache.apiDB = db
		mDBCache.session = session
		mDBCache.gormDB = gormDB
	})

	if mDBCache.err != nil {
		t.Fatalf("failed to set up test DB: %v", mDBCache.err)
	}

	if !mDBCache.migrated {
		if err := mDBCache.gormDB.AutoMigrate(matrixTestModels()...); err != nil {
			t.Fatalf("failed to auto-migrate: %v", err)
		}
		mDBCache.migrated = true
	}

	// Inject into the global database package so db.GetDB() works.
	database.SetTestDB(mDBCache.gormDB)

	return mDBCache.gormDB
}

// matrixTestModels returns the models needed for matrix package tests.
func matrixTestModels() []any {
	return []any{
		&models.User{},
		&models.Account{},
		&models.AccountExtra{},
		&models.Conversation{},
		&models.AccountConversation{},
		&models.Status{},
		&models.StatusContent{},
		// Status conversation hooks read mentions when saving direct messages.
		&models.StatusToAccount{},
		&models.MatrixUser{},
		&models.MatrixRoom{},
		&models.MatrixMessage{},
		&models.Group{},
		&models.GroupMember{},
		&models.GroupMessage{},
		&models.Contact{},
		&models.ContactRequest{},
	}
}

// truncateMatrixTables clears all matrix-related tables between tests.
func truncateMatrixTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	models := matrixTestModels()
	// Truncate in reverse order to avoid foreign-key constraint issues.
	for i := len(models) - 1; i >= 0; i-- {
		// Use Unscoped to bypass soft-delete; ignore errors for models that
		// may not have a table yet.
		_ = db.Unscoped().Where("1 = 1").Delete(models[i]).Error
	}
}

// requireNoError is a shortcut.
func requireNoError(t *testing.T, err error) {
	t.Helper()
	require.NoError(t, err)
}
