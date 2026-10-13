package homeserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/require"
)

// asyncJoinEnv prepares a fake room with `members` extra joined users, a join
// environment that always defers the remainder to the background, and
// cleanup of the background jobs.
func asyncJoinEnv(t *testing.T, members int) (*HomeServer, *fakeFed, *fakeRoom, []string) {
	t.Helper()
	live := newFakeFed(t)
	hs := joinTestEnv(t, live)
	room := newFakeRoom(t, live, live)
	room.add("@owner:"+live.name, "m.room.name", new(""), map[string]any{"name": "Big room"}, live)
	var users []string
	for i := 0; i < members; i++ {
		u := fmt.Sprintf("@member%03d:%s", i, live.name)
		users = append(users, u)
		room.add(u, "m.room.member", new(u), map[string]any{"membership": "join"}, live)
	}
	room.serve(live)
	oldSettings, oldHook := asyncJoinSettingsSource, joinRemainderBatchHook
	asyncJoinSettingsSource = func() asyncJoinSettings {
		s := loadAsyncJoinSettings()
		s.SyncThreshold = 0
		s.BatchEvents = 10
		s.RetryBase = 10 * time.Millisecond
		return s
	}
	t.Cleanup(func() {
		asyncJoins.shutdown()
		asyncJoinSettingsSource, joinRemainderBatchHook = oldSettings, oldHook
		takePartialPDUs(room.roomID)
	})
	return hs, live, room, users
}

// joinTestGate releases blocked fake federation work even when a require
// assertion aborts the test before the normal release point. Register this
// after asyncJoinEnv so its cleanup runs before asyncJoins.shutdown.
func joinTestGate(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	return release, unblock
}

func countMembers(t *testing.T, roomID string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.GetDB().Model(&MatrixRoomMember{}).Where("room_id = ?", roomID).Count(&n).Error)
	return n
}

func countRows(t *testing.T, model any, roomID string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.GetDB().Model(model).Where("room_id = ?", roomID).Count(&n).Error)
	return n
}

func partialFlag(roomID string) bool {
	_, _, partial := roomPartialState(db.GetDB(), roomID)
	return partial
}

func joinAlice(t *testing.T, hs *HomeServer, room *fakeRoom, live *fakeFed) {
	t.Helper()
	_, merr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{live.name}})
	require.Nil(t, merr)
}

func partialFedRouter() *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("origin", "test.local"); c.Next() })
	router.GET("/state/:roomId", handleGetState)
	router.GET("/state_ids/:roomId", handleGetStateIDs)
	router.GET("/event/:eventId", handleGetFederationEvent)
	router.GET("/make_join/:roomId/:userId", handleMakeJoin)
	router.PUT("/send_join/:roomId/:eventId", handleSendJoin)
	router.GET("/make_leave/:roomId/:userId", handleMakeLeave)
	router.PUT("/send_leave/:roomId/:eventId", handleSendLeave)
	router.PUT("/send/:txnId", handleFederationSend)
	return router
}

func TestAsyncJoinStoresEssentialSynchronouslyAndRestInBackground(t *testing.T) {
	hs, live, room, users := asyncJoinEnv(t, 60)
	release, unblock := joinTestGate(t)
	var batches atomic.Int64
	joinRemainderBatchHook = func() error { batches.Add(1); <-release; return nil }

	joinAlice(t, hs, room, live)

	// Stage 1 is visible immediately, the rest is not.
	require.True(t, joined(t, room.roomID, "@alice:test.local"))
	require.True(t, partialFlag(room.roomID), "room must be flagged partial while the rest is stored")
	name, merr := hs.Room.GetRoomState(room.roomID, "m.room.name", "")
	require.Nil(t, merr)
	require.Equal(t, "Big room", name.(map[string]any)["name"])
	rule, merr := hs.Room.GetRoomState(room.roomID, "m.room.join_rules", "")
	require.Nil(t, merr)
	require.Equal(t, "public", rule.(map[string]any)["join_rule"])
	require.Less(t, countMembers(t, room.roomID), int64(len(users)), "member rows are written in the background")

	// Client reads and sends keep working on a partial room.
	members, merr := hs.Room.GetRoomMembers(room.roomID, []MembershipState{MembershipJoin})
	require.Nil(t, merr)
	require.NotEmpty(t, members)
	states, merr := hs.Room.GetRoomStateAll(room.roomID)
	require.Nil(t, merr)
	require.NotEmpty(t, states)
	sent, merr := hs.Event.SendRoomEvent(room.roomID, "@alice:test.local", "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "hello"}, nil)
	require.Nil(t, merr, "sending in a partial room must work")
	require.NotEmpty(t, sent.EventID)

	unblock()
	asyncJoins.idle()

	require.False(t, partialFlag(room.roomID), "flag cleared when the background write is done")
	require.Equal(t, int64(len(users)+2), countMembers(t, room.roomID)) // members + owner + alice
	for _, u := range users {
		require.True(t, joined(t, room.roomID, u), u)
	}
	for _, p := range room.state {
		require.True(t, eventStored(t, p.EventID()), p.Type())
	}
	require.Greater(t, batches.Load(), int64(3), "events are written in several bounded batches")
}

func TestAsyncJoinSmallRoomsAreStoredInline(t *testing.T) {
	hs, live, room, users := asyncJoinEnv(t, 20)
	asyncJoinSettingsSource = func() asyncJoinSettings {
		s := loadAsyncJoinSettings()
		s.SyncThreshold = 1000
		return s
	}
	joinAlice(t, hs, room, live)
	require.False(t, partialFlag(room.roomID), "a small room is complete when the join returns")
	require.Equal(t, int64(len(users)+2), countMembers(t, room.roomID))
}

func TestAsyncJoinFederationEndpointsAnswerNotFoundWhilePartial(t *testing.T) {
	hs, live, room, _ := asyncJoinEnv(t, 30)
	release, unblock := joinTestGate(t)
	joinRemainderBatchHook = func() error { <-release; return nil }
	joinAlice(t, hs, room, live)
	require.True(t, partialFlag(room.roomID))
	var joinEventID string
	require.NoError(t, db.GetDB().Model(&MatrixEvent{}).Where("room_id = ? AND sender = ? AND event_type = ?", room.roomID, "@alice:test.local", "m.room.member").Select("event_id").Scan(&joinEventID).Error)

	router := partialFedRouter()
	for _, req := range []struct{ method, path string }{
		{"GET", "/state/" + room.roomID + "?event_id=" + url.QueryEscape(joinEventID)},
		{"GET", "/state_ids/" + room.roomID + "?event_id=" + url.QueryEscape(joinEventID)},
		{"GET", "/event/" + url.QueryEscape(joinEventID)},
		{"GET", "/make_join/" + room.roomID + "/@bob:test.local?ver=10"},
		{"PUT", "/send_join/" + room.roomID + "/$x"},
		{"GET", "/make_leave/" + room.roomID + "/@bob:test.local"},
		{"PUT", "/send_leave/" + room.roomID + "/$x"},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(req.method, req.path, bytes.NewReader([]byte("{}"))))
		require.Equal(t, 404, w.Code, req.path+" "+w.Body.String())
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Equal(t, "M_NOT_FOUND", body["errcode"], req.path)
		require.Contains(t, body["error"], "synchronised")
	}

	unblock()
	asyncJoins.idle()
	require.False(t, partialFlag(room.roomID))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/state_ids/"+room.roomID+"?event_id="+url.QueryEscape(joinEventID), nil))
	require.NotContains(t, w.Body.String(), "synchronised", "endpoints are served normally again once complete")
}

func TestAsyncJoinQueuesInboundPDUsAndReplaysThem(t *testing.T) {
	hs, live, room, _ := asyncJoinEnv(t, 30)
	release, unblock := joinTestGate(t)
	joinRemainderBatchHook = func() error { <-release; return nil }
	joinAlice(t, hs, room, live)

	pdu := map[string]any{"room_id": room.roomID, "type": "m.room.message", "sender": "@owner:" + live.name, "content": map[string]any{"body": "x"}}
	body, _ := json.Marshal(map[string]any{"origin": "test.local", "origin_server_ts": time.Now().UnixMilli(), "pdus": []any{pdu}})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/send/t1", bytes.NewReader(body))
	partialFedRouter().ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())
	var resp struct {
		PDUs map[string]map[string]any `json:"pdus"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.PDUs, 1)
	for _, v := range resp.PDUs {
		require.Empty(t, v, "queued PDU is accepted, not an error")
	}
	partialPDUQueue.Lock()
	require.Len(t, partialPDUQueue.rooms[room.roomID], 1)
	partialPDUQueue.Unlock()

	// A full queue refuses with a per-PDU error instead of growing.
	asyncJoinSettingsSource = func() asyncJoinSettings { s := loadAsyncJoinSettings(); s.MaxPending = 1; return s }
	require.False(t, queuePartialPDU(room.roomID, pdu))

	unblock()
	asyncJoins.idle()
	require.Empty(t, takePartialPDUs(room.roomID), "queue is drained when the room completes")
}

func TestAsyncJoinWriterIsIdempotentAndNeverOverwritesNewerState(t *testing.T) {
	hs, live, room, users := asyncJoinEnv(t, 25)
	release, unblock := joinTestGate(t)
	joinRemainderBatchHook = func() error { <-release; return nil }
	joinAlice(t, hs, room, live)
	// member000 left locally after the snapshot: the late snapshot row must not resurrect it.
	require.NoError(t, db.GetDB().Create(&MatrixRoomMember{RoomID: room.roomID, UserID: users[0], Membership: MembershipLeave, EventID: "$newer"}).Error)
	unblock()
	asyncJoins.idle()

	var m MatrixRoomMember
	require.NoError(t, db.GetDB().Where("room_id = ? AND user_id = ?", room.roomID, users[0]).First(&m).Error)
	require.Equal(t, MembershipLeave, m.Membership)
	events, state, mems := countRows(t, &MatrixEvent{}, room.roomID), countRows(t, &MatrixRoomState{}, room.roomID), countMembers(t, room.roomID)

	// Running the whole remainder again (retry/resume overlap) changes nothing.
	pj := prepareFromFake(t, hs, live, room)
	rem := &joinRemainder{roomID: room.roomID}
	for id, p := range pj.events {
		rem.items = append(rem.items, joinRemainderItem{p: p, state: pj.stateHas(id)})
	}
	rem.total = len(rem.items)
	st := asyncJoinSettingsSource()
	for i := 0; i < 2; i++ {
		rem.next, rem.written = 0, 0
		items := append([]joinRemainderItem(nil), rem.items...)
		again := &joinRemainder{roomID: room.roomID, items: items, total: len(items)}
		require.NoError(t, hs.Room.runJoinRemainder(context.Background(), again, st))
		require.Nil(t, again.items, "written items are released")
	}
	require.Equal(t, events, countRows(t, &MatrixEvent{}, room.roomID))
	require.Equal(t, state, countRows(t, &MatrixRoomState{}, room.roomID))
	require.Equal(t, mems, countMembers(t, room.roomID))
}

func (pj *preparedFederationJoin) stateHas(id string) bool {
	for _, p := range pj.state {
		if p.EventID() == id {
			return true
		}
	}
	return false
}

// prepareFromFake validates the fake room's join response the way a join does.
func prepareFromFake(t *testing.T, hs *HomeServer, live *fakeFed, room *fakeRoom) *preparedFederationJoin {
	t.Helper()
	var joinRaw json.RawMessage
	var joinEvent MatrixEvent
	require.NoError(t, db.GetDB().Where("room_id = ? AND sender = ? AND event_type = ?", room.roomID, "@alice:test.local", "m.room.member").First(&joinEvent).Error)
	joinRaw = json.RawMessage(joinEvent.FederationJSON)
	resp := &sendJoinResponse{State: room.wire(), AuthChain: room.wire(), Event: joinRaw}
	pj, err := hs.Room.validateFederationJoin(withKeyNotaries(context.Background(), live.name), room.roomID, "@alice:test.local", "10", joinRaw, resp)
	require.NoError(t, err)
	return pj
}

func TestAsyncJoinEssentialSelection(t *testing.T) {
	hs, live, room, users := asyncJoinEnv(t, 15)
	joinAlice(t, hs, room, live)
	pj := prepareFromFake(t, hs, live, room)
	ess := pj.essentialEvents()
	for _, p := range pj.state {
		switch {
		case p.Type() == "m.room.member" && *p.StateKey() == users[3]:
			require.False(t, ess[p.EventID()], "ordinary members are not essential")
		case p.Type() == "m.room.create", p.Type() == "m.room.join_rules", p.Type() == "m.room.name":
			require.True(t, ess[p.EventID()], p.Type())
		}
	}
	// The set is closed under auth events.
	for id := range ess {
		for _, parent := range pj.events[id].AuthEventIDs() {
			if _, ok := pj.events[parent]; ok {
				require.True(t, ess[parent], "auth ancestor of an essential event must be essential")
			}
		}
	}
}

func TestAsyncJoinRetriesAfterFailure(t *testing.T) {
	hs, live, room, users := asyncJoinEnv(t, 30)
	var calls atomic.Int64
	joinRemainderBatchHook = func() error {
		if calls.Add(1) <= 5 { // fails the first batch 3 times (1 job attempt) and part of the second attempt
			return fmt.Errorf("simulated database outage")
		}
		return nil
	}
	asyncJoinSettingsSource = func() asyncJoinSettings {
		s := loadAsyncJoinSettings()
		s.SyncThreshold = 0
		s.BatchEvents = 10
		s.RetryBase = 5 * time.Millisecond
		s.Retries = 4
		return s
	}
	joinAlice(t, hs, room, live)
	asyncJoins.idle()
	require.False(t, partialFlag(room.roomID))
	require.Equal(t, int64(len(users)+2), countMembers(t, room.roomID))
}

func TestAsyncJoinGivesUpThenResumesFromRemote(t *testing.T) {
	hs, live, room, users := asyncJoinEnv(t, 30)
	var broken atomic.Bool
	broken.Store(true)
	joinRemainderBatchHook = func() error {
		if broken.Load() {
			return fmt.Errorf("simulated database outage")
		}
		return nil
	}
	asyncJoinSettingsSource = func() asyncJoinSettings {
		s := loadAsyncJoinSettings()
		s.SyncThreshold = 0
		s.BatchEvents = 10
		s.RetryBase = time.Millisecond
		s.Retries = 1
		return s
	}
	// What the remote answers for /state at our join event: its state plus our join.
	live.stateResp = func() any {
		var joinEvent MatrixEvent
		require.NoError(t, db.GetDB().Where("room_id = ? AND sender = ? AND event_type = ?", room.roomID, "@alice:test.local", "m.room.member").First(&joinEvent).Error)
		return map[string]any{"pdus": append(room.wire(), json.RawMessage(joinEvent.FederationJSON)), "auth_chain": room.wire()}
	}
	joinAlice(t, hs, room, live)
	asyncJoins.idle()
	require.True(t, partialFlag(room.roomID), "after giving up the room stays flagged so it can be resumed")
	require.Less(t, countMembers(t, room.roomID), int64(len(users)))

	// "Restart": nothing is in memory any more; resume re-fetches from the recorded server.
	broken.Store(false)
	hs.Room.ResumePartialStateJoins()
	asyncJoins.idle()
	require.False(t, partialFlag(room.roomID))
	require.Equal(t, 1, live.count("state"))
	for _, u := range users {
		require.True(t, joined(t, room.roomID, u), u)
	}
	// Alice's own join (newer than the remote snapshot) is untouched.
	require.True(t, joined(t, room.roomID, "@alice:test.local"))
}

func TestAsyncJoinRepeatJoinRequestRestartsInterruptedSync(t *testing.T) {
	hs, live, room, users := asyncJoinEnv(t, 30)
	var broken atomic.Bool
	broken.Store(true)
	joinRemainderBatchHook = func() error {
		if broken.Load() {
			return fmt.Errorf("outage")
		}
		return nil
	}
	asyncJoinSettingsSource = func() asyncJoinSettings {
		s := loadAsyncJoinSettings()
		s.SyncThreshold, s.BatchEvents, s.RetryBase, s.Retries = 0, 10, time.Millisecond, 0
		return s
	}
	live.stateResp = func() any { return map[string]any{"pdus": room.wire(), "auth_chain": room.wire()} }
	joinAlice(t, hs, room, live)
	asyncJoins.idle()
	require.True(t, partialFlag(room.roomID))
	broken.Store(false)
	joinAlice(t, hs, room, live) // client retries the join: already joined, but the writer is revived
	asyncJoins.idle()
	require.False(t, partialFlag(room.roomID))
	require.True(t, joined(t, room.roomID, users[7]))
}

func TestAsyncJoinQueueFullFallsBackToRefetch(t *testing.T) {
	hs, live, room, users := asyncJoinEnv(t, 30)
	asyncJoinSettingsSource = func() asyncJoinSettings {
		s := loadAsyncJoinSettings()
		s.SyncThreshold, s.BatchEvents, s.RetryBase, s.MaxHeld = 0, 10, time.Millisecond, 1
		return s
	}
	live.stateResp = func() any { return map[string]any{"pdus": room.wire(), "auth_chain": room.wire()} }
	// Another room already holds the only in-memory slot.
	asyncJoins.mu.Lock()
	asyncJoins.held = 1
	asyncJoins.mu.Unlock()
	joinAlice(t, hs, room, live)
	asyncJoins.idle()
	require.Equal(t, 1, live.count("state"), "the room refetched its state instead of holding it")
	require.False(t, partialFlag(room.roomID))
	require.True(t, joined(t, room.roomID, users[5]))
}

func TestJoinStateEventCapRejectsUpFrontWithReadableError(t *testing.T) {
	hs, live, room, _ := asyncJoinEnv(t, 40)
	old := sendJoinEventCap
	sendJoinEventCap = func() int { return 20 }
	t.Cleanup(func() { sendJoinEventCap = old })
	_, merr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{live.name}})
	require.NotNil(t, merr)
	require.Equal(t, "M_TOO_LARGE", merr.ErrCode)
	require.Contains(t, merr.Error, "too large")
	require.Contains(t, merr.Error, "MATRIX_MAX_JOIN_STATE_EVENTS")
	require.Contains(t, merr.Error, "20")
	require.Equal(t, 413, matrixErrorStatus(merr))
	require.Equal(t, int64(0), countRows(t, &MatrixEvent{}, room.roomID), "nothing is stored for a refused join")
	var rooms int64
	require.NoError(t, db.GetDB().Model(&MatrixRoom{}).Where("room_id = ?", room.roomID).Count(&rooms).Error)
	require.Zero(t, rooms)
	require.Equal(t, 1, live.count("send_join"), "the oversized answer is not retried on another protocol version")
}

func TestJoinStateEventCapEnvDefault(t *testing.T) {
	t.Setenv("MATRIX_MAX_JOIN_STATE_EVENTS", "")
	require.Equal(t, 250000, sendJoinEventCap())
	t.Setenv("MATRIX_MAX_JOIN_STATE_EVENTS", "1234")
	require.Equal(t, 1234, sendJoinEventCap())
	t.Setenv("MATRIX_MAX_JOIN_STATE_EVENTS", "junk")
	require.Equal(t, 250000, sendJoinEventCap())
}

func TestPartialStateMarkerRoundTrip(t *testing.T) {
	m := partialStateMarker("$ev", "matrix.example")
	id, server, ok := parsePartialStateMarker(&m)
	require.True(t, ok)
	require.Equal(t, "$ev", id)
	require.Equal(t, "matrix.example", server)
	long := partialStateMarker("$ev", string(bytes.Repeat([]byte("a"), 300)))
	require.LessOrEqual(t, len(long), 255)
	id, server, ok = parsePartialStateMarker(&long)
	require.True(t, ok)
	require.Equal(t, "$ev", id)
	require.Empty(t, server)
	for _, v := range []*string{nil, new(""), new("$abc")} {
		_, _, ok = parsePartialStateMarker(v)
		require.False(t, ok)
	}
}

func TestAsyncJoinLargeRoomReturnsFastAndCompletes(t *testing.T) {
	if testing.Short() {
		t.Skip("large room")
	}
	hs, live, room, users := asyncJoinEnv(t, 3000)
	asyncJoinSettingsSource = func() asyncJoinSettings { s := loadAsyncJoinSettings(); s.SyncThreshold = 0; return s }
	start := time.Now()
	joinAlice(t, hs, room, live)
	t.Logf("join returned after %s with partial=%v", time.Since(start), partialFlag(room.roomID))
	require.True(t, partialFlag(room.roomID))
	asyncJoins.idle()
	t.Logf("background complete after %s", time.Since(start))
	require.False(t, partialFlag(room.roomID))
	require.Equal(t, int64(len(users)+2), countMembers(t, room.roomID))
}
