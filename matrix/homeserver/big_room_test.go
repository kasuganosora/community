package homeserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---- /messages on a room with many events -------------------------------------

// seedStubRoom stores `stubs` events with a non-empty (unparseable) federation
// PDU and no snapshot - what a partial-state join leaves behind: their history
// visibility cannot be proven, so they are never visible - newer than `visible`
// ordinary events that are.
func seedStubRoom(t *testing.T, roomID, userID string, stubs, visible int) string {
	t.Helper()
	conn := db.GetDB()
	tag := strings.NewReplacer("!", "", ":", "", ".", "").Replace(roomID)
	require.NoError(t, conn.Create(&MatrixRoomMember{RoomID: roomID, UserID: userID, Membership: MembershipJoin}).Error)
	require.NoError(t, conn.Create(&MatrixRoom{RoomID: roomID, RoomVersion: "10"}).Error)
	write := conn.Session(&gorm.Session{SkipHooks: true})
	rows := make([]MatrixEvent, 0, stubs+visible)
	for i := 0; i < visible; i++ {
		rows = append(rows, MatrixEvent{EventID: fmt.Sprintf("$%s-visible%04d", tag, i), RoomID: roomID, EventType: EventTypeRoomMessage, Sender: userID, OriginServerTS: int64(1000 + i), StateSnapshotKnown: true, Content: map[string]any{"body": i}})
	}
	for i := 0; i < stubs; i++ {
		key := fmt.Sprintf("@stub%05d:remote.example", i)
		rows = append(rows, MatrixEvent{EventID: fmt.Sprintf("$%s-stub%05d", tag, i), RoomID: roomID, EventType: EventTypeRoomMember, StateKey: &key, Sender: key, OriginServerTS: int64(100000 + i), FederationJSON: "{not a pdu", Content: map[string]any{"membership": "join"}})
	}
	require.NoError(t, write.CreateInBatches(rows, 200).Error)
	return tag
}

func countQueries(t *testing.T) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	conn := db.GetDB()
	name := fmt.Sprintf("test:count_%d", time.Now().UnixNano())
	require.NoError(t, conn.Callback().Query().After("gorm:query").Register(name, func(*gorm.DB) { n.Add(1) }))
	require.NoError(t, conn.Callback().Row().After("gorm:row").Register(name+"_row", func(*gorm.DB) { n.Add(1) }))
	return &n
}

func TestMessagesOnBigRoomScanIsBoundedAndPaginationContinues(t *testing.T) {
	t.Setenv("MATRIX_HISTORY_SCAN_MAX", "200")
	hs := createTestHomeServer(t)
	roomID, userID := "!bigroom:test.local", "@reader:test.local"
	tag := seedStubRoom(t, roomID, userID, 3000, 40)
	queries := countQueries(t)

	started := time.Now()
	page, merr := hs.Event.GetRoomMessages(roomID, userID, &GetRoomMessagesRequest{Dir: "b", Limit: 5})
	require.Nil(t, merr)
	t.Logf("first page: %d events, end=%q, %d queries in %s", len(page.Chunk), page.End, queries.Load(), time.Since(started))
	require.Empty(t, page.Chunk, "the newest 200 candidates are all unprovable stubs")
	require.NotEmpty(t, page.End, "a continuation token must be returned so the client can keep paginating")
	// 5 + pages of 64 up to 200 candidates is a handful of queries; walking the
	// room would take 3000/64 = 47 pages.
	require.LessOrEqual(t, queries.Load(), int64(20), "scan must stop after the configured number of candidates")

	// Following the tokens reaches the 40 visible events, newest first, once each.
	seen := []string{}
	from := page.End
	for i := 0; i < 40 && len(seen) < 40; i++ {
		next, merr := hs.Event.GetRoomMessages(roomID, userID, &GetRoomMessagesRequest{Dir: "b", Limit: 5, From: from})
		require.Nil(t, merr)
		for _, ev := range next.Chunk {
			seen = append(seen, ev.EventID)
		}
		require.NotEmpty(t, next.End)
		require.NotEqual(t, from, next.End, "the token must advance")
		from = next.End
	}
	require.Len(t, seen, 40)
	for i, id := range seen {
		require.Equal(t, fmt.Sprintf("$%s-visible%04d", tag, 39-i), id, "descending timeline order")
	}
}

func TestMessagesLimitedPageWhenRecentEventsAreVisible(t *testing.T) {
	hs := createTestHomeServer(t)
	roomID, userID := "!bigroom2:test.local", "@reader:test.local"
	tag := seedStubRoom(t, roomID, userID, 0, 500)
	queries := countQueries(t)
	page, merr := hs.Event.GetRoomMessages(roomID, userID, &GetRoomMessagesRequest{Dir: "b", Limit: 5})
	require.Nil(t, merr)
	require.Len(t, page.Chunk, 5)
	require.Equal(t, "$"+tag+"-visible0499", page.Chunk[0].EventID)
	require.Equal(t, "$"+tag+"-visible0495", page.Chunk[4].EventID)
	require.LessOrEqual(t, queries.Load(), int64(8), "a limit-5 page must not load the room")
	forward, merr := hs.Event.GetRoomMessages(roomID, userID, &GetRoomMessagesRequest{Dir: "f", Limit: 5, From: hs.Event.generatePaginationTokenForEvent(1000, "$"+tag+"-visible0000")})
	require.Nil(t, merr)
	require.Len(t, forward.Chunk, 5)
	require.Equal(t, "$"+tag+"-visible0001", forward.Chunk[0].EventID)
	// The end token resumes exactly after the last returned event.
	next, merr := hs.Event.GetRoomMessages(roomID, userID, &GetRoomMessagesRequest{Dir: "b", Limit: 5, From: page.End})
	require.Nil(t, merr)
	require.Equal(t, "$"+tag+"-visible0494", next.Chunk[0].EventID)
}

func TestMatrixEventsTimelineIndexExistsAndServesMessagesOrder(t *testing.T) {
	createTestHomeServer(t)
	conn := db.GetDB()
	require.True(t, conn.Migrator().HasIndex(&MatrixEvent{}, "idx_matrix_events_room_ts"))
	require.True(t, conn.Migrator().HasIndex(&MatrixEvent{}, "idx_matrix_events_redacts"))
	require.True(t, conn.Migrator().HasIndex(&MatrixEvent{}, "idx_matrix_events_room_state"))
	require.True(t, conn.Migrator().HasIndex(&MatrixRoomState{}, "idx_matrix_room_state_key"))
	if conn.Dialector.Name() != "sqlite" {
		t.Skip("EXPLAIN QUERY PLAN assertion is SQLite specific; PostgreSQL is verified by hand (see report)")
	}
	var plan []struct {
		ID, Parent, Notused int
		Detail              string
	}
	require.NoError(t, conn.Raw("EXPLAIN QUERY PLAN SELECT * FROM matrix_events WHERE room_id = ? AND (origin_server_ts < ? OR (origin_server_ts = ? AND event_id < ?)) ORDER BY origin_server_ts DESC, event_id DESC LIMIT 5", "!r:x", 5, 5, "$e").Scan(&plan).Error)
	details := []string{}
	for _, row := range plan {
		details = append(details, row.Detail)
	}
	joined := strings.Join(details, " | ")
	require.Contains(t, joined, "idx_matrix_events_room_ts")
	require.NotContains(t, joined, "TEMP B-TREE", "the order must come from the index, not a sort")
}

// ---- key cache -----------------------------------------------------------------

func TestKeyCacheSetNeverBlocksOnPersistenceAndSurvivesCancelledContext(t *testing.T) {
	setupKeyCacheTestDB(t)
	kc := NewKeyCache()
	held, err := kc.lockDB(context.Background()) // persistence is stuck
	require.NoError(t, err)
	pub, _ := ed25519Key()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	for i := 0; i < 200; i++ {
		kc.setContext(cancelled, fmt.Sprintf("origin%d.example", i), "ed25519:k", pub, time.Now().Add(time.Hour), 0)
	}
	require.Less(t, time.Since(started), time.Second, "Set must not wait for the database")
	for i := 0; i < 200; i++ {
		_, ok := kc.Get(fmt.Sprintf("origin%d.example", i), "ed25519:k")
		require.True(t, ok, "a key whose context was cancelled is still cached in memory")
	}
	held()
	require.NoError(t, kc.WaitSaved(context.Background()))
	var n int64
	require.NoError(t, db.GetDB().Model(&MatrixServerKey{}).Where("key_id = ?", "ed25519:k").Count(&n).Error)
	require.Equal(t, int64(200), n, "all keys are persisted once the database is free")
}

func TestKeyCacheDedupesBatchesAndWarnsOncePerInterval(t *testing.T) {
	setupKeyCacheTestDB(t)
	oldInterval := keySaveWarnInterval
	keySaveWarnInterval = time.Hour
	t.Cleanup(func() { keySaveWarnInterval = oldInterval })
	kc := NewKeyCache()
	pub, _ := ed25519Key()
	// Dedupe: the same key set many times is written once.
	held, _ := kc.lockDB(context.Background())
	for i := 0; i < 50; i++ {
		kc.Set("dedupe.example", "ed25519:k", pub, time.Now().Add(time.Hour))
	}
	kc.saveMu.Lock()
	require.Len(t, kc.pending, 1)
	kc.saveMu.Unlock()
	held()
	require.NoError(t, kc.WaitSaved(context.Background()))

	// A failing database: failures are counted and logged once per interval.
	require.NoError(t, db.GetDB().Migrator().DropTable(&MatrixServerKey{}))
	t.Cleanup(func() { _ = db.GetDB().AutoMigrate(&MatrixServerKey{}) })
	logs := captureLogs(t)
	for round := 0; round < 3; round++ {
		for i := 0; i < 5; i++ {
			kc.Set(fmt.Sprintf("fail%d-%d.example", round, i), "ed25519:k", pub, time.Now().Add(time.Hour))
		}
		require.NoError(t, kc.WaitSaved(context.Background()))
	}
	text := logs.text()
	require.Equal(t, 1, strings.Count(text, "Failed to cache"), "one warning per interval, not per key:\n%s", text)
	require.NotContains(t, text, "level=error")
	kc.warn.mu.Lock()
	suppressed := kc.warn.suppressed
	kc.warn.mu.Unlock()
	require.Equal(t, 10, suppressed, "later failures within the interval are only counted")
	_, ok := kc.Get("fail2-4.example", "ed25519:k")
	require.True(t, ok, "keys stay usable from memory when persistence fails")
}

// ---- bulk key prefetch ---------------------------------------------------------

func prefetchEnv(t *testing.T, bulk bool, origins int) (*HomeServer, *fakeFed, map[string]gmsl.PDU, []*fakeFed) {
	t.Helper()
	notary := newFakeFed(t)
	notary.bulkNotary = bulk
	hs := joinTestEnv(t, notary)
	defaultKeyNotaries = []string{notary.name}
	room := newFakeRoom(t, notary, notary)
	var all []*fakeFed
	events := map[string]gmsl.PDU{}
	for i := 0; i < origins; i++ {
		o := newFakeFed(t)
		o.noKeyDoc = true // unreachable directly: only the notary can vouch
		notary.notaryDocs[o.name] = o
		all = append(all, o)
		u := fmt.Sprintf("@u%d:%s", i, o.name)
		p := room.add(u, "m.room.member", new(u), map[string]any{"membership": "join"}, o)
		events[p.EventID()] = p
	}
	return hs, notary, events, all
}

func TestPrefetchResolvesUnreachableOriginsWithOneBulkNotaryQuery(t *testing.T) {
	hs, notary, events, origins := prefetchEnv(t, true, 12)
	stats := hs.Room.prefetchSigningKeys(context.Background(), events, true)
	t.Log(stats.String())
	require.Equal(t, 12, stats.servers)
	require.Equal(t, 12, stats.notary)
	require.Zero(t, stats.failed)
	require.Equal(t, 1, notary.count("bulk_query"), "twelve dead origins cost one POST, not twelve 8s round trips")
	require.Zero(t, notary.count("query"))
	for _, o := range origins {
		_, ok := GetKeyCache().Get(o.name, o.hs.keyID)
		require.True(t, ok, o.name)
	}
}

func TestPrefetchFallsBackToPerServerNotaryQueriesWhenBulkUnsupported(t *testing.T) {
	hs, notary, events, _ := prefetchEnv(t, false, 6)
	stats := hs.Room.prefetchSigningKeys(context.Background(), events, true)
	require.Equal(t, 6, stats.notary)
	require.Zero(t, stats.failed)
	require.Equal(t, 1, notary.count("bulk_query"))
	require.GreaterOrEqual(t, notary.count("query"), 6)
}

func TestTopOriginsSummary(t *testing.T) {
	counts := map[string]int{"a.example": 3, "b.example": 10, "c.example": 10}
	for i := 0; i < 12; i++ {
		counts[fmt.Sprintf("x%d.example", i)] = 1
	}
	out := topOrigins(counts, 3)
	require.True(t, strings.HasPrefix(out, "b.example=10, c.example=10, a.example=3"), out)
	require.Contains(t, out, "more origins")
	require.Equal(t, "none", topOrigins(nil, 5))
}

// ---- throttling ------------------------------------------------------------------

func TestCPUThrottleOnlyAffectsBackgroundContexts(t *testing.T) {
	t.Setenv("MATRIX_JOIN_BG_CPU_DUTY_PERCENT", "50")
	spin := func(ctx context.Context) time.Duration {
		start := time.Now()
		for i := 0; i < 5; i++ {
			until := time.Now().Add(25 * time.Millisecond)
			for time.Now().Before(until) {
			}
			throttleTick(ctx)
		}
		return time.Since(start)
	}
	plain := context.Background()
	require.Less(t, spin(plain), 150*time.Millisecond, "no throttle without a throttled context")
	// 125 ms of work at 50% duty adds about as much sleep.
	require.GreaterOrEqual(t, spin(withCPUThrottle(plain)), 200*time.Millisecond)
	t.Setenv("MATRIX_JOIN_BG_CPU_DUTY_PERCENT", "100")
	require.Equal(t, plain, withCPUThrottle(plain), "duty 100 disables throttling")
}

// ---- leave -----------------------------------------------------------------------

func leaveEnv(t *testing.T, members int, omit bool) (*HomeServer, *fakeFed, *fakeRoom, func()) {
	t.Helper()
	hs, live, room, _ := asyncJoinEnv(t, members)
	withOmit(omit)
	if omit {
		room.serveOmit(live, []string{live.name})
	} else {
		asyncJoinSettingsSource = func() asyncJoinSettings { s := loadAsyncJoinSettings(); s.SyncThreshold = 100000; return s }
	}
	live.leaveTemplate = func(u string) any {
		return room.builder(u, "m.room.member", &u, map[string]any{"membership": "leave"})
	}
	var once sync.Once
	release := make(chan struct{})
	if omit {
		base := fullStateResp(t, live, room)
		live.stateResp = func() any { <-release; return base() }
	}
	closeRelease := func() { once.Do(func() { close(release) }) }
	t.Cleanup(closeRelease)
	return hs, live, room, closeRelease
}

func memberState(t *testing.T, roomID, user string) MembershipState {
	t.Helper()
	var m MatrixRoomMember
	require.NoError(t, db.GetDB().Where("room_id = ? AND user_id = ?", roomID, user).First(&m).Error)
	return m.Membership
}

func sentLeave(t *testing.T, f *fakeFed) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Len(t, f.leaveEvents, 1)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(f.leaveEvents[0], &wire))
	return wire
}

func TestLeavePartialStateRoomUsesMakeLeaveSendLeaveAndCancelsStateSync(t *testing.T) {
	hs, live, room, release := leaveEnv(t, 40, true)
	joinAlice(t, hs, room, live)
	require.True(t, partialFlag(room.roomID))
	asyncJoins.mu.Lock()
	require.True(t, asyncJoins.running[room.roomID], "background state sync is running (its /state fetch is held)")
	asyncJoins.mu.Unlock()

	merr := hs.Room.LeaveRoomContext(context.Background(), "@alice:test.local", room.roomID, &LeaveRoomRequest{Reason: "bye"})
	require.Nil(t, merr)

	require.Equal(t, 1, live.count("make_leave"))
	require.Equal(t, 1, live.count("send_leave"))
	wire := sentLeave(t, live)
	require.Equal(t, "@alice:test.local", wire["sender"])
	require.Equal(t, "@alice:test.local", wire["state_key"])
	require.Equal(t, "leave", wire["content"].(map[string]any)["membership"])
	require.Equal(t, "bye", wire["content"].(map[string]any)["reason"])
	require.Contains(t, wire["signatures"], "test.local")
	require.NotEmpty(t, wire["prev_events"], "prev_events come from the resident server's template")

	require.Equal(t, MembershipLeave, memberState(t, room.roomID, "@alice:test.local"))
	require.False(t, partialFlag(room.roomID), "the partial marker is cleaned up")
	require.Empty(t, partialRoomServerHints(room.roomID, 10))
	var member MatrixRoomMember
	require.NoError(t, db.GetDB().Where("room_id = ? AND user_id = ?", room.roomID, "@alice:test.local").First(&member).Error)
	var stored MatrixEvent
	require.NoError(t, db.GetDB().Where("event_id = ?", member.EventID).First(&stored).Error)
	require.NotEmpty(t, stored.FederationJSON)
	require.Equal(t, "leave", stored.Content["membership"])

	// The job was cancelled by the leave: it is gone although its /state request
	// is still being held by the fake server.
	asyncJoins.mu.Lock()
	running := asyncJoins.running[room.roomID]
	asyncJoins.mu.Unlock()
	require.False(t, running, "state sync job must be cancelled by the leave")
	release()
	done := make(chan struct{})
	go func() { asyncJoins.idle(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("background job still running after leave")
	}
	require.Equal(t, MembershipLeave, memberState(t, room.roomID, "@alice:test.local"), "a late job must not undo the leave")
}

func TestLeaveNormalRemoteRoomUsesFederation(t *testing.T) {
	hs, live, room, _ := leaveEnv(t, 10, false)
	joinAlice(t, hs, room, live)
	asyncJoins.idle()
	require.False(t, partialFlag(room.roomID))

	merr := hs.Room.LeaveRoomContext(context.Background(), "@alice:test.local", room.roomID, &LeaveRoomRequest{})
	require.Nil(t, merr)
	require.Equal(t, 1, live.count("make_leave"))
	require.Equal(t, 1, live.count("send_leave"))
	wire := sentLeave(t, live)
	require.Equal(t, "leave", wire["content"].(map[string]any)["membership"])
	require.Equal(t, MembershipLeave, memberState(t, room.roomID, "@alice:test.local"))
	// Leaving twice is idempotent and does not call out again.
	require.Nil(t, hs.Room.LeaveRoomContext(context.Background(), "@alice:test.local", room.roomID, &LeaveRoomRequest{}))
	require.Equal(t, 1, live.count("send_leave"))
}

func TestLeaveReturnsMatrixErrorWhenNoRemoteServerAcceptsPartialRoom(t *testing.T) {
	hs, live, room, release := leaveEnv(t, 20, true)
	joinAlice(t, hs, room, live)
	require.True(t, partialFlag(room.roomID))
	live.sendLeaveStatus, live.sendLeaveBody = 502, "bad gateway"
	merr := hs.Room.LeaveRoomContext(context.Background(), "@alice:test.local", room.roomID, &LeaveRoomRequest{})
	require.NotNil(t, merr)
	require.Equal(t, "M_BAD_GATEWAY", merr.ErrCode, "a remote failure is not a generic M_INTERNAL")
	require.Equal(t, 502, matrixErrorStatus(merr))
	require.Equal(t, MembershipJoin, memberState(t, room.roomID, "@alice:test.local"))
	require.True(t, partialFlag(room.roomID), "a failed leave leaves the room as it was")
	release()
	asyncJoins.idle()
}

func TestLeaveFallsBackToLocalDepartureWhenRemoteRefusesOrIsDown(t *testing.T) {
	hs, live, room, _ := leaveEnv(t, 10, false)
	joinAlice(t, hs, room, live)
	asyncJoins.idle()
	// The remote does not know us any more: M_FORBIDDEN means "not in the room".
	live.sendLeaveStatus, live.sendLeaveBody = 403, `{"errcode":"M_FORBIDDEN","error":"not in room"}`
	require.Nil(t, hs.Room.LeaveRoomContext(context.Background(), "@alice:test.local", room.roomID, &LeaveRoomRequest{}))
	require.Equal(t, MembershipLeave, memberState(t, room.roomID, "@alice:test.local"))
}

func TestLocalLeaveEventReferencesAtMostTwentyPrevEventsInAStubbedDAG(t *testing.T) {
	hs, live, room, _ := leaveEnv(t, 10, false)
	joinAlice(t, hs, room, live)
	asyncJoins.idle()
	// A partial-state DAG: hundreds of stored events nobody references.
	write := db.GetDB().Session(&gorm.Session{SkipHooks: true})
	base := time.Now().UnixMilli()
	for i := 0; i < 300; i++ {
		require.NoError(t, write.Create(&MatrixEvent{EventID: fmt.Sprintf("$stubleaf%03d", i), RoomID: room.roomID, EventType: "m.room.message", Sender: "@owner:" + live.name, Depth: int64(1000 + i), OriginServerTS: base + int64(i), Content: map[string]any{"i": i}}).Error)
	}
	live.kill() // forces the local build path
	queries := countQueries(t)
	require.Nil(t, hs.Room.LeaveRoomContext(context.Background(), "@alice:test.local", room.roomID, &LeaveRoomRequest{}))
	t.Logf("local leave in a stubbed DAG: %d queries", queries.Load())
	require.Less(t, queries.Load(), int64(60), "building a leave must not load every state event or event of the room")
	var member MatrixRoomMember
	require.NoError(t, db.GetDB().Where("room_id = ? AND user_id = ?", room.roomID, "@alice:test.local").First(&member).Error)
	require.Equal(t, MembershipLeave, member.Membership)
	var ev MatrixEvent
	require.NoError(t, db.GetDB().Where("event_id = ?", member.EventID).First(&ev).Error)
	require.NotEmpty(t, ev.FederationJSON, "the leave is a signed PDU")
	require.LessOrEqual(t, len(ev.PrevEvents), 20)
	require.NotEmpty(t, ev.PrevEvents)
}

// ---- members / state / context on a room with thousands of members -------------

func seedManyMembers(t *testing.T, roomID, viewer string, members int) {
	t.Helper()
	conn := db.GetDB()
	require.NoError(t, conn.Create(&MatrixRoom{RoomID: roomID, RoomVersion: "10"}).Error)
	write := conn.Session(&gorm.Session{SkipHooks: true})
	tag := strings.NewReplacer("!", "", ":", "", ".", "").Replace(roomID)
	events := make([]MatrixEvent, 0, members+1)
	states := make([]MatrixRoomState, 0, members+1)
	rows := make([]MatrixRoomMember, 0, members+1)
	for i := 0; i <= members; i++ {
		user := fmt.Sprintf("@m%05d:remote.example", i)
		if i == 0 {
			user = viewer
		}
		id := fmt.Sprintf("$%s-member%05d", tag, i)
		content := map[string]any{"membership": "join"}
		events = append(events, MatrixEvent{EventID: id, RoomID: roomID, EventType: EventTypeRoomMember, StateKey: &user, Sender: user, OriginServerTS: int64(1000 + i), StateSnapshotKnown: true, Content: content})
		states = append(states, MatrixRoomState{RoomID: roomID, EventType: EventTypeRoomMember, StateKey: user, EventID: id, Content: content})
		rows = append(rows, MatrixRoomMember{RoomID: roomID, UserID: user, Membership: MembershipJoin, EventID: id})
	}
	empty := ""
	name := fmt.Sprintf("$%s-name", tag)
	events = append(events, MatrixEvent{EventID: name, RoomID: roomID, EventType: EventTypeRoomName, StateKey: &empty, Sender: viewer, OriginServerTS: 50, StateSnapshotKnown: true, Content: map[string]any{"name": "Big"}})
	states = append(states, MatrixRoomState{RoomID: roomID, EventType: EventTypeRoomName, StateKey: "", EventID: name, Content: map[string]any{"name": "Big"}})
	for i := 0; i < 20; i++ {
		events = append(events, MatrixEvent{EventID: fmt.Sprintf("$%s-msg%02d", tag, i), RoomID: roomID, EventType: EventTypeRoomMessage, Sender: fmt.Sprintf("@m%05d:remote.example", i+1), OriginServerTS: int64(900000 + i), StateSnapshotKnown: true, Content: map[string]any{"body": i}})
	}
	require.NoError(t, write.CreateInBatches(events, 300).Error)
	require.NoError(t, write.CreateInBatches(states, 300).Error)
	require.NoError(t, write.CreateInBatches(rows, 300).Error)
}

func TestMembersJoinedMembersAndStateUseBoundedQueriesOnLargeRoom(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hs := createTestHomeServer(t)
	restore := resetGlobalHS(hs)
	defer restore()
	roomID, viewer := "!manymembers:test.local", "@viewer:test.local"
	seedManyMembers(t, roomID, viewer, 2500)
	queries := countQueries(t)

	serve := func(handler gin.HandlerFunc, path string) *httptest.ResponseRecorder {
		r := gin.New()
		r.GET("/rooms/:roomId/*rest", func(c *gin.Context) { c.Set("user_id", viewer); handler(c) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	for name, tc := range map[string]struct {
		h    gin.HandlerFunc
		path string
		want int
	}{
		"members":        {handleGetRoomMembers, "/rooms/%21manymembers%3Atest.local/members", 2501},
		"joined_members": {handleGetJoinedMembers, "/rooms/%21manymembers%3Atest.local/joined_members", 2501},
		"state":          {handleGetRoomState, "/rooms/%21manymembers%3Atest.local/state", 2502},
	} {
		before := queries.Load()
		w := serve(tc.h, tc.path)
		require.Equal(t, http.StatusOK, w.Code, name)
		n := strings.Count(w.Body.String(), "\"event_id\"")
		if name == "joined_members" {
			n = strings.Count(w.Body.String(), "display_name")
		}
		require.Equal(t, tc.want, n, name)
		used := queries.Load() - before
		t.Logf("%s on 2500 members: %d queries", name, used)
		require.LessOrEqual(t, used, int64(12), "%s must not issue a query per member", name)
	}
}

func TestContextAndStateLazyLoadMembersOnLargeRoom(t *testing.T) {
	hs := createTestHomeServer(t)
	roomID, viewer := "!lazylarge:test.local", "@viewer:test.local"
	seedManyMembers(t, roomID, viewer, 1500)
	tag := strings.NewReplacer("!", "", ":", "", ".", "").Replace(roomID)

	full, merr := hs.Room.GetRoomStateAllContext(context.Background(), roomID)
	require.Nil(t, merr)
	require.Len(t, full, 1502)
	lazy, merr := hs.Room.GetRoomStateLazyContext(context.Background(), roomID, map[string]bool{"@m00003:remote.example": true, viewer: true})
	require.Nil(t, merr)
	require.Len(t, lazy, 3, "name + the two requested members only")

	queries := countQueries(t)
	ctxResp, merr := hs.Event.GetRoomContextLazyContext(context.Background(), roomID, "$"+tag+"-msg10", viewer, 5, true)
	require.Nil(t, merr)
	require.Len(t, ctxResp.EventsBefore, 5)
	require.Len(t, ctxResp.EventsAfter, 5)
	senders := map[string]bool{ctxResp.Event.Sender: true}
	for _, e := range append(append([]*ClientEvent{}, ctxResp.EventsBefore...), ctxResp.EventsAfter...) {
		senders[e.Sender] = true
	}
	members := 0
	for _, st := range ctxResp.State {
		if st.Type == string(EventTypeRoomMember) {
			members++
			require.True(t, senders[*st.StateKey], "only senders of the returned events")
		}
	}
	require.Equal(t, len(senders), members)
	require.LessOrEqual(t, queries.Load(), int64(30))
	// Without the filter the full member list is still returned (spec default).
	all, merr := hs.Event.GetRoomContextContext(context.Background(), roomID, "$"+tag+"-msg10", viewer, 5)
	require.Nil(t, merr)
	require.Len(t, all.State, 1502)

	// A departed user's /state is one windowed query instead of one per entry.
	got := getFullStateAtTime(roomID, 1<<40)
	require.Len(t, got, 1502)
}

func TestInitialSyncLazyLoadMembersAndSummaryOnLargeRoom(t *testing.T) {
	hs := createTestHomeServer(t)
	roomID, viewer := "!synclarge:test.local", "@viewer:test.local"
	seedManyMembers(t, roomID, viewer, 1200)
	filter := `{"room":{"state":{"lazy_load_members":true},"timeline":{"limit":5}}}`
	queries := countQueries(t)
	resp, merr := hs.Sync.SyncContext(context.Background(), viewer, "DEV", &SyncRequest{Filter: filter})
	require.Nil(t, merr)
	room := resp.Rooms.Join[roomID]
	require.NotNil(t, room)
	require.Len(t, room.Timeline.Events, 5)
	members := 0
	senders := map[string]bool{viewer: true}
	for _, e := range room.Timeline.Events {
		senders[e.Sender] = true
	}
	for _, st := range room.State.Events {
		if st.Type == string(EventTypeRoomMember) {
			members++
		}
	}
	require.LessOrEqual(t, members, len(senders)+5+1, "viewer + timeline senders + heroes, not 1200")
	require.Less(t, members, 50)
	require.Equal(t, int64(1201), room.Summary.JoinedMemberCount)
	require.LessOrEqual(t, len(room.Summary.Heroes), 5)
	t.Logf("lazy initial sync of 1200 members: %d member state events, %d queries", members, queries.Load())

	// Without the filter the whole state is returned, as the spec requires.
	full, merr := hs.Sync.SyncContext(context.Background(), viewer, "DEV2", &SyncRequest{})
	require.Nil(t, merr)
	require.Greater(t, len(full.Rooms.Join[roomID].State.Events), 1200)
}
