package notifications

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Set COMMUNITY_NOTIFICATION_TEST_MYSQL_DSN to a disposable empty database.
// Real InnoDB locks are required: the in-memory test engine cannot reproduce
// a worker waiting for a different connection's uncommitted status row.
func TestMentionNotificationTransaction(t *testing.T) {
	dsn := os.Getenv("COMMUNITY_NOTIFICATION_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires disposable MySQL database")
	}
	conn, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, ddl := range []string{
		"CREATE TABLE statuses (id BIGINT UNSIGNED PRIMARY KEY, account_id BIGINT UNSIGNED NOT NULL, updated_at DATETIME(3)) ENGINE=InnoDB",
		"CREATE TABLE accounts (id BIGINT UNSIGNED PRIMARY KEY, username VARCHAR(20), domain VARCHAR(128), deleted_at DATETIME) ENGINE=InnoDB",
		"CREATE TABLE status_contents (status_id BIGINT UNSIGNED PRIMARY KEY, content TEXT, spoiler_text TEXT) ENGINE=InnoDB",
		"CREATE TABLE status_to_accounts (status_id BIGINT UNSIGNED, account_id BIGINT UNSIGNED) ENGINE=InnoDB",
		"CREATE TABLE notifications (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, type TINYINT NOT NULL, activity_type VARCHAR(30), account_id BIGINT UNSIGNED NOT NULL, from_account_id BIGINT UNSIGNED, status_id BIGINT UNSIGNED, report_id BIGINT UNSIGNED, list_id BIGINT UNSIGNED, is_read BOOLEAN, created_at DATETIME(3), updated_at DATETIME(3)) ENGINE=InnoDB",
	} {
		require.NoError(t, conn.Exec(ddl).Error)
	}
	require.NoError(t, conn.AutoMigrate(&models.Block{}, &models.AccountExtra{}))
	t.Cleanup(func() {
		for _, table := range []string{"blocks", "accounts_extra", "notifications", "status_to_accounts", "status_contents", "statuses", "accounts"} {
			_ = conn.Exec("DROP TABLE " + table).Error
		}
	})
	require.NoError(t, conn.Exec("INSERT INTO accounts (id, username) VALUES (1, 'author'), (2, 'old'), (3, 'new')").Error)

	for i, tc := range []struct {
		name                            string
		update, rollback, sameTimestamp bool
	}{
		{"create commit", false, false, false}, {"create rollback", false, true, false},
		{"edit commit", true, false, false}, {"edit rollback", true, true, false},
		{"same millisecond edit commit", true, false, true}, {"same millisecond edit rollback", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := uint64(i + 1)
			var oldNotificationID uint64
			if tc.update {
				require.NoError(t, conn.Exec("INSERT INTO statuses VALUES (?, 1, '2020-01-01')", id).Error)
				require.NoError(t, conn.Exec("INSERT INTO status_to_accounts VALUES (?, 2)", id).Error)
				require.NoError(t, conn.Exec("INSERT INTO status_contents VALUES (?, 'old', '')", id).Error)
				old := models.Notification{StatusId: id, AccountId: 2, FromAccountId: 1, Type: models.NotificationTypeMention}
				require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&old).Error)
				oldNotificationID = old.Id
			}
			tx := conn.Begin()
			require.NoError(t, tx.Error)
			defer tx.Rollback()
			if tc.update {
				require.NoError(t, tx.Exec("UPDATE statuses SET updated_at = '2021-01-01' WHERE id = ?", id).Error)
				require.NoError(t, tx.Exec("DELETE FROM status_to_accounts WHERE status_id = ?", id).Error)
			} else {
				require.NoError(t, tx.Exec("INSERT INTO statuses VALUES (?, 1, '2020-01-01')", id).Error)
			}
			require.NoError(t, tx.Exec("INSERT INTO status_to_accounts VALUES (?, 3)", id).Error)
			if tc.update {
				require.NoError(t, tx.Exec("UPDATE status_contents SET content = 'new' WHERE status_id = ?", id).Error)
			}
			require.NoError(t, tx.Exec("UPDATE statuses SET updated_at = '2021-01-01' WHERE id = ?", id).Error)

			version := mentionStatusVersion{updatedAt: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)}
			if tc.sameTimestamp {
				require.NoError(t, tx.Exec("UPDATE statuses SET updated_at = '2020-01-01' WHERE id = ?", id).Error)
				version.updatedAt = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
				version.content = models.StatusContent{StatusId: id, Content: "new"}
			}

			// Observe that the worker reaches its locking read while the save is
			// unresolved, instead of depending on sleeps to schedule the worker.
			type workerKey struct{}
			started := make(chan struct{})
			var once sync.Once
			callback := "test:mention_lock"
			require.NoError(t, conn.Callback().Query().Before("gorm:query").Register(callback, func(q *gorm.DB) {
				if q.Statement.Context.Value(workerKey{}) == true {
					once.Do(func() { close(started) })
				}
			}))
			defer conn.Callback().Query().Remove(callback)
			var publishedMu sync.Mutex
			var published []uint64
			handler := event.Register("notification", "after_create", &event.Handler{Exec: func(e event.Event) bool {
				n, ok := e.Data.(*models.Notification)
				if !ok || n.StatusId != id {
					return true
				}
				var count int64
				assert.NoError(t, conn.Model(&models.Notification{}).Where("id = ?", n.Id).Count(&count).Error)
				assert.Equal(t, int64(1), count, "published notification must already be committed")
				publishedMu.Lock()
				published = append(published, n.Id)
				publishedMu.Unlock()
				return true
			}})
			defer event.Remove("notification", "after_create", handler)
			done := make(chan error, 1)
			go func() {
				done <- processMentionNotification(conn.WithContext(context.WithValue(context.Background(), workerKey{}, true)), id, tc.update, version)
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("worker did not start")
			}
			select {
			case err := <-done:
				t.Fatalf("worker finished before save resolved: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			if tc.rollback {
				require.NoError(t, tx.Rollback().Error)
			} else {
				require.NoError(t, tx.Commit().Error)
			}
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(3 * time.Second):
				t.Fatal("worker remained blocked")
			}
			var mentions []models.Notification
			require.NoError(t, conn.Where("status_id = ? AND type = ?", id, models.NotificationTypeMention).Find(&mentions).Error)
			switch {
			case tc.rollback && !tc.update:
				require.Empty(t, mentions)
			case tc.rollback && tc.update:
				require.Len(t, mentions, 1)
				assert.Equal(t, uint64(2), mentions[0].AccountId)
				assert.Equal(t, oldNotificationID, mentions[0].Id)
			default:
				require.Len(t, mentions, 1)
				assert.Equal(t, uint64(3), mentions[0].AccountId)
			}
			if tc.rollback {
				var total int64
				require.NoError(t, conn.Model(&models.Notification{}).Where("status_id = ?", id).Count(&total).Error)
				expected := int64(0)
				if tc.update {
					expected = 1
				}
				assert.Equal(t, expected, total)
				publishedMu.Lock()
				assert.Empty(t, published)
				publishedMu.Unlock()
			}
		})
	}
}
