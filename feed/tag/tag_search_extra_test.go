package tag

import (
	"testing"

	"github.com/go-redis/redismock/v9"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// This file focuses on the remaining uncovered branches in tag_timeline.go:
//   - GetTimelineContent afterFilter: direct visibility (ToAccounts.Contains)
//   - GetTimelineContent afterFilter: private status visible to its own author
//   - GetTimelineContent afterFilter: private status visible to a follower
//   - Push with an actual subscriber channel (pushToClient path)
//   - Push reblog with multiple tags
//   - Unpush with pushToClient and a subscriber channel
//
// All helpers (setupTestDB / setupMockFeedService / setupRedismockRedis /
// seedTagContent / globalStateMu / uintToStr / statusIds) live in the other
// _test.go files in this package and are reused here.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// GetTimelineContent — afterFilter visibility branches
// ---------------------------------------------------------------------------

// setupMockFeedRedis installs both the in-memory mock feed service (so Push/
// feed functions work) and a redismock-backed global redis pool (so
// GetTimelineContent's Exists/ZRangeArgs can be scripted), returning the
// redismock ClientMock.
func setupMockFeedRedis(t *testing.T) redismock.ClientMock {
	t.Helper()
	setupMockFeedService(t)
	_, rm := setupRedismockRedis(t)
	return rm
}

// direct visibility: a status whose ToAccounts contains the viewer is visible;
// one that does not is filtered out. This is the only way to cover the
// StatusVisibilityDirect case in the production afterFilter closure.
func TestGetTimelineContent_DirectVisibilityAfterFilter(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	gdb := setupTestDB(t)
	rm := setupMockFeedRedis(t)

	tag := &models.Tag{Name: "dm"}
	require.NoError(t, gdb.Create(tag).Error)

	// Viewer = account 30; author = account 10.
	require.NoError(t, gdb.Create(&models.Account{Id: 10}).Error)
	require.NoError(t, gdb.Create(&models.Account{Id: 30}).Error)

	mkDirect := func(toSelf bool) *models.Status {
		s := &models.Status{AccountId: 10, Visibility: models.StatusVisibilityDirect}
		require.NoError(t, gdb.Session(&gorm.Session{SkipHooks: true}).Create(s).Error)
		require.NoError(t, gdb.Exec(
			"INSERT INTO status_tags (status_id, tag_id) VALUES (?, ?)", s.Id, tag.Id,
		).Error)
		if toSelf {
			require.NoError(t, gdb.Create(&models.StatusToAccount{
				StatusId: s.Id, AccountId: 30,
			}).Error)
		}
		return s
	}
	directToViewer := mkDirect(true)
	directToOther := mkDirect(false)

	var ids []string
	for _, s := range []*models.Status{directToViewer, directToOther} {
		ids = append(ids, uintToStr(s.Id))
	}

	timelineKey := "feed:hashtag:dm"
	rm.ExpectExists(timelineKey).SetVal(1)
	rm.ExpectZRangeArgs(redis.ZRangeArgs{
		Key: timelineKey, Start: int64(0), Stop: int64(9), Rev: true,
	}).SetVal(ids)

	viewer := &models.Account{Id: 30}
	got, err := GetTimelineContent("dm", viewer, &feed.ListParam{Limit: 10})
	require.NoError(t, err)
	gotIds := statusIds(got)
	assert.Contains(t, gotIds, directToViewer.Id, "viewer mentioned on direct must see it")
	assert.NotContains(t, gotIds, directToOther.Id, "direct not mentioning viewer must be filtered")

	rm.ExpectationsWereMet()
}

// private visibility: visible to the author themselves and to followers, but
// not to an unrelated logged-in account.
func TestGetTimelineContent_PrivateVisibilityAfterFilter(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	gdb := setupTestDB(t)
	rm := setupMockFeedRedis(t)

	tag := &models.Tag{Name: "priv"}
	require.NoError(t, gdb.Create(tag).Error)

	// author=10, follower=20, stranger=30
	for _, id := range []uint64{10, 20, 30} {
		require.NoError(t, gdb.Create(&models.Account{Id: id}).Error)
	}
	// account 20 follows account 10
	require.NoError(t, gdb.Exec("INSERT INTO follows (account_id, follow_id) VALUES (20, 10)").Error)

	mkPrivate := func(authorId uint64) *models.Status {
		s := &models.Status{AccountId: authorId, Visibility: models.StatusVisibilityPrivate}
		require.NoError(t, gdb.Session(&gorm.Session{SkipHooks: true}).Create(s).Error)
		require.NoError(t, gdb.Exec(
			"INSERT INTO status_tags (status_id, tag_id) VALUES (?, ?)", s.Id, tag.Id,
		).Error)
		return s
	}
	own := mkPrivate(10)          // author=10
	otherPrivate := mkPrivate(30) // author=30 (stranger's private status)

	var ids []string
	for _, s := range []*models.Status{own, otherPrivate} {
		ids = append(ids, uintToStr(s.Id))
	}

	timelineKey := "feed:hashtag:priv"

	// Viewer = author (account 10): sees own private, not stranger's.
	rm.ExpectExists(timelineKey).SetVal(1)
	rm.ExpectZRangeArgs(redis.ZRangeArgs{
		Key: timelineKey, Start: int64(0), Stop: int64(9), Rev: true,
	}).SetVal(ids)
	author := &models.Account{Id: 10}
	got, err := GetTimelineContent("priv", author, &feed.ListParam{Limit: 10})
	require.NoError(t, err)
	gotIds := statusIds(got)
	assert.Contains(t, gotIds, own.Id)
	assert.NotContains(t, gotIds, otherPrivate.Id)
	rm.ExpectationsWereMet()

	// A pending request grants no access, even when its author is present in
	// the same follows table used to build the visibility map.
	require.NoError(t, gdb.Exec("INSERT INTO follows (account_id, follow_id, status) VALUES (40, 10, ?)", models.FollowStatusRequested).Error)
	rm.ExpectExists(timelineKey).SetVal(1)
	rm.ExpectZRangeArgs(redis.ZRangeArgs{Key: timelineKey, Start: int64(0), Stop: int64(9), Rev: true}).SetVal(ids)
	pending, err := GetTimelineContent("priv", &models.Account{Id: 40}, &feed.ListParam{Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, pending, "pending followers cannot see private tagged statuses")
	require.NoError(t, rm.ExpectationsWereMet())

	// Viewer = follower (account 20): sees author's private (following) but not stranger's.
	rm.ExpectExists(timelineKey).SetVal(1)
	rm.ExpectZRangeArgs(redis.ZRangeArgs{
		Key: timelineKey, Start: int64(0), Stop: int64(9), Rev: true,
	}).SetVal(ids)
	follower := &models.Account{Id: 20}
	got, err = GetTimelineContent("priv", follower, &feed.ListParam{Limit: 10})
	require.NoError(t, err)
	gotIds = statusIds(got)
	assert.Contains(t, gotIds, own.Id, "follower must see the private status of the followed author")
	assert.NotContains(t, gotIds, otherPrivate.Id)
	rm.ExpectationsWereMet()
}

// only_media afterFilter passes a status that HAS attachments through.
func TestGetTimelineContent_OnlyMediaKeepsAttachmentStatus(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	gdb := setupTestDB(t)
	rm := setupMockFeedRedis(t)

	tag := &models.Tag{Name: "pics"}
	require.NoError(t, gdb.Create(tag).Error)
	require.NoError(t, gdb.Create(&models.Account{Id: 1}).Error)

	s := &models.Status{AccountId: 1, Visibility: models.StatusVisibilityPublic}
	require.NoError(t, gdb.Session(&gorm.Session{SkipHooks: true}).Create(s).Error)
	require.NoError(t, gdb.Exec(
		"INSERT INTO status_tags (status_id, tag_id) VALUES (?, ?)", s.Id, tag.Id,
	).Error)
	// Attach one attachment via media_attachments.status_id.
	blurHash := "LEHV6nWB2yk8pyo0adR*.7kCMdnj"
	require.NoError(t, gdb.Create(&models.Attachment{
		Id: 1, AccountId: s.AccountId, StatusId: &s.Id, Type: models.MediaTypeImage,
		FileName: "x.png", FilePath: "/x.png", ContentType: "image/png",
		Description: "a photo", Blurhash: blurHash,
	}).Error)

	timelineKey := "feed:hashtag:pics"
	rm.ExpectExists(timelineKey).SetVal(1)
	rm.ExpectZRangeArgs(redis.ZRangeArgs{
		Key: timelineKey, Start: int64(0), Stop: int64(9), Rev: true,
	}).SetVal([]string{uintToStr(s.Id)})

	got, err := GetTimelineContent("pics", nil, &feed.ListParam{Limit: 10, Other: map[string]any{"only_media": true}})
	require.NoError(t, err)
	require.Len(t, got, 1, "status with attachments must survive only_media filter")
	assert.Equal(t, s.Id, got[0].Id)
	rm.ExpectationsWereMet()
}

// ---------------------------------------------------------------------------
// Push — the pushToClient=true path with an actual subscriber channel so that
// pushAllChannel's PushToClient branch is exercised end-to-end.
// ---------------------------------------------------------------------------

func TestPush_PushToClientWithSubscriber(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	mockRedis := setupMockFeedService(t)
	// Subscribe the base tag channel so PushToClient is actually invoked.
	mockRedis.SetChannel("subscribed:feed:hashtag:streamed", true)

	status := &models.Status{
		Id:         777,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Tags:       models.Tags{{Name: "streamed"}},
		Account:    &models.Account{Id: 1},
	}

	require.NoError(t, Push(status, false, true))
	// The status was added to the timeline and (no error from) the client push.
	zset := mockRedis.GetZSet("feed:hashtag:streamed")
	assert.Contains(t, zset, "777")
}

// Reblog status pushed with pushToClient=true and a subscriber exercises both
// the reblog (per-tag AddToTimelineItem) branch and the client-push branch.
func TestPush_ReblogWithSubscriber(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	mockRedis := setupMockFeedService(t)
	mockRedis.SetChannel("subscribed:feed:hashtag:boosted", true)

	reblogId := uint64(9)
	status := &models.Status{
		Id:         888,
		AccountId:  2,
		Visibility: models.StatusVisibilityPublic,
		ReblogId:   &reblogId,
		Tags:       models.Tags{{Name: "boosted"}, {Name: "multi"}},
		Account:    &models.Account{Id: 2},
	}

	require.NoError(t, Push(status, false, true))
	// reblog path records both tags.
	assert.Contains(t, mockRedis.GetZSet("feed:hashtag:boosted"), "888")
	assert.Contains(t, mockRedis.GetZSet("feed:hashtag:multi"), "888")
}

// Unpush with pushToClient=true and a subscriber exercises the delete-channel
// branch through pushAllChannel.
func TestUnpush_PushToClientWithSubscriber(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	mockRedis := setupMockFeedService(t)
	mockRedis.SetChannel("subscribed:feed:hashtag:gone", true)

	status := &models.Status{
		Id:         999,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Tags:       models.Tags{{Name: "gone"}},
		Account:    &models.Account{Id: 1},
	}

	require.NoError(t, Push(status, false, false))
	require.Contains(t, mockRedis.GetZSet("feed:hashtag:gone"), "999")

	// Unpush with pushToClient=true: removes from timeline + pushes "delete".
	require.NoError(t, Unpush(status, true))
	assert.NotContains(t, mockRedis.GetZSet("feed:hashtag:gone"), "999")
}

// ---------------------------------------------------------------------------
// GetTimelineContent — Limit clamps the returned slice.
// ---------------------------------------------------------------------------

func TestGetTimelineContent_LimitClampsResult(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	gdb := setupTestDB(t)
	rm := setupMockFeedRedis(t)

	tag := &models.Tag{Name: "many"}
	require.NoError(t, gdb.Create(tag).Error)
	require.NoError(t, gdb.Create(&models.Account{Id: 1}).Error)

	// Three public statuses.
	var ids []string
	for range 3 {
		s := &models.Status{AccountId: 1, Visibility: models.StatusVisibilityPublic}
		require.NoError(t, gdb.Session(&gorm.Session{SkipHooks: true}).Create(s).Error)
		require.NoError(t, gdb.Exec(
			"INSERT INTO status_tags (status_id, tag_id) VALUES (?, ?)", s.Id, tag.Id,
		).Error)
		ids = append(ids, uintToStr(s.Id))
	}

	timelineKey := "feed:hashtag:many"
	rm.ExpectExists(timelineKey).SetVal(1)
	// Limit=2 -> GetRedisRangeOffsetCount = (0, 1).
	rm.ExpectZRangeArgs(redis.ZRangeArgs{
		Key: timelineKey, Start: int64(0), Stop: int64(1), Rev: true,
	}).SetVal(ids[:2])

	got, err := GetTimelineContent("many", nil, &feed.ListParam{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, got, 2, "limit must clamp the returned slice")
	rm.ExpectationsWereMet()
}

// ---------------------------------------------------------------------------
// getTags — explicitly cover the "already loaded" branch once more with a
// multi-element slice and assert the slice is returned untouched.
// ---------------------------------------------------------------------------

func TestGetTags_PreservesMultiplePreloaded(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	pre := models.Tags{
		{Id: 1, Name: "alpha"},
		{Id: 2, Name: "beta"},
		{Id: 3, Name: "gamma"},
	}
	status := &models.Status{Id: 3, Tags: pre}
	require.NoError(t, getTags(status))
	require.Len(t, status.Tags, 3)
	for i, want := range []string{"alpha", "beta", "gamma"} {
		assert.Equal(t, want, status.Tags[i].Name)
	}
}
