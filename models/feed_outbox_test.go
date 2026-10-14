package models

import (
	"errors"
	"fmt"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"os"
	"sync"
	"testing"
	"time"
)

func feedTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	var conn *gorm.DB
	var err error
	if dsn := os.Getenv("FEED_TEST_MYSQL_DSN"); dsn != "" {
		conn, err = gorm.Open(mysql.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
		if err == nil {
			require.NoError(t, conn.Migrator().DropTable(&FeedBatch{}, &FeedEvent{}))
		}
	} else {
		conn, err = pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	}
	require.NoError(t, err)
	sql, err := conn.DB()
	require.NoError(t, err)
	sql.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sql.Close() })
	require.NoError(t, conn.AutoMigrate(&FeedEvent{}, &FeedBatch{}))
	return conn
}
func TestFeedLeaseBatchesAndParentCompletion(t *testing.T) {
	conn := feedTestDB(t)
	event := FeedEvent{EventKey: "first", Kind: FeedStatusCreate, ObjectType: "status", ObjectID: 1, ActorID: 1, ObjectVersion: 1}
	require.NoError(t, conn.Create(&event).Error)
	claims, err := ClaimFeedEvents(conn, 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, claims, 1)
	claim := claims[0].LeaseRef()
	others, err := ClaimFeedEvents(conn, 1, 30*time.Second)
	require.NoError(t, err)
	require.Empty(t, others)
	require.ErrorIs(t, CompleteFeedEvent(conn, claim), ErrFeedNotComplete)
	batches := []FeedBatch{{BatchNo: 1, TargetKind: "home", Recipients: []FeedRecipient{{AccountID: 2}}}}
	require.NoError(t, PersistFeedBatches(conn, claim, batches, map[string]uint64{"followers": 10}, map[string]uint64{"followers": 10}, true))
	require.ErrorIs(t, CompleteFeedEvent(conn, claim), ErrFeedNotComplete)
	children, err := ClaimFeedBatches(conn, 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, children, 1)
	wrong := children[0].LeaseRef()
	wrong.Generation++
	require.ErrorIs(t, CompleteFeedBatch(conn, wrong), ErrFeedLeaseLost)
	require.NoError(t, CompleteFeedBatch(conn, children[0].LeaseRef()))
	require.NoError(t, CompleteFeedEvent(conn, claim))
	require.ErrorIs(t, CompleteFeedEvent(conn, claim), ErrFeedLeaseLost)
}
func TestFeedLeaseTakeoverAndAtomicCursorRollback(t *testing.T) {
	conn := feedTestDB(t)
	event := FeedEvent{EventKey: "takeover", Kind: FeedStatusCreate}
	require.NoError(t, conn.Create(&event).Error)
	first, err := ClaimFeedEvents(conn, 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.NoError(t, conn.Model(&FeedEvent{}).Where("id = ?", event.ID).UpdateColumn("lease_expires_at", time.Now().UTC().Add(-time.Minute)).Error)
	require.ErrorIs(t, RenewFeedEvent(conn, first[0].LeaseRef(), 30*time.Second), ErrFeedLeaseLost)
	second, err := ClaimFeedEvents(conn, 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Greater(t, second[0].LeaseGeneration, first[0].LeaseGeneration)
	require.ErrorIs(t, PersistFeedBatches(conn, first[0].LeaseRef(), nil, nil, nil, true), ErrFeedLeaseLost)
	duplicate := []FeedBatch{{BatchNo: 1, TargetKind: "home", Recipients: []FeedRecipient{{AccountID: 2}}}, {BatchNo: 1, TargetKind: "home", Recipients: []FeedRecipient{{AccountID: 3}}}}
	require.Error(t, PersistFeedBatches(conn, second[0].LeaseRef(), duplicate, map[string]uint64{"followers": 99}, map[string]uint64{"followers": 100}, false))
	var count int64
	require.NoError(t, conn.Model(&FeedBatch{}).Count(&count).Error)
	require.Zero(t, count)
	var current FeedEvent
	require.NoError(t, conn.First(&current, event.ID).Error)
	require.Empty(t, current.ScanCursor)
	require.NoError(t, YieldFeedEvent(conn, second[0].LeaseRef(), 0))
	require.NoError(t, conn.First(&current, event.ID).Error)
	require.Zero(t, current.Attempts)
	third, err := ClaimFeedEvents(conn, 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, third, 1)
	require.NoError(t, RetryFeedEvent(conn, third[0].LeaseRef(), "redis_unavailable", time.Hour, false))
	require.NoError(t, conn.First(&current, event.ID).Error)
	require.EqualValues(t, 1, current.Attempts)
	none, err := ClaimFeedEvents(conn, 1, 30*time.Second)
	require.NoError(t, err)
	require.Empty(t, none)
}
func TestFeedStatusTransactionRevisionAndScheduling(t *testing.T) {
	conn := feedTestDB(t)
	require.NoError(t, conn.AutoMigrate(&Status{}, &Conversation{}, &AccountConversation{}, &StatusToAccount{}))
	status := &Status{Id: 1}
	require.NoError(t, conn.Omit("Content").Create(status).Error)
	var events []FeedEvent
	require.NoError(t, conn.Order("id").Find(&events).Error)
	require.Len(t, events, 1)
	require.Equal(t, FeedStatusCreate, events[0].Kind)
	require.EqualValues(t, 1, events[0].ObjectVersion)
	status.FeedRevision = 0 // stale client objects must not reset the durable counter.
	status.Language = "zh"
	require.NoError(t, conn.Omit("Content").Save(status).Error)
	require.NoError(t, conn.Order("id").Find(&events).Error)
	require.Len(t, events, 2)
	require.EqualValues(t, 2, events[1].ObjectVersion)
	require.Error(t, conn.Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Content").Create(&Status{Id: 2}).Error; err != nil {
			return err
		}
		return errors.New("rollback")
	}))
	var count int64
	require.NoError(t, conn.Model(&FeedEvent{}).Where("object_id = ?", 2).Count(&count).Error)
	require.Zero(t, count)
	future := time.Now().Add(time.Hour)
	scheduled := &Status{Id: 3, ScheduledAt: &future}
	require.NoError(t, conn.Omit("Content").Create(scheduled).Error)
	require.NoError(t, conn.Model(&FeedEvent{}).Where("object_id = ?", 3).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, conn.Model(&Status{}).Where("id = ?", 3).Update("scheduled_at", nil).Error)
	require.NoError(t, conn.Model(&FeedEvent{}).Where("object_id = ?", 3).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, conn.Delete(status).Error)
	require.NoError(t, conn.Order("id").Find(&events).Error)
	require.Equal(t, FeedStatusDelete, events[len(events)-1].Kind)
	require.EqualValues(t, 3, events[len(events)-1].ObjectVersion)
}

func TestFeedConcurrentClaims(t *testing.T) {
	if os.Getenv("FEED_TEST_MYSQL_DSN") == "" && !pgtest.Enabled() {
		t.Skip("requires transactional SQL server")
	}
	conn := feedTestDB(t)
	pool, err := conn.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(8)
	for i := 0; i < 40; i++ {
		require.NoError(t, conn.Create(&FeedEvent{EventKey: fmt.Sprintf("concurrent:%d", i), Kind: FeedStatusCreate}).Error)
	}
	var wg sync.WaitGroup
	ids := make(chan uint64, 40)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := ClaimFeedEvents(conn, 5, 30*time.Second)
			errs <- err
			for _, row := range rows {
				ids <- row.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	seen := map[uint64]bool{}
	for id := range ids {
		require.False(t, seen[id])
		seen[id] = true
	}
	require.Len(t, seen, 40)
}

func TestFeedRequiredOutboxFailureRollsBackMutation(t *testing.T) {
	conn := feedTestDB(t)
	require.NoError(t, conn.Exec("CREATE TABLE feed_business_fixture (id BIGINT PRIMARY KEY)").Error)
	t.Cleanup(func() { conn.Exec("DROP TABLE feed_business_fixture") })
	require.NoError(t, conn.Migrator().DropTable(&FeedEvent{}))
	previous := feedOutboxRequired.Load()
	RequireFeedOutbox()
	defer feedOutboxRequired.Store(previous)
	err := conn.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("INSERT INTO feed_business_fixture (id) VALUES (1)").Error; err != nil {
			return err
		}
		return QueueFeedInvalidation(tx, FeedEventMetadata{ActorID: 1, Reason: "follow_changed"})
	})
	require.Error(t, err)
	var n int64
	require.NoError(t, conn.Table("feed_business_fixture").Count(&n).Error)
	require.Zero(t, n)
}

func TestFeedRefreshRetainsOldAudienceAfterAPReplacesJoins(t *testing.T) {
	conn := feedTestDB(t)
	require.NoError(t, conn.AutoMigrate(&Status{}, &Conversation{}, &AccountConversation{}, &StatusToAccount{}, &Tag{}))
	status := Status{AccountId: 1, Visibility: StatusVisibilityDirect}
	require.NoError(t, conn.Omit("Account", "Content").Create(&status).Error)
	require.NoError(t, conn.Exec("INSERT INTO status_to_accounts (status_id,account_id,created_at) VALUES (?,?,CURRENT_TIMESTAMP)", status.Id, 2).Error)
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error { return QueueFeedStatus(tx, FeedStatusRefresh, &status) }))
	// AP verifies new recipients then replaces joins before saving the status.
	require.NoError(t, conn.Exec("DELETE FROM status_to_accounts WHERE status_id = ?", status.Id).Error)
	require.NoError(t, conn.Exec("INSERT INTO status_to_accounts (status_id,account_id,created_at) VALUES (?,?,CURRENT_TIMESTAMP)", status.Id, 3).Error)
	require.NoError(t, conn.Omit("Account", "Content").Save(&status).Error)
	var event FeedEvent
	require.NoError(t, conn.Where("event_key = ?", fmt.Sprintf("status:%d:%d", status.Id, status.FeedRevision)).First(&event).Error)
	require.Contains(t, event.Metadata.PreviousRecipientIDs, uint64(2))
	require.Equal(t, []uint64{3}, event.Metadata.RecipientIDs)
}

func TestFeedPriorAudienceContextSurvivesEventGarbageCollection(t *testing.T) {
	conn := feedTestDB(t)
	require.NoError(t, conn.AutoMigrate(&Status{}, &Conversation{}, &AccountConversation{}, &StatusToAccount{}, &Tag{}))
	status := Status{AccountId: 1, Visibility: StatusVisibilityDirect}
	require.NoError(t, conn.Omit("Account", "Content").Create(&status).Error)
	require.NoError(t, conn.Exec("INSERT INTO status_to_accounts (status_id,account_id,created_at) VALUES (?,?,CURRENT_TIMESTAMP)", status.Id, 2).Error)
	require.NoError(t, conn.Where("object_id = ?", status.Id).Delete(&FeedEvent{}).Error)
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error {
		tx, err := PreserveFeedStatusAudience(tx, status.Id)
		if err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM status_to_accounts WHERE status_id = ?", status.Id).Error; err != nil {
			return err
		}
		return tx.Model(&Status{}).Where("id = ?", status.Id).Update("sensitive", true).Error
	}))
	var event FeedEvent
	require.NoError(t, conn.Where("object_id = ?", status.Id).First(&event).Error)
	require.Contains(t, event.Metadata.PreviousRecipientIDs, uint64(2))
	require.Empty(t, event.Metadata.RecipientIDs)
}

func TestFeedRelationshipScopeCapturedBeforeUpdate(t *testing.T) {
	conn := feedTestDB(t)
	require.NoError(t, conn.Exec("CREATE TABLE follows (id BIGINT PRIMARY KEY,account_id BIGINT,follow_id BIGINT,status INT)").Error)
	require.NoError(t, conn.Exec("INSERT INTO follows VALUES (1,2,3,?)", FollowStatusRequested).Error)
	require.NoError(t, conn.Model(&Follow{}).Where("status = ?", FollowStatusRequested).Update("status", FollowStatusFollowed).Error)
	var rows []FeedEvent
	require.NoError(t, conn.Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, uint64(2), rows[0].Metadata.ActorID)
	require.Equal(t, uint64(3), rows[0].Metadata.TargetAccountID)
}
