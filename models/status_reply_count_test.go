package models

import (
	"errors"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"os"
	"testing"
)

func TestStatusReplyCountLifecycle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	require.NoError(t, db.AutoMigrate(&Status{}, &Conversation{}, &AccountConversation{}, &StatusToAccount{}))
	parent := &Status{Id: 100}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Omit("Content").Create(parent).Error)
	count := func(want uint64) {
		t.Helper()
		var got Status
		require.NoError(t, db.First(&got, parent.Id).Error)
		require.Equal(t, want, got.RepliesCount)
	}
	reply := &Status{Id: 101, InReplyToId: &parent.Id}
	require.NoError(t, db.Omit("Content").Create(reply).Error)
	count(1)
	reply.Language = "zh"
	require.NoError(t, db.Omit("Content").Save(reply).Error)
	count(1) // editing must not count a reply twice
	reblog := &Status{Id: 102, ReblogId: &reply.Id, InReplyToId: &parent.Id}
	require.NoError(t, db.Omit("Content").Create(reblog).Error)
	count(1)
	require.NoError(t, db.Delete(reblog).Error)
	count(1)
	require.NoError(t, db.Delete(reply).Error)
	count(0)
	require.NoError(t, db.Delete(reply).Error)
	count(0) // repeated deletion and legacy counters cannot underflow
}

// Real row locks cannot be exercised by SQLite. Use a disposable empty MySQL
// database to verify two requests deleting the same reply do not double-count.
func TestConcurrentReplyDeletionMySQL(t *testing.T) {
	dsn := os.Getenv("COMMUNITY_REPLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires disposable MySQL database")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, db.Exec("CREATE TABLE statuses (id BIGINT UNSIGNED PRIMARY KEY, in_reply_to_id BIGINT UNSIGNED NULL, reblog_id BIGINT UNSIGNED NULL, replies_count BIGINT UNSIGNED NOT NULL DEFAULT 0, account_id BIGINT UNSIGNED DEFAULT 0) ENGINE=InnoDB").Error)
	t.Cleanup(func() { _ = db.Exec("DROP TABLE statuses").Error })
	for i := 0; i < 10; i++ {
		require.NoError(t, db.Exec("DELETE FROM statuses").Error)
		require.NoError(t, db.Exec("INSERT INTO statuses(id,in_reply_to_id,replies_count) VALUES(1,NULL,2),(2,1,0),(3,1,0)").Error)
		start := make(chan struct{})
		results := make(chan error, 2)
		for j := 0; j < 2; j++ {
			go func() { <-start; results <- db.Delete(&Status{Id: 2}).Error }()
		}
		close(start)
		require.NoError(t, <-results)
		require.NoError(t, <-results)
		var parent Status
		require.NoError(t, db.First(&parent, 1).Error)
		require.Equal(t, uint64(1), parent.RepliesCount)
		rollback := errors.New("rollback deletion")
		err = db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Delete(&Status{Id: 3}).Error; err != nil {
				return err
			}
			return rollback
		})
		require.ErrorIs(t, err, rollback)
		require.NoError(t, db.First(&parent, 1).Error)
		require.Equal(t, uint64(1), parent.RepliesCount)
	}
}
