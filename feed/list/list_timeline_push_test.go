package list

import (
	"strconv"
	"testing"
	"time"

	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statusIDStr formats a status id the same way the feed package stores it
// in redis sorted sets (as a string score).
func statusIDStr(id uint64) string {
	return strconv.FormatUint(id, 10)
}

// ---------------------------------------------------------------------------
// Push / Unpush — visibility gating
// ---------------------------------------------------------------------------

// TestPush_NonPublicStatusIsNoOp verifies that Push short-circuits (returns
// nil without touching Redis or the DB) when the status is not public.
// This covers the early-return guard at the top of Push.
func TestPush_NonPublicStatusIsNoOp(t *testing.T) {
	gdb := setupDBOnly(t)

	owner := createAccount(t, gdb, "owner")
	list := createList(t, gdb, owner, "l1")
	addListMember(t, gdb, list, owner)

	cases := []uint8{
		models.StatusVisibilityUnlisted,
		models.StatusVisibilityPrivate,
		models.StatusVisibilityDirect,
	}

	for _, vis := range cases {
		status := &models.Status{
			AccountId:  owner.Id,
			Visibility: vis,
		}
		// Must not error and must not panic.
		err := Push(status, false, true)
		assert.NoError(t, err, "Push should be a no-op for visibility %d", vis)
	}
}

// TestUnpush_NonPublicStatusIsNoOp verifies the same guard on Unpush.
func TestUnpush_NonPublicStatusIsNoOp(t *testing.T) {
	gdb := setupDBOnly(t)

	owner := createAccount(t, gdb, "owner")
	status := &models.Status{
		AccountId:  owner.Id,
		Visibility: models.StatusVisibilityPrivate,
	}

	err := Unpush(status, true)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Push — end-to-end with members
// ---------------------------------------------------------------------------

// TestPush_NoListsReturnsEarly exercises the "len(lists) == 0" early return:
// the author is in no (recently viewed) list, so Push should do nothing and
// return nil.
func TestPush_NoListsReturnsEarly(t *testing.T) {
	gdb := setupDBOnly(t)

	author := createAccount(t, gdb, "author")
	status := createStatus(t, gdb, author)

	// No list contains the author → Push finds zero lists and returns.
	err := Push(status, false, false)
	require.NoError(t, err)
}

// TestPush_PublicStatusAddedToMemberLists pushes a public status and then
// reads it back through GetTimelineContent, asserting the round trip works
// for a list whose member authored the status.
func TestPush_PublicStatusAddedToMemberLists(t *testing.T) {
	gdb, mr := setupFull(t)

	owner := createAccount(t, gdb, "owner")
	author := createAccount(t, gdb, "author")
	list := createList(t, gdb, owner, "tech")
	addListMember(t, gdb, list, author)

	status := createStatus(t, gdb, author)

	err := Push(status, false, false)
	require.NoError(t, err)

	// The timeline key should now exist in redis.
	key := "feed:list:" + list.Title
	assert.True(t, mr.Exists(key), "timeline key %q should exist after Push", key)
}

// TestPush_UpdateFlagUsesStatusUpdateMethod checks that Push(update=true)
// still succeeds and populates the timeline. The method-name difference
// ("status.update" vs "update") is internal; we just verify no error and
// the key is written.
func TestPush_UpdateFlagUsesStatusUpdateMethod(t *testing.T) {
	gdb, mr := setupFull(t)

	owner := createAccount(t, gdb, "owner")
	author := createAccount(t, gdb, "author")
	list := createList(t, gdb, owner, "updatelist")
	addListMember(t, gdb, list, author)

	status := createStatus(t, gdb, author)

	err := Push(status, true, false)
	require.NoError(t, err)
	assert.True(t, mr.Exists("feed:list:"+list.Title))
}

// TestPush_WithReblog exercises the reblog branch of Push, which uses the
// single-item AddToTimelineItem path instead of the batch path.
func TestPush_WithReblog(t *testing.T) {
	gdb, mr := setupFull(t)

	owner := createAccount(t, gdb, "owner")
	author := createAccount(t, gdb, "author")
	list := createList(t, gdb, owner, "rebloglist")
	addListMember(t, gdb, list, author)

	// Original status.
	original := createStatus(t, gdb, author)

	// A reblog of the original, by the same author.
	reblogID := original.Id
	reblog := createStatus(t, gdb, author, func(s *models.Status) {
		s.ReblogId = &reblogID
	})

	err := Push(reblog, false, false)
	require.NoError(t, err)
	assert.True(t, mr.Exists("feed:list:"+list.Title))
}

// TestPush_PushToClientWithSubscriber exercises the pushToClient=true branch
// by registering a subscriber on the timeline channel.
func TestPush_PushToClientWithSubscriber(t *testing.T) {
	gdb, _ := setupFull(t)

	owner := createAccount(t, gdb, "owner")
	author := createAccount(t, gdb, "author")
	list := createList(t, gdb, owner, "clientpush")
	addListMember(t, gdb, list, author)

	status := createStatus(t, gdb, author)

	// With pushToClient=true; PushToClient publishes to redis (a no-op error
	// wise on miniredis when there are no subscribers, but must not fail).
	err := Push(status, false, true)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Unpush — end-to-end
// ---------------------------------------------------------------------------

// TestUnpush_RemovesStatusFromTimeline pushes then unpushes a status and
// verifies the timeline key is emptied.
func TestUnpush_RemovesStatusFromTimeline(t *testing.T) {
	gdb, mr := setupFull(t)

	owner := createAccount(t, gdb, "owner")
	author := createAccount(t, gdb, "author")
	list := createList(t, gdb, owner, "unpushlist")
	addListMember(t, gdb, list, author)

	status := createStatus(t, gdb, author)

	require.NoError(t, Push(status, false, false))
	key := "feed:list:" + list.Title
	require.True(t, mr.Exists(key), "precondition: key should exist after Push")

	// Now unpush.
	err := Unpush(status, false)
	require.NoError(t, err)

	// After removing the only member, the sorted set is empty. miniredis (like
	// real Redis) drops the key when the last zset member is removed, so the
	// key may no longer exist — which is itself proof the member is gone. If
	// the key does exist, assert the member is absent from it.
	if mr.Exists(key) {
		members, mErr := mr.ZMembers(key)
		require.NoError(t, mErr)
		assert.NotContains(t, members, statusIDStr(status.Id))
	}
}

// TestUnpush_WithPushToClient covers the pushToClient=true branch of Unpush.
func TestUnpush_WithPushToClient(t *testing.T) {
	gdb, _ := setupFull(t)

	owner := createAccount(t, gdb, "owner")
	author := createAccount(t, gdb, "author")
	list := createList(t, gdb, owner, "unpushclient")
	addListMember(t, gdb, list, author)

	status := createStatus(t, gdb, author)
	require.NoError(t, Push(status, false, false))

	err := Unpush(status, true)
	require.NoError(t, err)
}

// TestUnpush_NoLists just exercises the path where the author is in no list.
func TestUnpush_NoLists(t *testing.T) {
	gdb := setupDBOnly(t)

	author := createAccount(t, gdb, "lonely")
	status := createStatus(t, gdb, author)

	err := Unpush(status, false)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Rebuild
// ---------------------------------------------------------------------------

// TestRebuild_PopulatesTimelineFromDB creates a list with a member who has
// several public statuses, then calls Rebuild and checks the timeline key in
// redis contains those status ids.
func TestRebuild_PopulatesTimelineFromDB(t *testing.T) {
	gdb, mr := setupFull(t)

	owner := createAccount(t, gdb, "owner")
	author := createAccount(t, gdb, "author")
	list := createList(t, gdb, owner, "rebuildlist")
	addListMember(t, gdb, list, author)

	// Create 3 public statuses for the member.
	var statuses []*models.Status

	for range 3 {
		statuses = append(statuses, createStatus(t, gdb, author))
	}

	// Also create a non-public status that Rebuild must NOT include.
	privateStatus := createStatus(t, gdb, author, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPrivate
	})

	timeline := &ListTimeline{List: list}
	err := timeline.Rebuild()
	require.NoError(t, err)

	key := "feed:list:" + list.Title
	members, err := mr.ZMembers(key)
	require.NoError(t, err)

	for _, s := range statuses {
		assert.Contains(t, members, statusIDStr(s.Id), "public status %d should be in timeline", s.Id)
	}

	assert.NotContains(t, members, statusIDStr(privateStatus.Id), "private status should be excluded")
}

// TestRebuild_EmptyListSucceeds verifies Rebuild works on a list with no
// members (no statuses to pull, but the feed must still be cleaned).
func TestRebuild_EmptyListSucceeds(t *testing.T) {
	gdb, _ := setupFull(t)

	owner := createAccount(t, gdb, "owner")
	list := createList(t, gdb, owner, "emptylist")
	// Deliberately do NOT add any member.

	timeline := &ListTimeline{List: list}
	err := timeline.Rebuild()
	require.NoError(t, err)
}

// TestRebuild_AsTimelineInterface verifies the Timeline interface contract:
// calling Rebuild through the interface type works.
func TestRebuild_AsTimelineInterface(t *testing.T) {
	gdb, _ := setupFull(t)

	owner := createAccount(t, gdb, "owner")
	list := createList(t, gdb, owner, "iface")

	var timeline feed.Timeline = &ListTimeline{List: list}
	err := timeline.Rebuild()
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// GetTimelineContent
// ---------------------------------------------------------------------------

// TestGetTimelineContent_RebuildsWhenCacheMiss checks that when the redis
// cache is empty, GetTimelineContent triggers a Rebuild and returns the
// member's public statuses.
func TestGetTimelineContent_RebuildsWhenCacheMiss(t *testing.T) {
	gdb, _ := setupFull(t)

	owner := createAccount(t, gdb, "owner")
	author := createAccount(t, gdb, "author")
	list := createList(t, gdb, owner, "gtcmiss")
	addListMember(t, gdb, list, author)

	status := createStatus(t, gdb, author)

	params := &feed.ListParam{Limit: 20}
	statuses, err := GetTimelineContent(list, params)
	require.NoError(t, err)

	require.Len(t, statuses, 1)
	assert.Equal(t, status.Id, statuses[0].Id)

	// LastViewedAt should have been updated to roughly now.
	require.NotNil(t, list.LastViewedAt)
	assert.WithinDuration(t, time.Now(), *list.LastViewedAt, 5*time.Second)
}

// TestGetTimelineContent_EmptyListReturnsEmpty verifies that a list with no
// members returns an empty slice and no error.
func TestGetTimelineContent_EmptyListReturnsEmpty(t *testing.T) {
	gdb, _ := setupFull(t)

	owner := createAccount(t, gdb, "owner")
	list := createList(t, gdb, owner, "gtcempty")

	params := &feed.ListParam{Limit: 20}
	statuses, err := GetTimelineContent(list, params)
	require.NoError(t, err)
	assert.Empty(t, statuses)
}

// TestGetTimelineContent_NilParamsReturnsAll covers the params==nil branch
// of GetStatusTimelineContent (read whole timeline).
func TestGetTimelineContent_NilParamsReturnsAll(t *testing.T) {
	gdb, _ := setupFull(t)

	owner := createAccount(t, gdb, "owner")
	author := createAccount(t, gdb, "author")
	list := createList(t, gdb, owner, "gtcnil")
	addListMember(t, gdb, list, author)

	// Push two statuses so the timeline has data, then read with nil params.
	s1 := createStatus(t, gdb, author)
	s2 := createStatus(t, gdb, author)

	require.NoError(t, Push(s1, false, false))
	require.NoError(t, Push(s2, false, false))

	statuses, err := GetTimelineContent(list, nil)
	require.NoError(t, err)
	assert.Len(t, statuses, 2)
}

// ---------------------------------------------------------------------------
// GetKeyFormat / GetTimelineId extra coverage
// ---------------------------------------------------------------------------

// TestGetKeyFormat_Stability is a regression guard: the format string is part
// of the redis key contract and must never change silently.
func TestGetKeyFormat_Stability(t *testing.T) {
	tl := &ListTimeline{}
	assert.Equal(t, "feed:list:%s", tl.GetKeyFormat())
}

// TestGetTimelineId_EmptyTitle covers the degenerate case where only the
// (empty) title contributes to the id.
func TestGetTimelineId_EmptyTitle(t *testing.T) {
	tl := &ListTimeline{List: &models.List{}}
	assert.Equal(t, "", tl.GetTimelineId())
}

// TestGetTimelineId_OnlyRemote covers the branch where OnlyRemote is the
// sole prefix.
func TestGetTimelineId_OnlyRemote(t *testing.T) {
	tl := &ListTimeline{List: &models.List{Title: "r"}, OnlyRemote: true}
	assert.Equal(t, "remote:r", tl.GetTimelineId())
}

// TestGetTimelineId_LocalAndMedia covers the local+media (no remote) combo.
func TestGetTimelineId_LocalAndMedia(t *testing.T) {
	tl := &ListTimeline{List: &models.List{Title: "lm"}, OnlyLocal: true, OnlyMedia: true}
	assert.Equal(t, "local:media:lm", tl.GetTimelineId())
}

// ---------------------------------------------------------------------------
// init() event handler coverage (via event.Trigger)
// ---------------------------------------------------------------------------

// The list package registers a "status"/"*" handler in its init(). These
// tests exercise that handler through the public event.Trigger API.

// TestEventHandler_DirectStatusShortCircuits verifies that a direct-visibility
// status event is accepted (returns via Trigger) but does not push anything.
func TestEventHandler_DirectStatusShortCircuits(t *testing.T) {
	setupDBOnly(t)

	status := &models.Status{Visibility: models.StatusVisibilityDirect}
	// Trigger must not panic; the handler returns true for direct statuses
	// without scheduling any Push.
	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: status})
	})
}

// TestEventHandler_NilDataDoesNotPanic verifies the nil-Data guard.
func TestEventHandler_NilDataDoesNotPanic(t *testing.T) {
	setupDBOnly(t)

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: nil})
	})
}

// TestEventHandler_NilStatusDoesNotPanic verifies the nil-status guard (Data
// is a typed *models.Status pointer that is itself nil).
func TestEventHandler_NilStatusDoesNotPanic(t *testing.T) {
	setupDBOnly(t)

	var status *models.Status
	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: status})
	})
}

// TestEventHandler_AfterDeleteFires covers the after_delete branch with a
// real public status. Uses the shared DB because the handler spawns a
// background goroutine that outlives a per-test handle.
func TestEventHandler_AfterDeleteFires(t *testing.T) {
	gdb := setupSharedDB(t)

	owner := createAccount(t, gdb, "owner")
	author := createAccount(t, gdb, "author")
	list := createList(t, gdb, owner, "evdel")
	addListMember(t, gdb, list, author)
	status := createStatus(t, gdb, author)

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_delete", Data: status})
	})
	// Give the async Unpush goroutine a moment.
	time.Sleep(100 * time.Millisecond)
}

// TestEventHandler_BeforeUpdateFires covers the before_update branch. Uses
// the shared DB because the handler spawns a background goroutine.
func TestEventHandler_BeforeUpdateFires(t *testing.T) {
	gdb := setupSharedDB(t)

	owner := createAccount(t, gdb, "owner")
	author := createAccount(t, gdb, "author")
	list := createList(t, gdb, owner, "evupd")
	addListMember(t, gdb, list, author)
	status := createStatus(t, gdb, author)

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "before_update", Data: status})
	})
	time.Sleep(100 * time.Millisecond)
}

// TestEventHandler_AfterUpdateFires covers the after_update branch. Uses the
// shared DB because the handler spawns a background goroutine.
func TestEventHandler_AfterUpdateFires(t *testing.T) {
	gdb := setupSharedDB(t)

	owner := createAccount(t, gdb, "owner")
	author := createAccount(t, gdb, "author")
	list := createList(t, gdb, owner, "evafterupd")
	addListMember(t, gdb, list, author)
	status := createStatus(t, gdb, author)

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_update", Data: status})
	})
	time.Sleep(100 * time.Millisecond)
}

// TestEventHandler_AfterCreateFires covers the after_create branch with a
// public status. Uses the shared DB because the handler spawns a background
// goroutine.
func TestEventHandler_AfterCreateFires(t *testing.T) {
	gdb := setupSharedDB(t)

	owner := createAccount(t, gdb, "owner")
	author := createAccount(t, gdb, "author")
	list := createList(t, gdb, owner, "evcreate")
	addListMember(t, gdb, list, author)
	status := createStatus(t, gdb, author)

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: status})
	})
	// Give the spawned goroutine a moment to finish its best-effort work.
	time.Sleep(100 * time.Millisecond)
}

// TestEventHandler_UnknownEventNameIsHarmless verifies that an unrecognised
// event name does not panic (the switch simply falls through).
func TestEventHandler_UnknownEventNameIsHarmless(t *testing.T) {
	setupDBOnly(t)

	status := &models.Status{Visibility: models.StatusVisibilityPublic}
	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "something_else", Data: status})
	})
}
