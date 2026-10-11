package worker

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/driver/mysql"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	redisdb "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func workerFixture(t *testing.T) (*Worker, *redis.Client) {
	t.Helper()
	var conn *gorm.DB
	var err error
	if dsn := os.Getenv("FEED_WORKER_TEST_MYSQL_DSN"); dsn != "" {
		conn, err = gorm.Open(mysql.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
		require.NoError(t, err)
		for _, table := range []string{"feed_batches", "feed_events", "accounts", "follows", "lists", "list_accounts", "statuses", "status_to_accounts", "tags", "status_tags", "media_attachments"} {
			require.NoError(t, conn.Migrator().DropTable(table))
		}
	} else {
		conn, err = pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	}
	require.NoError(t, err)
	pool, err := conn.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { pool.Close() })
	require.NoError(t, conn.AutoMigrate(&models.FeedEvent{}, &models.FeedBatch{}))
	for _, sql := range []string{
		"CREATE TABLE accounts (id BIGINT PRIMARY KEY, user_id BIGINT, domain TEXT, deleted_at TIMESTAMP, created_at TIMESTAMP, last_activity_at TIMESTAMP, feed_last_seen_at TIMESTAMP)",
		"CREATE TABLE follows (id BIGINT PRIMARY KEY,account_id BIGINT,follow_id BIGINT,status INT)",
		"CREATE TABLE lists (id BIGINT PRIMARY KEY,account_id BIGINT,deleted_at TIMESTAMP)",
		"CREATE TABLE list_accounts (id BIGINT PRIMARY KEY,list_id BIGINT,account_id BIGINT)",
		"CREATE TABLE statuses (id BIGINT PRIMARY KEY,account_id BIGINT,visibility INT,feed_revision BIGINT,deleted_at TIMESTAMP,scheduled_at TIMESTAMP,domain TEXT,origin_uri TEXT,reblog_id BIGINT,conversation_id BIGINT,properties TEXT)",
		"CREATE TABLE status_to_accounts (status_id BIGINT,account_id BIGINT)",
		"CREATE TABLE tags (id BIGINT PRIMARY KEY,name TEXT)",
		"CREATE TABLE status_tags (status_id BIGINT,tag_id BIGINT)",
		"CREATE TABLE media_attachments (id BIGINT PRIMARY KEY,status_id BIGINT)",
	} {
		require.NoError(t, conn.Exec(sql).Error)
	}
	now := time.Now()
	for i := 1; i <= 4; i++ {
		require.NoError(t, conn.Exec("INSERT INTO accounts (id,feed_last_seen_at) VALUES (?,?)", i, now).Error)
	}
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { client.Close() })
	redisdb.SetUniversalRedis(client)
	feed.SetDefaultService(feed.NewFeedService(feed.NewRedisClient(client)))
	t.Cleanup(func() { redisdb.SetUniversalRedis(nil); feed.SetDefaultService(nil) })
	return New(conn, Options{BatchSize: 128, Shadow: true}), client
}
func queueEvent(t *testing.T, w *Worker, key string, kind string, visibility uint8) models.FeedEvent {
	t.Helper()
	e := models.FeedEvent{EventKey: key, Kind: kind, ObjectType: "status", ObjectID: 10, ActorID: 1, ObjectVersion: 1, Metadata: models.FeedEventMetadata{ActorID: 1, Visibility: visibility, RecipientIDs: []uint64{2}}}
	require.NoError(t, w.DB.Create(&e).Error)
	return e
}
func TestNotificationTargetsRecipientNotActor(t *testing.T) {
	w, _ := workerFixture(t)
	event := models.FeedEvent{Kind: models.FeedNotification, Metadata: models.FeedEventMetadata{ActorID: 1, RecipientIDs: []uint64{2}}}
	groups, err := w.fixedTargets(context.Background(), &event)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, []models.FeedRecipient{{AccountID: 2}}, groups[0].recipients)
}
func TestDirectScanNeverFansOutFollowersOrPublic(t *testing.T) {
	w, _ := workerFixture(t)
	require.NoError(t, w.DB.Exec("INSERT INTO follows VALUES (1,3,1,?)", models.FollowStatusFollowed).Error)
	queueEvent(t, w, "direct", models.FeedStatusCreate, models.StatusVisibilityDirect)
	_, err := w.ScanOnce(context.Background())
	require.NoError(t, err)
	var batches []models.FeedBatch
	require.NoError(t, w.DB.Find(&batches).Error)
	require.NotEmpty(t, batches)
	for _, batch := range batches {
		require.NotEqual(t, "public", batch.TargetKind)
		require.NotEqual(t, "list", batch.TargetKind)
		for _, r := range batch.Recipients {
			require.Contains(t, []uint64{1, 2}, r.AccountID)
		}
	}
}
func TestFollowerScanBoundedCursorAndReadOnlyActiveAudience(t *testing.T) {
	w, _ := workerFixture(t)
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour)
	for i := 5; i < 650; i++ {
		last := now
		if i == 649 {
			last = old
		}
		require.NoError(t, w.DB.Exec("INSERT INTO accounts (id,created_at,last_activity_at,feed_last_seen_at) VALUES (?,?,?,?)", i, old, old, last).Error)
		require.NoError(t, w.DB.Exec("INSERT INTO follows VALUES (?,?,1,?)", i, i, models.FollowStatusFollowed).Error)
	}
	e := queueEvent(t, w, "fanout", models.FeedStatusCreate, models.StatusVisibilityPublic)
	var upper uint64
	for i := 0; i < 12; i++ {
		_, err := w.ScanOnce(context.Background())
		require.NoError(t, err)
		require.NoError(t, w.DB.First(&e, e.ID).Error)
		if i == 0 {
			upper = e.ScanUpperBound["followers"]
			require.NoError(t, w.DB.Exec("INSERT INTO follows VALUES (10000,4,1,?)", models.FollowStatusFollowed).Error)
		}
		require.Equal(t, upper, e.ScanUpperBound["followers"])
		if e.ScanComplete {
			break
		}
		require.NoError(t, w.DB.Model(&models.FeedEvent{}).Where("id = ?", e.ID).UpdateColumn("next_attempt_at", nil).Error)
	}
	require.True(t, e.ScanComplete)
	var batches []models.FeedBatch
	require.NoError(t, w.DB.Where("target_kind = ?", "home").Find(&batches).Error)
	seen := map[uint64]int{}
	for _, batch := range batches {
		require.LessOrEqual(t, len(batch.Recipients), 128)
		for _, r := range batch.Recipients {
			seen[r.AccountID]++
		}
	}
	for i := uint64(5); i < 649; i++ {
		require.Equal(t, 1, seen[i], fmt.Sprint(i))
	}
	require.Zero(t, seen[649])
	require.Zero(t, seen[4])
}
func TestDeleteBeforeCreateAndSupersededBatchComplete(t *testing.T) {
	w, client := workerFixture(t)
	ctx := context.Background()
	// SQL already committed deletion; replayed create must also project removal.
	old := queueEvent(t, w, "old", models.FeedStatusCreate, models.StatusVisibilityDirect)
	newer := queueEvent(t, w, "new", models.FeedStatusDelete, models.StatusVisibilityDirect)
	newer.ObjectVersion = 2
	require.NoError(t, w.DB.Save(&newer).Error)
	for _, event := range []models.FeedEvent{newer, old} {
		b := models.FeedBatch{EventID: event.ID, TargetKind: "home", Recipients: []models.FeedRecipient{{AccountID: 2}}}
		require.NoError(t, w.DB.Create(&b).Error)
		_, err := w.DeliverOnce(ctx)
		require.NoError(t, err)
		require.NoError(t, w.DB.First(&b, b.ID).Error)
		require.Equal(t, "complete", b.State)
	}
	require.Zero(t, client.ZCard(ctx, feed.TimelineKey(&home.HomeTimeline{Account: &models.Account{Id: 2}})).Val())
}

type lostPipelineReply struct{ calls atomic.Int32 }

func (h *lostPipelineReply) DialHook(next redis.DialHook) redis.DialHook          { return next }
func (h *lostPipelineReply) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }
func (h *lostPipelineReply) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		err := next(ctx, cmds)
		for _, cmd := range cmds {
			args := cmd.Args()
			if len(args) > 1 && strings.Contains(fmt.Sprint(args[1]), "local requested=tonumber") && h.calls.CompareAndSwap(0, 1) {
				return errors.New("test lost redis reply after application")
			}
		}
		return err
	}
}
func TestProjectionAppliedButReplyLostRetriesWholeBatch(t *testing.T) {
	w, client := workerFixture(t)
	ctx := context.Background()
	require.NoError(t, w.DB.Exec("INSERT INTO statuses (id,account_id,visibility,feed_revision) VALUES (10,1,?,1)", models.StatusVisibilityDirect).Error)
	require.NoError(t, w.DB.Exec("INSERT INTO status_to_accounts VALUES (10,2)").Error)
	e := queueEvent(t, w, "reply-lost", models.FeedStatusCreate, models.StatusVisibilityDirect)
	b := models.FeedBatch{EventID: e.ID, TargetKind: "home", Recipients: []models.FeedRecipient{{AccountID: 1}, {AccountID: 2}}}
	require.NoError(t, w.DB.Create(&b).Error)
	client.AddHook(&lostPipelineReply{})
	_, err := w.DeliverOnce(ctx)
	require.Error(t, err)
	require.NoError(t, w.DB.First(&b, b.ID).Error)
	require.Equal(t, "pending", b.State)
	require.EqualValues(t, 1, b.Attempts)
	require.Equal(t, []string{"10"}, client.ZRange(ctx, feed.TimelineKey(&home.HomeTimeline{Account: &models.Account{Id: 2}}), 0, -1).Val(), "projection committed before retry")
	require.NoError(t, w.DB.Model(&models.FeedBatch{}).Where("id = ?", b.ID).UpdateColumn("next_attempt_at", nil).Error)
	_, err = w.DeliverOnce(ctx)
	require.NoError(t, err)
	require.NoError(t, w.DB.First(&b, b.ID).Error)
	require.Equal(t, "complete", b.State)
	for _, id := range []uint64{1, 2} {
		key := feed.TimelineKey(&home.HomeTimeline{Account: &models.Account{Id: id}})
		require.Equal(t, []string{"10"}, client.ZRange(ctx, key, 0, -1).Val())
	}
}
func TestReplayWindowExpiryDoesNotResurrect(t *testing.T) {
	w, client := workerFixture(t)
	e := queueEvent(t, w, "expired", models.FeedStatusCreate, models.StatusVisibilityPublic)
	require.NoError(t, w.DB.Model(&models.FeedEvent{}).Where("id = ?", e.ID).UpdateColumn("created_at", time.Now().Add(-8*24*time.Hour)).Error)
	b := models.FeedBatch{EventID: e.ID, TargetKind: "home", Recipients: []models.FeedRecipient{{AccountID: 2}}}
	require.NoError(t, w.DB.Create(&b).Error)
	_, err := w.DeliverOnce(context.Background())
	require.NoError(t, err)
	require.NoError(t, w.DB.First(&b, b.ID).Error)
	require.Equal(t, "failed", b.State)
	require.Equal(t, "replay_window_expired", b.LastErrorCode)
	require.Empty(t, client.Keys(context.Background(), "*").Val())
}

func TestAccountReconciliationInvalidatesAllPersonalIndexes(t *testing.T) {
	w, _ := workerFixture(t)
	targets, err := w.targets(context.Background(), &models.FeedBatch{TargetKind: "account_invalidate", Recipients: []models.FeedRecipient{{AccountID: 2}}})
	require.NoError(t, err)
	require.Len(t, targets, 4)
	keys := map[string]bool{}
	for _, target := range targets {
		keys[feed.TimelineKey(target.timeline)] = true
	}
	require.Len(t, keys, 4)
}

func TestInvalidationReplyLossDoesNotRepeatedlyResetNewSnapshot(t *testing.T) {
	w, client := workerFixture(t)
	ctx := context.Background()
	e := models.FeedEvent{EventKey: "invalidate-retry", Kind: models.FeedInvalidate, Metadata: models.FeedEventMetadata{ActorID: 2, RecipientIDs: []uint64{2}, Reason: "mute_changed"}}
	require.NoError(t, w.DB.Create(&e).Error)
	b := models.FeedBatch{EventID: e.ID, TargetKind: "account_invalidate", Recipients: []models.FeedRecipient{{AccountID: 2}}}
	require.NoError(t, w.DB.Create(&b).Error)
	client.AddHook(&lostPipelineReply{})
	_, err := w.DeliverOnce(ctx)
	require.Error(t, err)
	timeline := &home.HomeTimeline{Account: &models.Account{Id: 2}}
	meta := feed.TimelineKey(timeline) + ":meta"
	generation, err := client.HGet(ctx, meta, "generation").Result()
	require.NoError(t, err)
	require.Equal(t, "1", generation, "the invalidation committed before its reply was lost")
	// A reader has since rebuilt a valid current snapshot in this epoch.
	require.NoError(t, client.HSet(ctx, meta, "ready", 1).Err())
	require.NoError(t, w.DB.Model(&models.FeedBatch{}).Where("id = ?", b.ID).UpdateColumn("next_attempt_at", nil).Error)
	_, err = w.DeliverOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, generation, client.HGet(ctx, meta, "generation").Val())
	require.Equal(t, "1", client.HGet(ctx, meta, "ready").Val())
	require.NoError(t, w.DB.First(&b, b.ID).Error)
	require.Equal(t, "complete", b.State)
}

func TestFailedChildTerminatesParentInsteadOfPollingForever(t *testing.T) {
	w, _ := workerFixture(t)
	e := queueEvent(t, w, "failed-child", models.FeedStatusCreate, models.StatusVisibilityDirect)
	require.NoError(t, w.DB.Model(&models.FeedEvent{}).Where("id = ?", e.ID).UpdateColumn("scan_complete", true).Error)
	b := models.FeedBatch{EventID: e.ID, TargetKind: "home", Recipients: []models.FeedRecipient{}, FeedLease: models.FeedLease{State: "failed"}}
	require.NoError(t, w.DB.Create(&b).Error)
	_, err := w.ScanOnce(context.Background())
	require.NoError(t, err)
	require.NoError(t, w.DB.First(&e, e.ID).Error)
	require.Equal(t, "failed", e.State)
	require.Equal(t, "dependent_failed", e.LastErrorCode)
}
func TestTerminalGCBoundedAndPreservesLiveLeases(t *testing.T) {
	w, _ := workerFixture(t)
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour)
	for _, state := range []string{"complete", "cancelled", "failed"} {
		e := models.FeedEvent{EventKey: "gc-" + state, Kind: models.FeedStatusCreate, FeedLease: models.FeedLease{State: state, CreatedAt: old, UpdatedAt: old}}
		require.NoError(t, w.DB.Create(&e).Error)
	}
	protected := models.FeedEvent{EventKey: "gc-live", Kind: models.FeedStatusCreate, FeedLease: models.FeedLease{State: "failed", CreatedAt: old, UpdatedAt: old}}
	require.NoError(t, w.DB.Create(&protected).Error)
	until := now.Add(time.Minute)
	require.NoError(t, w.DB.Create(&models.FeedBatch{EventID: protected.ID, TargetKind: "home", Recipients: []models.FeedRecipient{}, FeedLease: models.FeedLease{State: "processing", LeaseExpiresAt: &until}}).Error)
	recent := models.FeedEvent{EventKey: "gc-recent", Kind: models.FeedStatusCreate, FeedLease: models.FeedLease{State: "failed", CreatedAt: old}}
	require.NoError(t, w.DB.Create(&recent).Error)
	w.maintain(context.Background())
	var events []models.FeedEvent
	require.NoError(t, w.DB.Find(&events).Error)
	keys := []string{}
	for _, e := range events {
		keys = append(keys, e.EventKey)
	}
	require.ElementsMatch(t, []string{"gc-live", "gc-recent"}, keys)
}
func TestFailureClassificationPreservesUnknownFailures(t *testing.T) {
	code, terminal := classifyFailure(context.DeadlineExceeded)
	require.Equal(t, "dependency_timeout", code)
	require.False(t, terminal)
	code, terminal = classifyFailure(fmt.Errorf("wrapped: %w", ErrInvalidTask))
	require.Equal(t, "invalid_task", code)
	require.True(t, terminal)
	code, terminal = classifyFailure(errors.New("unrecognized database failure"))
	require.Equal(t, "operation_failed", code)
	require.False(t, terminal)
}

func TestSQLReserveHysteresisAndDependencyProbe(t *testing.T) {
	w, _ := workerFixture(t)
	pool, err := w.DB.DB()
	require.NoError(t, err)
	held, err := pool.Conn(context.Background())
	require.NoError(t, err)
	require.True(t, w.pauseClaims())
	require.True(t, w.sqlPaused.Load())
	require.NoError(t, held.Close())
	require.False(t, w.pauseClaims())
	require.False(t, w.sqlPaused.Load())
	w.dependencyUntil.Store(time.Now().Add(time.Minute).UnixNano())
	require.True(t, w.pauseClaims())
	w.dependencyUntil.Store(time.Now().Add(-time.Second).UnixNano())
	require.False(t, w.pauseClaims())
}

func TestSQLAckFailureUsesBoundedRetryAndRetainsAppliedProjection(t *testing.T) {
	w, client := workerFixture(t)
	ctx := context.Background()
	require.NoError(t, w.DB.Exec("INSERT INTO statuses (id,account_id,visibility,feed_revision) VALUES (10,1,?,1)", models.StatusVisibilityDirect).Error)
	require.NoError(t, w.DB.Exec("INSERT INTO status_to_accounts VALUES (10,2)").Error)
	e := queueEvent(t, w, "sql-ack-error", models.FeedStatusCreate, models.StatusVisibilityDirect)
	b := models.FeedBatch{EventID: e.ID, TargetKind: "home", Recipients: []models.FeedRecipient{{AccountID: 2}}}
	require.NoError(t, w.DB.Create(&b).Error)
	var failed atomic.Bool
	require.NoError(t, w.DB.Callback().Update().Before("gorm:update").Register("test_feed_ack", func(tx *gorm.DB) {
		changes, ok := tx.Statement.Dest.(map[string]any)
		if tx.Statement.Table == "feed_batches" && ok && changes["state"] == "complete" && failed.CompareAndSwap(false, true) {
			tx.AddError(errors.New("SQL acknowledgement unavailable"))
		}
	}))
	_, err := w.DeliverOnce(ctx)
	require.Error(t, err)
	require.NoError(t, w.DB.First(&b, b.ID).Error)
	require.Equal(t, "pending", b.State)
	require.EqualValues(t, 1, b.Attempts)
	require.Equal(t, []string{"10"}, client.ZRange(ctx, feed.TimelineKey(&home.HomeTimeline{Account: &models.Account{Id: 2}}), 0, -1).Val())
	require.NoError(t, w.DB.Model(&models.FeedBatch{}).Where("id = ?", b.ID).UpdateColumn("next_attempt_at", nil).Error)
	_, err = w.DeliverOnce(ctx)
	require.NoError(t, err)
	require.NoError(t, w.DB.First(&b, b.ID).Error)
	require.Equal(t, "complete", b.State)
}

func TestFixedRecipientPagesSurviveAccountDeletion(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy_cursor_%t", legacy), func(t *testing.T) {
			w, _ := workerFixture(t)
			ctx := context.Background()
			for id := 5; id <= 520; id++ {
				require.NoError(t, w.DB.Exec("INSERT INTO accounts (id) VALUES (?)", id).Error)
			}
			require.NoError(t, w.DB.Exec("UPDATE accounts SET domain='remote.example' WHERE id=2").Error)
			ids := []uint64{0, 9999, 3, 3, 4}
			// Deliberately unsorted and duplicated source IDs: cursors must use a
			// stable identity order, without granting remote/missing accounts a feed.
			for id := 520; id >= 1; id-- {
				ids = append(ids, uint64(id))
			}
			event := models.FeedEvent{EventKey: "fixed-recipient-pages", Kind: models.FeedStatusCreate, ObjectType: "status", ObjectID: 10, ActorID: 1, ObjectVersion: 1, Metadata: models.FeedEventMetadata{ActorID: 1, Visibility: models.StatusVisibilityDirect, RecipientIDs: ids}}
			if legacy {
				event.ScanCursor = map[string]uint64{"fixed:home": 512, "batch:home": 4}
			}
			require.NoError(t, w.DB.Create(&event).Error)
			if legacy {
				// Old workers filtered remote ID 2 before persisting the offset. Keep
				// their four already-registered batches, then upgrade midway through.
				prior := []models.FeedRecipient{}
				for id := uint64(1); id <= 513; id++ {
					if id != 2 {
						prior = append(prior, models.FeedRecipient{AccountID: id})
					}
				}
				for i := 0; i < 4; i++ {
					require.NoError(t, w.DB.Create(&models.FeedBatch{EventID: event.ID, TargetKind: "home", BatchNo: uint64(i), Recipients: prior[i*128 : (i+1)*128]}).Error)
				}
			} else {
				_, err := w.ScanOnce(ctx)
				require.NoError(t, err)
				require.NoError(t, w.DB.First(&event, event.ID).Error)
				require.Equal(t, uint64(512), event.ScanCursor["fixed:home"])
				var count int64
				require.NoError(t, w.DB.Model(&models.FeedBatch{}).Where("event_id = ?", event.ID).Count(&count).Error)
				require.EqualValues(t, 4, count)
			}
			// Removing an earlier row used to shrink the reloaded array and skip
			// a still-valid recipient at the next page boundary. Delete a
			// recipient, keeping the author and their content valid.
			require.NoError(t, w.DB.Exec("UPDATE accounts SET deleted_at=CURRENT_TIMESTAMP WHERE id=3").Error)
			for i := 0; i < 12; i++ {
				require.NoError(t, w.DB.Model(&models.FeedEvent{}).Where("id = ?", event.ID).UpdateColumn("next_attempt_at", nil).Error)
				_, err := w.ScanOnce(ctx)
				require.NoError(t, err)
				require.NoError(t, w.DB.First(&event, event.ID).Error)
				if event.ScanComplete {
					break
				}
			}
			require.True(t, event.ScanComplete)
			var batches []models.FeedBatch
			require.NoError(t, w.DB.Where("event_id = ?", event.ID).Find(&batches).Error)
			seen := map[string]map[uint64]int{}
			for _, batch := range batches {
				require.LessOrEqual(t, len(batch.Recipients), 128)
				targets, err := w.targets(ctx, &batch)
				require.NoError(t, err)
				if seen[batch.TargetKind] == nil {
					seen[batch.TargetKind] = map[uint64]int{}
				}
				for _, target := range targets {
					require.NotNil(t, target.account)
					require.True(t, target.account.Id == 1 || target.account.Id >= 4)
					require.LessOrEqual(t, target.account.Id, uint64(520))
					seen[batch.TargetKind][target.account.Id]++
				}
			}
			for _, kind := range []string{"home", "mention", "direct"} {
				require.Len(t, seen[kind], 518)
				for id := uint64(1); id <= 520; id++ {
					if id == 2 || id == 3 {
						continue
					}
					require.GreaterOrEqual(t, seen[kind][id], 1, "%s recipient %d omitted", kind, id)
					if !legacy {
						require.Equal(t, 1, seen[kind][id], "%s duplicate recipient %d", kind, id)
					}
				}
			}
		})
	}
}
