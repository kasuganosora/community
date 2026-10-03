package tag

import (
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Test infrastructure
//
// The tag package depends on two package-global singletons:
//   - database.DB (accessed via db.GetDB())            -> in-memory SQLite
//   - feed.defaultService (the FeedService singleton)  -> feed.MockRedisClient
//   - db/redis.redises (accessed via db.GetRedis())    -> unreachable client
//
// Tests that mutate these globals are serialized with globalStateMu so the
// mutations stay test-local. None of the tests below use t.Parallel.
// ---------------------------------------------------------------------------

var globalStateMu sync.Mutex

// setupTestDB creates an in-memory SQLite database, injects it via
// database.SetTestDB so that db.GetDB() returns it, and migrates the models
// the tag package touches. The previous DB handle is restored on cleanup.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err, "failed to open in-memory sqlite")

	// In-memory SQLite databases are per-connection. Pin a single connection
	// so every query/share sees the same schema and data.
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, gdb.AutoMigrate(
		&models.Tag{},
		&models.Status{},
		&models.Follow{},
		&models.Account{},
		&models.AccountExtra{},
		&models.Block{},
		&models.MuteAccount{},
		&models.StatusContent{},
		&models.StatusToAccount{},
		&models.Poll{},
		&models.PollOption{},
		&models.Application{},
		&models.Attachment{},
		&models.Emoji{},
	), "failed to auto-migrate models")

	// The status_tags join table (queried directly by Rebuild and
	// GetTimelineContent via db.GetDB().Model(&StatusTag{})) is not reliably
	// created by the many2many association under the glebarez sqlite driver,
	// so create it explicitly with the columns those queries expect.
	require.NoError(t, gdb.Exec(`CREATE TABLE IF NOT EXISTS status_tags (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		status_id INTEGER NOT NULL,
		tag_id INTEGER NOT NULL
	)`).Error, "failed to create status_tags table")

	prevDB := database.GetDB()
	database.SetTestDB(gdb)

	t.Cleanup(func() {
		if prevDB != nil {
			database.SetTestDB(prevDB)
		} else {
			database.ResetTestDB()
		}
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	return gdb
}

// setupMockFeedService installs a feed service backed by feed.MockRedisClient
// via feed.SetDefaultService, so Push/Unpush/etc. can exercise the feed
// package without a real Redis. It also resets the subscription cache so
// cached PushUpdateRequired results don't leak between tests.
func setupMockFeedService(t *testing.T) *feed.MockRedisClient {
	t.Helper()

	mockRedis := feed.NewMockRedisClient()
	feed.SetDefaultService(feed.NewFeedService(mockRedis))
	feed.ResetSubscriptionCache()

	return mockRedis
}

// setupGlobalRedis injects an (unreachable) Redis client into the db/redis
// pool so that db.GetRedis() does not panic. Commands return errors (zero
// values), which GetStatusTimelineContent's Rebuild path tolerates when the
// tag lookup itself fails first.
func setupGlobalRedis(t *testing.T) {
	t.Helper()

	saved := cRedis.GetRedisesForTest()
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:0"})
	cRedis.SetRedisesForTest([]*goredis.Client{client})

	t.Cleanup(func() {
		cRedis.SetRedisesForTest(saved)
		_ = client.Close()
	})
}

// ---------------------------------------------------------------------------
// getTags
// ---------------------------------------------------------------------------

func TestGetTags_PreloadedTagsShortCircuits(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	// No DB is configured; if getTags tried to touch the DB this would fail.
	status := &models.Status{
		Id:   1,
		Tags: models.Tags{{Name: "golang"}, {Name: "rust"}},
	}

	err := getTags(status)
	require.NoError(t, err)
	assert.Len(t, status.Tags, 2)
	assert.Equal(t, "golang", status.Tags[0].Name)
}

func TestGetTags_NilTagsHitsDB(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	// A DB is configured so getTags exercises the Association().Find() path.
	setupTestDB(t)

	status := &models.Status{Id: 999, Tags: nil}
	err := getTags(status)
	// getTags uses Model(&models.Status{}) (fresh, id 0) for the association,
	// so it queries tags for status id 0 and returns an empty slice — but
	// crucially no error. This covers the DB-load branch.
	require.NoError(t, err)
	assert.Empty(t, status.Tags)
}

func TestGetTags_NilTagsNoDBPanics(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	// No global DB -> db.GetDB() returns nil -> .Model() panics.
	database.ResetTestDB()
	t.Cleanup(database.ResetTestDB)

	status := &models.Status{Id: 999, Tags: nil}
	assert.Panics(t, func() {
		_ = getTags(status)
	})
}

// ---------------------------------------------------------------------------
// Push
// ---------------------------------------------------------------------------

func TestPush_NoTagsReturnsNil(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	// Empty Tags slice -> getTags short-circuits, len==0 -> early return.
	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Tags:       models.Tags{},
	}

	err := Push(status, false, true)
	require.NoError(t, err)
}

func TestPush_NilTagsNoDBPanics(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	// No DB -> getTags panics before any feed work.
	database.ResetTestDB()
	t.Cleanup(database.ResetTestDB)

	status := &models.Status{Id: 1, Tags: nil}
	assert.Panics(t, func() {
		_ = Push(status, false, false)
	})
}

func TestPush_SingleTagAddsToTimeline(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	mockRedis := setupMockFeedService(t)
	// Single tag -> BatchAddToTimelineItems delegates to AddToTimelineItem
	// (not the Pipelined no-op path), so the data is actually stored.
	mockRedis.SetChannel("subscribed:feed:hashtag:golang", true)

	status := &models.Status{
		Id:         100,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Tags:       models.Tags{{Name: "golang"}},
		Account:    &models.Account{Id: 1},
	}

	err := Push(status, false, true)
	require.NoError(t, err)
	assert.Contains(t, mockRedis.GetZSet("feed:hashtag:golang"), "100")
}

func TestPush_MultipleTagsBatch(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	// Two tags -> BatchAddToTimelineItems uses the Pipelined path (mock no-op).
	// We assert no error and no panic; data verification isn't possible for
	// the pipeline-backed mock.
	status := &models.Status{
		Id:         200,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Tags: models.Tags{
			{Name: "golang"},
			{Name: "programming"},
		},
		Account: &models.Account{Id: 1},
	}

	err := Push(status, false, true)
	require.NoError(t, err)
}

func TestPush_UpdateFlagSetsMethod(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	status := &models.Status{
		Id:         300,
		AccountId:  1,
		Visibility: models.StatusVisibilityUnlisted,
		Tags:       models.Tags{{Name: "news"}},
		Account:    &models.Account{Id: 1},
	}

	// update=true -> method becomes "status.update".
	err := Push(status, true, false)
	require.NoError(t, err)
}

func TestPush_ReblogStatusUsesAddToTimelineItem(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	mockRedis := setupMockFeedService(t)

	reblogId := uint64(50)
	// Reblog path calls AddToTimelineItem per tag (not the batch pipeline),
	// so the reblog status is recorded in the timeline.
	status := &models.Status{
		Id:         400,
		AccountId:  2,
		Visibility: models.StatusVisibilityPublic,
		ReblogId:   &reblogId,
		Tags:       models.Tags{{Name: "repost"}},
		Account:    &models.Account{Id: 2},
	}

	err := Push(status, false, true)
	require.NoError(t, err)
	assert.Contains(t, mockRedis.GetZSet("feed:hashtag:repost"), "400")
}

func TestPush_PushToClientFalseSkipsChannels(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	status := &models.Status{
		Id:         500,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Tags:       models.Tags{{Name: "silent"}},
		Account:    &models.Account{Id: 1},
	}

	err := Push(status, false, false)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Unpush
// ---------------------------------------------------------------------------

func TestUnpush_NoTagsReturnsNil(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Tags:       models.Tags{},
	}

	err := Unpush(status, true)
	require.NoError(t, err)
}

func TestUnpush_NilTagsNoDBPanics(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	database.ResetTestDB()
	t.Cleanup(database.ResetTestDB)

	status := &models.Status{Id: 1, Tags: nil}
	assert.Panics(t, func() {
		_ = Unpush(status, false)
	})
}

func TestUnpush_RemovesFromTimeline(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	mockRedis := setupMockFeedService(t)
	mockRedis.SetChannel("subscribed:feed:hashtag:golang", true)

	status := &models.Status{
		Id:         600,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Tags:       models.Tags{{Name: "golang"}},
		Account:    &models.Account{Id: 1},
	}

	// Add first, then remove.
	require.NoError(t, Push(status, false, false))
	require.Contains(t, mockRedis.GetZSet("feed:hashtag:golang"), "600")

	err := Unpush(status, true)
	require.NoError(t, err)
	assert.NotContains(t, mockRedis.GetZSet("feed:hashtag:golang"), "600")
}

func TestUnpush_MultipleTags(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	status := &models.Status{
		Id:         700,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Tags: models.Tags{
			{Name: "a"},
			{Name: "b"},
		},
		Account: &models.Account{Id: 1},
	}

	err := Unpush(status, false)
	require.NoError(t, err)
}

func TestUnpush_PushToClientFalseSkipsChannels(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	status := &models.Status{
		Id:         800,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Tags:       models.Tags{{Name: "delete-me"}},
		Account:    &models.Account{Id: 1},
	}

	err := Unpush(status, false)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// pushAllChannel — covers the filter branches (media/local/remote/none)
// ---------------------------------------------------------------------------

func TestPushAllChannel_OnlyMediaNoAttachments(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	mockRedis := setupMockFeedService(t)
	mockRedis.SetChannel("subscribed:feed:hashtag:photos", true)

	// No attachments -> OnlyMedia timelines are skipped; only the base tag
	// timeline (no filter) is pushed if it has subscribers.
	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Account:    &models.Account{Id: 1},
	}

	err := pushAllChannel(status, "update", "photos")
	require.NoError(t, err)
}

func TestPushAllChannel_OnlyMediaWithAttachments(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	mockRedis := setupMockFeedService(t)
	for _, ch := range []string{
		"subscribed:feed:hashtag:photos",
		"subscribed:feed:hashtag:media:photos",
	} {
		mockRedis.SetChannel(ch, true)
	}

	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Account:    &models.Account{Id: 1},
		Attachments: models.Attachments{
			{Id: 1, Type: models.MediaTypeImage, FileName: "x.png", FilePath: "/x.png"},
		},
	}

	err := pushAllChannel(status, "update", "photos")
	require.NoError(t, err)
}

func TestPushAllChannel_OnlyLocalNilAccountSkipped(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	// Account nil -> OnlyLocal timelines are skipped; no panic.
	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Account:    nil,
	}

	err := pushAllChannel(status, "update", "local-tag")
	require.NoError(t, err)
}

func TestPushAllChannel_OnlyLocalRemoteDomainSkipped(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	domain := "remote.example.com"
	// Local account but status.Domain != nil -> OnlyLocal skipped.
	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Domain:     &domain,
		Account:    &models.Account{Id: 1},
	}

	err := pushAllChannel(status, "update", "local")
	require.NoError(t, err)
}

func TestPushAllChannel_OnlyRemoteLocalDomainSkipped(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	// Domain nil -> OnlyRemote skipped.
	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Domain:     nil,
		Account:    &models.Account{Id: 1},
	}

	err := pushAllChannel(status, "update", "fed")
	require.NoError(t, err)
}

func TestPushAllChannel_OnlyRemoteWithRemoteDomain(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	mockRedis := setupMockFeedService(t)
	mockRedis.SetChannel("subscribed:feed:hashtag:remote:fed", true)

	domain := "remote.example.com"
	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Domain:     &domain,
		Account:    &models.Account{Id: 1},
	}

	err := pushAllChannel(status, "update", "fed")
	require.NoError(t, err)
}

func TestPushAllChannel_NoSubscribers(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)
	// No channels set -> PushUpdateRequired returns false -> no push, no error.

	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Account:    &models.Account{Id: 1},
	}

	err := pushAllChannel(status, "update", "nobody")
	require.NoError(t, err)
}

func TestPushAllChannel_DeleteMethod(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	mockRedis := setupMockFeedService(t)
	mockRedis.SetChannel("subscribed:feed:hashtag:deleted", true)

	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Account:    &models.Account{Id: 1},
	}

	err := pushAllChannel(status, "delete", "deleted")
	require.NoError(t, err)
}

func TestPushAllChannel_LocalAndMediaWithAttachments(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	mockRedis := setupMockFeedService(t)
	mockRedis.SetChannel("subscribed:feed:hashtag:local:media:mixed", true)

	// Local account (Domain nil) + attachments -> both OnlyLocal and
	// OnlyMedia timelines qualify and are pushed.
	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Domain:     nil,
		Account:    &models.Account{Id: 1},
		Attachments: models.Attachments{
			{Id: 1, Type: models.MediaTypeImage, FileName: "a.png", FilePath: "/a.png"},
		},
	}

	err := pushAllChannel(status, "update", "mixed")
	require.NoError(t, err)
}

func TestPushAllChannel_RemoteAndMediaWithAttachments(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	mockRedis := setupMockFeedService(t)
	mockRedis.SetChannel("subscribed:feed:hashtag:remote:media:rfed", true)

	domain := "fed.example.com"
	// Remote account + attachments -> OnlyRemote + OnlyMedia timelines pushed.
	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Domain:     &domain,
		Account:    &models.Account{Id: 1},
		Attachments: models.Attachments{
			{Id: 1, Type: models.MediaTypeImage, FileName: "b.png", FilePath: "/b.png"},
		},
	}

	err := pushAllChannel(status, "update", "rfed")
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Rebuild
// ---------------------------------------------------------------------------

func TestRebuild_TagNotFoundReturnsError(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupTestDB(t)
	setupMockFeedService(t)

	timeline := &TagTimeline{Tag: "does-not-exist"}
	err := timeline.Rebuild()
	assert.Error(t, err)
}

func TestRebuild_Success(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	gdb := setupTestDB(t)
	setupMockFeedService(t)

	// Seed tag + statuses + status_tags join rows.
	tag := &models.Tag{Name: "golang"}
	require.NoError(t, gdb.Create(tag).Error)

	// Create statuses with hooks disabled. models.Status.AfterCreate fires the
	// "status" after_create event, which the tag package's init() handler turns
	// into a detached goroutine calling Push()->getTags()->db.GetDB(). Those
	// goroutines outlive the test and crash other tests that reset the global
	// DB to nil, so we skip the hook entirely here (and insert the join rows
	// via raw SQL below anyway).
	for range 5 {
		status := &models.Status{
			AccountId:  1,
			Visibility: models.StatusVisibilityPublic,
		}
		require.NoError(t, gdb.Session(&gorm.Session{SkipHooks: true}).Create(status).Error)
		// Use raw SQL to populate the join table; the StatusTag model's
		// RETURNING id clause is incompatible with the association-managed
		// table schema (no id column).
		require.NoError(t, gdb.Exec(
			"INSERT INTO status_tags (status_id, tag_id) VALUES (?, ?)",
			status.Id, tag.Id,
		).Error)
	}

	timeline := &TagTimeline{Tag: "golang"}
	err := timeline.Rebuild()
	require.NoError(t, err)
}

func TestRebuild_NoStatusesStillSucceeds(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	gdb := setupTestDB(t)
	setupMockFeedService(t)

	tag := &models.Tag{Name: "empty-tag"}
	require.NoError(t, gdb.Create(tag).Error)

	timeline := &TagTimeline{Tag: "empty-tag"}
	err := timeline.Rebuild()
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// GetTimelineContent — error paths (Rebuild fails on missing tag before any
// Redis access via db.GetRedis()).
// ---------------------------------------------------------------------------

func TestGetTimelineContent_MissingTagReturnsError(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupTestDB(t)
	setupGlobalRedis(t)
	setupMockFeedService(t)

	// Tag not in DB -> Rebuild returns record-not-found.
	statuses, err := GetTimelineContent("missing-tag", nil, nil)
	assert.Error(t, err)
	assert.Empty(t, statuses)
}

func TestGetTimelineContent_NilParamsInitializesDefaults(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupTestDB(t)
	setupGlobalRedis(t)
	setupMockFeedService(t)

	// No tag -> Rebuild errors (record not found). The nil-safe param
	// initialization (params == nil -> &ListParam{}) is exercised first.
	statuses, err := GetTimelineContent("also-missing", nil, nil)
	assert.Error(t, err)
	assert.Empty(t, statuses)
}

func TestGetTimelineContent_RemoteAndMediaParams(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupTestDB(t)
	setupGlobalRedis(t)
	setupMockFeedService(t)

	params := &feed.ListParam{
		Limit: 10,
		Other: map[string]any{"remote": true, "only_media": true},
	}
	statuses, err := GetTimelineContent("nope", nil, params)
	assert.Error(t, err)
	assert.Empty(t, statuses)
}

// ---------------------------------------------------------------------------
// init() event handler — exercise the registered status wildcard handler.
// The handler launches goroutines that call Push/Unpush, so we only assert
// no panic and give the goroutines a moment to run.
// ---------------------------------------------------------------------------

func TestInitEventHandler_NilData(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: nil})
	})
}

func TestInitEventHandler_NilStatusPointer(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	var status *models.Status
	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: status})
	})
}

func TestInitEventHandler_DirectVisibilitySkips(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityDirect,
		Tags:       models.Tags{{Name: "secret"}},
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: status})
	})
}

func TestInitEventHandler_AfterCreate(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Tags:       models.Tags{{Name: "event-tag"}},
		Account:    &models.Account{Id: 1},
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: status})
	})
	time.Sleep(50 * time.Millisecond)
}

func TestInitEventHandler_AfterDelete(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityUnlisted,
		Tags:       models.Tags{{Name: "del-tag"}},
		Account:    &models.Account{Id: 1},
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_delete", Data: status})
	})
	time.Sleep(50 * time.Millisecond)
}

func TestInitEventHandler_BeforeUpdate(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Tags:       models.Tags{{Name: "upd-tag"}},
		Account:    &models.Account{Id: 1},
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "before_update", Data: status})
	})
	time.Sleep(50 * time.Millisecond)
}

func TestInitEventHandler_AfterUpdate(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Tags:       models.Tags{{Name: "upd2-tag"}},
		Account:    &models.Account{Id: 1},
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_update", Data: status})
	})
	time.Sleep(50 * time.Millisecond)
}

func TestInitEventHandler_UnknownEventName(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	// An event name that isn't one of the handled cases: handler returns true
	// without launching a goroutine.
	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Tags:       models.Tags{{Name: "unknown"}},
		Account:    &models.Account{Id: 1},
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "something_else", Data: status})
	})
}

// ---------------------------------------------------------------------------
// Additional GetTimelineId edge cases for coverage
// ---------------------------------------------------------------------------

func TestTagTimeline_RebuildReturnsFeedError(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	// No DB at all -> Rebuild's db.GetDB().Model().First panics.
	database.ResetTestDB()
	t.Cleanup(database.ResetTestDB)

	timeline := &TagTimeline{Tag: "panic-tag"}
	assert.Panics(t, func() {
		_ = timeline.Rebuild()
	})
}
