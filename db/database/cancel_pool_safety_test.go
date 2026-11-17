package database

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type cancelTestRecord struct {
	ID    uint64 `gorm:"primaryKey"`
	Label string `gorm:"type:varchar(64);not null"`
}

type captureGormLogger struct {
	mu     sync.Mutex
	errors []string
}

func (l *captureGormLogger) LogMode(logger.LogLevel) logger.Interface { return l }
func (l *captureGormLogger) Info(context.Context, string, ...any)     {}
func (l *captureGormLogger) Warn(context.Context, string, ...any)     {}
func (l *captureGormLogger) Error(_ context.Context, msg string, _ ...any) {
	l.mu.Lock()
	l.errors = append(l.errors, msg)
	l.mu.Unlock()
}
func (l *captureGormLogger) Trace(_ context.Context, _ time.Time, fc func() (string, int64), err error) {
	if err != nil {
		sql, _ := fc()
		l.mu.Lock()
		l.errors = append(l.errors, sql+": "+err.Error())
		l.mu.Unlock()
	}
}

func TestContextCancelPreservesPoolAndStatementCacheIntegrity(t *testing.T) {
	for _, prepareStmt := range []bool{false, true} {
		name := "default_pool"
		if prepareStmt {
			name = "prepared_stmt_cache"
		}
		t.Run(name, func(t *testing.T) {
			sink := &captureGormLogger{}
			dbPath := filepath.Join(t.TempDir(), "pool.db")
			conn, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
				PrepareStmt: prepareStmt,
				Logger:      NewRedactingLogger(sink),
			})
			require.NoError(t, err)
			sqlDB, err := conn.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })

			// Force every canceled and subsequent healthy query onto the exact same
			// pooled connection (and cached prepared statement when PrepareStmt=true).
			sqlDB.SetMaxOpenConns(1)
			sqlDB.SetMaxIdleConns(1)

			require.NoError(t, conn.AutoMigrate(&cancelTestRecord{}))
			require.NoError(t, conn.Create(&[]cancelTestRecord{
				{ID: 1, Label: "alpha"},
				{ID: 2, Label: "beta"},
			}).Error)

			var cancelHookMu sync.Mutex
			var pendingCancel context.CancelFunc
			require.NoError(t, conn.Callback().Query().Before("gorm:query").Register("test:mid_query_cancel", func(*gorm.DB) {
				cancelHookMu.Lock()
				fn := pendingCancel
				pendingCancel = nil
				cancelHookMu.Unlock()
				if fn != nil {
					fn()
				}
			}))

			for i := 0; i < 15; i++ {
				// Prime/use statement with a healthy query.
				var healthy cancelTestRecord
				require.NoError(t, conn.WithContext(context.Background()).Where("id = ?", 1).First(&healthy).Error)
				require.Equal(t, "alpha", healthy.Label)

				// Cancel mid-query on the same SQL shape and single pooled connection.
				ctx, cancel := context.WithCancel(context.Background())
				cancelHookMu.Lock()
				pendingCancel = cancel
				cancelHookMu.Unlock()

				var canceledRow cancelTestRecord
				qErr := conn.WithContext(ctx).Where("id = ?", 2).First(&canceledRow).Error
				require.ErrorIs(t, qErr, context.Canceled)

				// Cancel inside a transaction and verify rollback releases the single connection cleanly.
				txCtx, txCancel := context.WithCancel(context.Background())
				txErr := conn.WithContext(txCtx).Transaction(func(tx *gorm.DB) error {
					if err := tx.Create(&cancelTestRecord{ID: uint64(100 + i), Label: "temp"}).Error; err != nil {
						return err
					}
					txCancel()
					var check cancelTestRecord
					return tx.Where("id = ?", 1).First(&check).Error
				})
				require.True(t, errors.Is(txErr, context.Canceled))

				// Immediately reuse the single pooled connection and cached statement.
				var after cancelTestRecord
				require.NoError(t, conn.WithContext(context.Background()).Where("id = ?", 2).First(&after).Error)
				require.Equal(t, "beta", after.Label)

				var total int64
				require.NoError(t, conn.Model(&cancelTestRecord{}).Count(&total).Error)
				require.EqualValues(t, 2, total, "aborted transaction must not commit partial rows")
			}

			stats := sqlDB.Stats()
			require.Zero(t, stats.InUse, "no SQL connection may remain checked out after cancellations")
			require.LessOrEqual(t, stats.OpenConnections, 1)

			sink.mu.Lock()
			recordedErrors := append([]string(nil), sink.errors...)
			sink.mu.Unlock()
			require.Empty(t, recordedErrors, "expected client context cancellations must not be logged as SQL errors")
		})
	}
}
