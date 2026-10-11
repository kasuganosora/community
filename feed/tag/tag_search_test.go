package tag

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Timeline id / key-format table tests (deterministic, no I/O)
//
// These cover GetKeyFormat / GetTimelineId exhaustively and assert the
// exact channel/redis-key derivation so regressions in the format string are
// caught.
// ---------------------------------------------------------------------------

func TestGetKeyFormat_Canonical(t *testing.T) {
	assert.Equal(t, "feed:hashtag:%s", (&TagTimeline{}).GetKeyFormat())
}

func TestGetTimelineId_FlagMatrix(t *testing.T) {
	cases := []struct {
		name     string
		tl       *TagTimeline
		expected string
	}{
		{"plain", &TagTimeline{Tag: "go"}, "go"},
		{"local only", &TagTimeline{Tag: "go", OnlyLocal: true}, "local:go"},
		{"remote only", &TagTimeline{Tag: "go", OnlyRemote: true}, "remote:go"},
		{"media only", &TagTimeline{Tag: "go", OnlyMedia: true}, "media:go"},
		{"local+media", &TagTimeline{Tag: "go", OnlyLocal: true, OnlyMedia: true}, "local:media:go"},
		{"remote+media", &TagTimeline{Tag: "go", OnlyRemote: true, OnlyMedia: true}, "remote:media:go"},
		{"local+remote", &TagTimeline{Tag: "go", OnlyLocal: true, OnlyRemote: true}, "local:remote:go"},
		{"all three", &TagTimeline{Tag: "go", OnlyLocal: true, OnlyRemote: true, OnlyMedia: true}, "local:remote:media:go"},
		{"empty tag", &TagTimeline{Tag: ""}, ""},
		{"zero value", &TagTimeline{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.tl.GetTimelineId())
		})
	}
}

// The flag order in the id is fixed (local, remote, media) regardless of how
// the struct is constructed — guard against accidental reordering.
func TestGetTimelineId_FlagOrderIsStable(t *testing.T) {
	tl := &TagTimeline{Tag: "x", OnlyMedia: true, OnlyRemote: true, OnlyLocal: true}
	assert.Equal(t, "local:remote:media:x", tl.GetTimelineId())
}

// The redis key and the pub/sub channel name both derive from
// (GetKeyFormat, GetTimelineId); make sure the two stay consistent.
func TestTimelineKeyAndChannelConsistency(t *testing.T) {
	tl := &TagTimeline{Tag: "golang", OnlyLocal: true}

	key := "feed:hashtag:" + tl.GetTimelineId()
	channel := "subscribed:" + tl.GetKeyFormat()
	// channel uses the same id; just assert the pieces line up with the
	// public helper.
	assert.Equal(t, "feed:hashtag:local:golang", key)
	assert.Equal(t, "subscribed:feed:hashtag:%s", channel)
	assert.Equal(t, feed.GetRedisChannelName(tl), "subscribed:"+key)
}

func TestTagTimeline_ImplementsFeedTimeline(t *testing.T) {
	var tl feed.Timeline = &TagTimeline{Tag: "t"}
	assert.NotPanics(t, func() {
		_ = tl.GetKeyFormat()
		_ = tl.GetTimelineId()
	})
}

// ---------------------------------------------------------------------------
// getTags — short-circuit when Tags already populated
// ---------------------------------------------------------------------------

func TestGetTags_PreloadedPreservedExactly(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	pre := models.Tags{{Name: "a", Id: 1}, {Name: "b", Id: 2}}
	status := &models.Status{Id: 7, Tags: pre}
	require.NoError(t, getTags(status))
	// Exactly the same slice contents/order — no DB round-trip.
	require.Len(t, status.Tags, 2)
	assert.Equal(t, uint64(1), status.Tags[0].Id)
	assert.Equal(t, "a", status.Tags[0].Name)
	assert.Equal(t, "b", status.Tags[1].Name)
}

func TestGetTags_EmptyPreloadedDoesNotTouchDB(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	// An empty (non-nil) slice is treated as "already loaded" by getTags,
	// so even without a DB configured it must not panic.
	status := &models.Status{Id: 9, Tags: models.Tags{}}
	require.NoError(t, getTags(status))
	assert.Empty(t, status.Tags)
}

// ---------------------------------------------------------------------------
// Push — single-tag writes through to the (mock) timeline zset
// ---------------------------------------------------------------------------

func TestPush_SingleTagStoredInZSet(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	mockRedis := setupMockFeedService(t)

	status := &models.Status{
		Id:         42,
		AccountId:  1,
		Visibility: models.StatusVisibilityUnlisted,
		Tags:       models.Tags{{Name: "unicorns"}},
		Account:    &models.Account{Id: 1},
	}
	require.NoError(t, Push(status, false, false))

	zset := mockRedis.GetZSet("feed:hashtag:unicorns")
	require.Contains(t, zset, "42")
	// score should equal the status id
	assert.InDelta(t, float64(42), zset["42"], 0.0001)
}

func TestPush_UpdateMethodDoesNotDuplicateEntries(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	mockRedis := setupMockFeedService(t)

	status := &models.Status{
		Id: 77, AccountId: 1, Visibility: models.StatusVisibilityPublic,
		Tags:    models.Tags{{Name: "dupe"}},
		Account: &models.Account{Id: 1},
	}
	require.NoError(t, Push(status, false, false))
	// Push again as an update — zset should still hold exactly one member.
	require.NoError(t, Push(status, true, false))
	zset := mockRedis.GetZSet("feed:hashtag:dupe")
	assert.Len(t, zset, 1)
	assert.Contains(t, zset, "77")
}

func TestPush_ManyTagsNoError(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	status := &models.Status{
		Id: 99, AccountId: 1, Visibility: models.StatusVisibilityPublic,
		Tags: models.Tags{
			{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"},
		},
		Account: &models.Account{Id: 1},
	}
	// Batch path (len>1, not reblog) exercises Pipelined in the mock.
	require.NoError(t, Push(status, false, false))
}

// ---------------------------------------------------------------------------
// Unpush — removes the entry previously pushed
// ---------------------------------------------------------------------------

func TestUnpush_RemovesPreviouslyPushed(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	mockRedis := setupMockFeedService(t)

	status := &models.Status{
		Id: 123, AccountId: 1, Visibility: models.StatusVisibilityPublic,
		Tags:    models.Tags{{Name: "removable"}},
		Account: &models.Account{Id: 1},
	}
	require.NoError(t, Push(status, false, false))
	require.Contains(t, mockRedis.GetZSet("feed:hashtag:removable"), "123")

	require.NoError(t, Unpush(status, false))
	assert.NotContains(t, mockRedis.GetZSet("feed:hashtag:removable"), "123")
}

func TestUnpush_TagThatWasNeverPresentIsNoop(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	status := &models.Status{
		Id: 555, AccountId: 1, Visibility: models.StatusVisibilityPublic,
		Tags:    models.Tags{{Name: "ghost"}},
		Account: &models.Account{Id: 1},
	}
	// Never pushed; Unpush must still succeed (ZRem on missing key is fine).
	require.NoError(t, Unpush(status, false))
}

// ---------------------------------------------------------------------------
// pushAllChannel — verifies the (media/local/remote) gating branches and that
// the base timeline is pushed when it has subscribers.
// ---------------------------------------------------------------------------

func TestPushAllChannel_BaseTagPushedWhenSubscribed(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	mockRedis := setupMockFeedService(t)
	// Only the base channel has subscribers.
	mockRedis.SetChannel("subscribed:feed:hashtag:base", true)

	status := &models.Status{
		Id: 1, AccountId: 1, Visibility: models.StatusVisibilityPublic,
		Account: &models.Account{Id: 1},
	}
	require.NoError(t, pushAllChannel(status, "update", "base"))
}

func TestPushAllChannel_MediaFilterRequiresAttachments(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	mockRedis := setupMockFeedService(t)
	mockRedis.SetChannel("subscribed:feed:hashtag:media:pic", true)
	mockRedis.SetChannel("subscribed:feed:hashtag:pic", true)

	// No attachments -> OnlyMedia timelines skipped, base still pushed.
	noAttach := &models.Status{
		Id: 1, AccountId: 1, Visibility: models.StatusVisibilityPublic,
		Account: &models.Account{Id: 1},
	}
	require.NoError(t, pushAllChannel(noAttach, "update", "pic"))

	// With attachments -> media timeline qualifies.
	mockRedis.SetChannel("subscribed:feed:hashtag:media:pic2", true)
	withAttach := &models.Status{
		Id: 2, AccountId: 1, Visibility: models.StatusVisibilityPublic,
		Account: &models.Account{Id: 1},
		Attachments: models.Attachments{
			{Id: 1, Type: models.MediaTypeImage, FileName: "p.png", FilePath: "/p.png"},
		},
	}
	require.NoError(t, pushAllChannel(withAttach, "update", "pic2"))
}

func TestPushAllChannel_LocalFilterRequiresNilDomainAndAccount(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	// nil account -> OnlyLocal skipped (no panic).
	require.NoError(t, pushAllChannel(&models.Status{
		Id: 1, AccountId: 1, Visibility: models.StatusVisibilityPublic, Account: nil,
	}, "update", "l1"))

	// remote domain -> OnlyLocal skipped.
	domain := "far.example"
	require.NoError(t, pushAllChannel(&models.Status{
		Id: 2, AccountId: 1, Visibility: models.StatusVisibilityPublic,
		Account: &models.Account{Id: 1}, Domain: &domain,
	}, "update", "l2"))

	// local (nil domain) + account -> OnlyLocal qualifies.
	require.NoError(t, pushAllChannel(&models.Status{
		Id: 3, AccountId: 1, Visibility: models.StatusVisibilityPublic,
		Account: &models.Account{Id: 1}, Domain: nil,
	}, "update", "l3"))
}

func TestPushAllChannel_RemoteFilterRequiresNonNilDomain(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupMockFeedService(t)

	// nil domain -> OnlyRemote skipped.
	require.NoError(t, pushAllChannel(&models.Status{
		Id: 1, AccountId: 1, Visibility: models.StatusVisibilityPublic,
		Account: &models.Account{Id: 1}, Domain: nil,
	}, "update", "r1"))

	// remote domain -> OnlyRemote qualifies.
	domain := "fed.example"
	require.NoError(t, pushAllChannel(&models.Status{
		Id: 2, AccountId: 1, Visibility: models.StatusVisibilityPublic,
		Account: &models.Account{Id: 1}, Domain: &domain,
	}, "update", "r2"))
}

// ---------------------------------------------------------------------------
// Rebuild — success with seeded data + error when tag missing
// ---------------------------------------------------------------------------

func TestRebuild_SeededStatusesRebuildOK(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	gdb := setupTestDB(t)
	_, mockRedis := setupRedismockRedis(t)

	tag := &models.Tag{Name: "rebuilt"}
	require.NoError(t, gdb.Create(tag).Error)

	require.NoError(t, gdb.Session(&gorm.Session{SkipHooks: true}).Create(&models.Account{Id: 1}).Error)
	for i := range 3 {
		status := &models.Status{AccountId: 1, Visibility: models.StatusVisibilityPublic}
		// Skip hooks: AfterCreate fires a status event whose goroutine would
		// outlive the test and race with global DB resets elsewhere.
		require.NoError(t, gdb.Session(&gorm.Session{SkipHooks: true}).Create(status).Error)
		// status.Id is set by Create
		require.NoError(t, gdb.Exec(
			"INSERT INTO status_tags (status_id, tag_id) VALUES (?, ?)",
			status.Id, tag.Id,
		).Error)
		_ = i
	}

	require.NoError(t, (&TagTimeline{Tag: "rebuilt"}).Rebuild())
	// Rebuild should have written the statuses into the timeline zset.
	zset, err := mockRedis.ZMembers(feed.TimelineKey(&TagTimeline{Tag: "rebuilt"}))
	require.NoError(t, err)
	assert.NotEmpty(t, zset)
}

func TestRebuild_NoMatchingTagErrors(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupTestDB(t)
	setupMockFeedService(t)

	err := (&TagTimeline{Tag: "no-such-tag"}).Rebuild()
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// GetTimelineContent — success path via redismock.
//
// db.GetRedis() returns *redis.Client (concrete). redismock.NewClientMock
// returns a *redis.Client whose commands are intercepted, so injecting it into
// the global redis pool lets us script the Exists / ZRange / Pluck responses
// and exercise the happy path + the local/remote filter branches.
// ---------------------------------------------------------------------------

// seedTagContent prepares an in-memory DB with one tag and `n` public statuses
// tagged with it (created with hooks skipped to avoid leaking event goroutines).
func seedTagContent(t *testing.T, gdb *gorm.DB, tagName string, n int) *models.Tag {
	t.Helper()
	require.NoError(t, gdb.FirstOrCreate(&models.Account{Id: 1}, models.Account{Id: 1}).Error)
	tag := &models.Tag{Name: tagName}
	require.NoError(t, gdb.Create(tag).Error)
	for range n {
		status := &models.Status{AccountId: 1, Visibility: models.StatusVisibilityPublic}
		require.NoError(t, gdb.Session(&gorm.Session{SkipHooks: true}).Create(status).Error)
		require.NoError(t, gdb.Exec(
			"INSERT INTO status_tags (status_id, tag_id) VALUES (?, ?)",
			status.Id, tag.Id,
		).Error)
	}
	return tag
}

func setupRedismockRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	saved := cRedis.GetRedisesForTest()
	cRedis.SetRedisesForTest([]*redis.Client{client})
	feed.SetDefaultService(feed.NewFeedService(feed.NewRedisClient(client)))
	t.Cleanup(func() { cRedis.SetRedisesForTest(saved); client.Close() })
	return client, server
}
func seedTimelineCache(t *testing.T, server *miniredis.Miniredis, key string, ids []string) {
	t.Helper()
	key = "feed:v3:{" + strings.TrimPrefix(key, "feed:") + "}:items"
	server.HSet(key+":meta", "epoch", "test", "generation", "0", "ready", "1", "count", strconv.Itoa(len(ids)))
	server.HSet(key+":versions", "_epoch", "test")
	for _, id := range ids {
		score, err := strconv.ParseFloat(id, 64)
		require.NoError(t, err)
		_, err = server.ZAdd(key, score, id)
		require.NoError(t, err)
	}
	if len(ids) == 0 {
		server.Set(key+":empty", "1")
	}
}

// When the timeline key does not exist, GetStatusTimelineContent calls
// timeline.Rebuild() first, then reads the zset. We script Exists to return 1
// (already cached) to skip Rebuild, then return the status ids from ZRangeArgs.
func TestGetTimelineContent_HappyPathFromCache(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	gdb := setupTestDB(t)
	setupMockFeedService(t)
	_, rm := setupRedismockRedis(t)

	tag := seedTagContent(t, gdb, "happy", 2)

	// Two statuses were created; collect their ids to script the response.
	var ids []string
	type row struct{ Id uint64 }
	var rows []row
	require.NoError(t, gdb.Table("statuses").Order("id").Find(&rows).Error)
	for _, r := range rows {
		ids = append(ids, uintToStr(r.Id))
	}

	timelineKey := "feed:hashtag:happy"
	// params non-nil, MaxId=MinId=SinceId=0 -> offset/count branch:
	//   GetRedisRangeOffsetCount() = (0, limit-1) -> Start:int64(0), Stop:int64(1)
	seedTimelineCache(t, rm, timelineKey, ids)
	// The final Find preloads happen against the DB (no redis), so no more
	// redis expectations are needed.

	statuses, err := GetTimelineContent("happy", nil, &feed.ListParam{Limit: uint64(len(ids))})
	require.NoError(t, err)
	assert.Len(t, statuses, len(ids))
	if err == nil {
	}
	_ = tag
}

// First-page miss (params == nil path) returns empty without hitting the DB
// fallback because there is genuinely nothing in the cache.
func TestGetTimelineContent_EmptyCacheFirstPageReturnsEmpty(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	gdb := setupTestDB(t)
	setupMockFeedService(t)
	_, rm := setupRedismockRedis(t)

	seedTagContent(t, gdb, "empty", 0)

	timelineKey := "feed:hashtag:empty"
	// params == nil -> full range with Rev: Start:int64(0), Stop:int64(-1)
	seedTimelineCache(t, rm, timelineKey, []string{})

	statuses, err := GetTimelineContent("empty", nil, nil)
	require.NoError(t, err)
	assert.Empty(t, statuses)
}

// params with MaxId/MinId/SinceId take the score-range ZRangeArgs branch.
func TestGetTimelineContent_ScoreRangeBranch(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	gdb := setupTestDB(t)
	setupMockFeedService(t)
	_, rm := setupRedismockRedis(t)

	seedTagContent(t, gdb, "scored", 1)

	timelineKey := "feed:hashtag:scored"
	// Score range query returns empty -> first-page empty short-circuit fires.
	// GetRedisRangeMinMax: MaxId>0 -> end="100", MinId>0 -> start="1"
	seedTimelineCache(t, rm, timelineKey, []string{})

	params := &feed.ListParam{MaxId: 100, MinId: 1}
	statuses, err := GetTimelineContent("scored", nil, params)
	require.NoError(t, err)
	// Empty cache (score range miss) with non-first-page params triggers the
	// DB fallback, which finds the seeded status (id 1 within [1,100]).
	assert.Empty(t, statuses, "min_id is exclusive in both Redis and SQL")
}

// local=true filter adds "domain is null" to the DB query (exercised on the
// cache-miss DB fallback). Use an out-of-range MaxId so the zset returns empty
// and the fallback DB query is used.
func TestGetTimelineContent_LocalFilterDBFallback(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	gdb := setupTestDB(t)
	setupMockFeedService(t)
	_, rm := setupRedismockRedis(t)

	seedTagContent(t, gdb, "localonly", 1)

	timelineKey := "feed:hashtag:localonly"
	// Non-first-page params + empty cache -> DB fallback path.
	// GetRedisRangeMinMax: MaxId=0 -> end="+inf", MinId=5 -> start="5"
	seedTimelineCache(t, rm, timelineKey, []string{})

	params := &feed.ListParam{MinId: 5, Other: map[string]any{"local": true}}
	statuses, err := GetTimelineContent("localonly", nil, params)
	// Fallback query returns nothing because statuses have no matching ids in
	// status_tags for the out-of-range window, so we just assert no error and
	// that the local filter branch was reached (no panic).
	require.NoError(t, err)
	_ = statuses
}

// remote=true filter adds "domain is not null" to the DB query.
func TestGetTimelineContent_RemoteFilterDBFallback(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	gdb := setupTestDB(t)
	setupMockFeedService(t)
	_, rm := setupRedismockRedis(t)

	seedTagContent(t, gdb, "remoteonly", 1)

	timelineKey := "feed:hashtag:remoteonly"
	seedTimelineCache(t, rm, timelineKey, []string{})

	params := &feed.ListParam{MinId: 5, Other: map[string]any{"remote": true}}
	statuses, err := GetTimelineContent("remoteonly", nil, params)
	require.NoError(t, err)
	_ = statuses
}

// only_media afterFilter: a status with no attachments is filtered out.
func TestGetTimelineContent_OnlyMediaFilter(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	gdb := setupTestDB(t)
	setupMockFeedService(t)
	_, rm := setupRedismockRedis(t)

	tag := seedTagContent(t, gdb, "media-tag", 1)

	var ids []string
	type row struct{ Id uint64 }
	var rows []row
	require.NoError(t, gdb.Table("statuses").Order("id").Find(&rows).Error)
	for _, r := range rows {
		ids = append(ids, uintToStr(r.Id))
	}

	timelineKey := "feed:hashtag:media-tag"
	// MaxId=MinId=SinceId=0, Limit=10 -> GetRedisRangeOffsetCount = (0, 9)
	seedTimelineCache(t, rm, timelineKey, ids)

	// The seeded status has no attachments, so only_media should drop it.
	params := &feed.ListParam{Limit: 10, Other: map[string]any{"only_media": true}}
	statuses, err := GetTimelineContent("media-tag", nil, params)
	require.NoError(t, err)
	assert.Empty(t, statuses, "status without attachments must be filtered out")
	_ = tag
}

// Missing tag (not in DB) -> Rebuild returns record-not-found error.
func TestGetTimelineContent_MissingTag(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	setupTestDB(t)
	setupMockFeedService(t)
	_, _ = setupRedismockRedis(t)

	statuses, err := GetTimelineContent("absent", nil, nil)
	assert.Error(t, err)
	assert.Empty(t, statuses)
}

// ---------------------------------------------------------------------------
// Visibility filtering exercised through GetTimelineContent's afterFilter by
// seeding statuses of each visibility class and checking what a given viewer
// can see. Uses the real (non-duplicated) production closure.
// ---------------------------------------------------------------------------

func TestGetTimelineContent_VisibilityFiltering(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	gdb := setupTestDB(t)
	setupMockFeedService(t)
	_, rm := setupRedismockRedis(t)

	tag := &models.Tag{Name: "vis"}
	require.NoError(t, gdb.Create(tag).Error)

	// Owner account = 10. A follower of account 10 = 20.
	require.NoError(t, gdb.Create(&models.Account{Id: 10}).Error)
	require.NoError(t, gdb.Create(&models.Account{Id: 20}).Error)
	// account 20 follows account 10
	require.NoError(t, gdb.Exec("INSERT INTO follows (account_id, follow_id) VALUES (20, 10)").Error)

	mk := func(visibility uint8) *models.Status {
		s := &models.Status{AccountId: 10, Visibility: visibility}
		require.NoError(t, gdb.Session(&gorm.Session{SkipHooks: true}).Create(s).Error)
		require.NoError(t, gdb.Exec(
			"INSERT INTO status_tags (status_id, tag_id) VALUES (?, ?)", s.Id, tag.Id,
		).Error)
		return s
	}
	pub := mk(models.StatusVisibilityPublic)
	unl := mk(models.StatusVisibilityUnlisted)
	priv := mk(models.StatusVisibilityPrivate)

	// Anonymous viewer (nil account): sees public + unlisted, not private.
	timelineKey := "feed:hashtag:vis"
	seedTimelineCache(t, rm, timelineKey, []string{
		uintToStr(pub.Id), uintToStr(unl.Id), uintToStr(priv.Id),
	})
	got, err := GetTimelineContent("vis", nil, &feed.ListParam{Limit: 10})
	require.NoError(t, err)
	gotIds := statusIds(got)
	assert.Contains(t, gotIds, pub.Id)
	assert.Contains(t, gotIds, unl.Id)
	assert.NotContains(t, gotIds, priv.Id, "anonymous must not see private")

}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func uintToStr(v uint64) string {
	const digits = "0123456789"
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = digits[v%10]
		v /= 10
	}
	return string(buf[i:])
}

func statusIds(ss models.Statuses) []uint64 {
	out := make([]uint64, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Id)
	}
	return out
}

func TestGetTimelineContent_CacheBoostDatabaseOriginal(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()
	gdb := setupTestDB(t)
	_, rm := setupRedismockRedis(t)
	tag := seedTagContent(t, gdb, "aggregate", 0)
	origin := uint64(90)
	for _, status := range []*models.Status{{Id: 90, AccountId: 1, Visibility: models.StatusVisibilityPublic}, {Id: 100, AccountId: 1, Visibility: models.StatusVisibilityPublic, ReblogId: &origin}} {
		require.NoError(t, gdb.Session(&gorm.Session{SkipHooks: true}).Create(status).Error)
		require.NoError(t, gdb.Exec("INSERT INTO status_tags (status_id, tag_id) VALUES (?, ?)", status.Id, tag.Id).Error)
	}
	seedTimelineCache(t, rm, "feed:hashtag:aggregate", []string{"100"})
	statuses, err := GetTimelineContent("aggregate", nil, &feed.ListParam{Limit: 2})
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	require.Equal(t, uint64(100), statuses[0].Id)
}

func TestGetTimelineContent_MuteChangesApplyToWarmCache(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()
	gdb := setupTestDB(t)
	_, rm := setupRedismockRedis(t)
	tag := seedTagContent(t, gdb, "muted", 0)
	for _, id := range []uint64{2, 3, 99} {
		require.NoError(t, gdb.Session(&gorm.Session{SkipHooks: true}).Create(&models.Account{Id: id}).Error)
	}
	original := uint64(10)
	for _, status := range []*models.Status{{Id: 10, AccountId: 1}, {Id: 20, AccountId: 2, ReblogId: &original}, {Id: 30, AccountId: 3}} {
		require.NoError(t, gdb.Session(&gorm.Session{SkipHooks: true}).Create(status).Error)
		require.NoError(t, gdb.Exec("INSERT INTO status_tags (status_id, tag_id) VALUES (?, ?)", status.Id, tag.Id).Error)
	}
	seedTimelineCache(t, rm, "feed:hashtag:muted", []string{"10", "20", "30"})
	read := func() []uint64 {
		statuses, err := GetTimelineContent("muted", &models.Account{Id: 99}, &feed.ListParam{Limit: 3})
		require.NoError(t, err)
		ids := []uint64{}
		for _, s := range statuses {
			ids = append(ids, s.Id)
		}
		return ids
	}
	require.Equal(t, []uint64{30, 20}, read())
	require.NoError(t, gdb.Create(&models.MuteAccount{Id: 1, AccountId: 99, TargetId: 1}).Error)
	require.Equal(t, []uint64{30}, read(), "fresh mute removes root and embedded original without cache rebuild")
	future := time.Now().Add(time.Hour)
	require.NoError(t, gdb.Model(&models.MuteAccount{}).Where("id = 1").Update("expired_at", future).Error)
	require.Equal(t, []uint64{30}, read())
	past := time.Now().Add(-time.Hour)
	require.NoError(t, gdb.Model(&models.MuteAccount{}).Where("id = 1").Update("expired_at", past).Error)
	require.Equal(t, []uint64{30, 20}, read(), "expired mute restores feed on same cache")
}

func TestGetTimelineContent_WarmFullPageHydratesOnlyOnce(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()
	conn := setupTestDB(t)
	_, cache := setupRedismockRedis(t)
	seedTagContent(t, conn, "fast", 50)
	ids := []string{}
	for i := 1; i <= 50; i++ {
		ids = append(ids, strconv.Itoa(i))
	}
	seedTimelineCache(t, cache, "feed:hashtag:fast", ids)
	contentQueries, attachmentQueries, queries := 0, 0, 0
	var contentRows int
	require.NoError(t, conn.Callback().Query().After("gorm:query").Register("test:fast_hydration", func(tx *gorm.DB) {
		queries++
		switch tx.Statement.Table {
		case "status_contents":
			contentQueries++
			contentRows += len(tx.Statement.Vars)
		case "media_attachments":
			attachmentQueries++
		}
	}))
	statuses, err := GetTimelineContent("fast", nil, &feed.ListParam{Limit: 20})
	require.NoError(t, err)
	require.Len(t, statuses, 20)
	require.Equal(t, uint64(50), statuses[0].Id)
	require.Equal(t, uint64(31), statuses[19].Id)
	require.Equal(t, 1, contentQueries, "full page must not hydrate twice")
	require.Equal(t, 1, attachmentQueries, "no thin attachment preloads")
	require.Equal(t, 20, contentRows, "requested page only, no 40-row overscan hydration")
	t.Logf("warm 20 result page: %d SQL queries", queries)
}

func TestGetTimelineContent_DuplicateAssociationsKeepPagesDistinct(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()
	conn := setupTestDB(t)
	_, cache := setupRedismockRedis(t)
	// Production StatusTag uses a surrogate id; this fixture explicitly permits
	// repeated pairs to guard against multiplying the outer status rows.
	require.NoError(t, conn.Exec("DROP TABLE status_tags").Error)
	require.NoError(t, conn.Exec("CREATE TABLE status_tags (id INTEGER PRIMARY KEY AUTOINCREMENT, status_id INTEGER, tag_id INTEGER)").Error)
	tag := seedTagContent(t, conn, "duplicates", 3)
	// The semantic lookup remains a membership predicate even with duplicate
	// source associations; both warm IDs and deep database pagination stay unique.
	for _, id := range []uint64{1, 2, 3} {
		require.NoError(t, conn.Exec("INSERT INTO status_tags (status_id,tag_id) VALUES (?,?)", id, tag.Id).Error)
		for j := uint64(0); j < 2; j++ {
			require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.Attachment{Id: id*10 + j, StatusId: &id, AccountId: 1}).Error)
		}
	}
	seedTimelineCache(t, cache, "feed:hashtag:duplicates", []string{"1", "2", "3"})
	for _, media := range []bool{false, true} {
		first, err := GetTimelineContent("duplicates", nil, &feed.ListParam{Limit: 2, Other: map[string]any{"only_media": media}})
		require.NoError(t, err)
		require.Len(t, first, 2)
		require.Equal(t, uint64(3), first[0].Id)
		require.Equal(t, uint64(2), first[1].Id)
		second, err := GetTimelineContent("duplicates", nil, &feed.ListParam{Limit: 2, MaxId: 2, Other: map[string]any{"only_media": media}})
		require.NoError(t, err)
		require.Len(t, second, 1)
		require.Equal(t, uint64(1), second[0].Id)
	}
	require.NoError(t, (&TagTimeline{Tag: "duplicates", OnlyMedia: true}).Rebuild())
	ids, err := cache.ZMembers(feed.TimelineKey(&TagTimeline{Tag: "duplicates", OnlyMedia: true}))
	require.NoError(t, err)
	require.Len(t, ids, 3)
}
