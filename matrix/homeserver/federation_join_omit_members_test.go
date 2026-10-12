package homeserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

// logCapture collects every log line written while a test runs.
type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func (l *logCapture) Levels() []logrus.Level { return logrus.AllLevels }
func (l *logCapture) Fire(e *logrus.Entry) error {
	l.mu.Lock()
	l.lines = append(l.lines, e.Message)
	l.mu.Unlock()
	return nil
}
func (l *logCapture) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	obj := log.GetLogObject()
	oldLevel := obj.GetLevel()
	obj.SetLevel(logrus.DebugLevel)
	c := &logCapture{}
	old := obj.ReplaceHooks(make(logrus.LevelHooks))
	obj.AddHook(c)
	t.Cleanup(func() { obj.ReplaceHooks(old); obj.SetLevel(oldLevel) })
	return c
}

func withOmit(on bool) {
	old := joinSettingsSource
	joinSettingsSource = func() joinSettings {
		s := old()
		s.OmitMembers = on
		return s
	}
}

// omitWire is what a resident server answers for omit_members=true: every
// member event except the owner's (which authorises the rest) is left out.
func (fr *fakeRoom) omitWire() []json.RawMessage {
	var out []json.RawMessage
	for _, p := range fr.state {
		if p.Type() == "m.room.member" && !strings.HasPrefix(*p.StateKey(), "@owner:") {
			continue
		}
		out = append(out, json.RawMessage(p.JSON()))
	}
	return out
}

// serveOmit makes f honour omit_members; hits full/partial answers are counted.
func (fr *fakeRoom) serveOmit(f *fakeFed, servers []string) {
	fr.serve(f)
	f.sendJoinQ = func(q string, event json.RawMessage, w http.ResponseWriter, r *http.Request) (int, any) {
		if strings.Contains(q, "omit_members=true") {
			f.hit("send_join_omit")
			return 200, map[string]any{"origin": f.name, "state": fr.omitWire(), "auth_chain": fr.omitWire(), "event": event,
				"members_omitted": true, "servers_in_room": servers}
		}
		f.hit("send_join_full")
		return 200, map[string]any{"origin": f.name, "state": fr.wire(), "auth_chain": fr.wire(), "event": event}
	}
}

func fullStateResp(t *testing.T, live *fakeFed, room *fakeRoom) func() any {
	return func() any {
		var joinEvent MatrixEvent
		require.NoError(t, db.GetDB().Where("room_id = ? AND sender = ? AND event_type = ?", room.roomID, "@alice:test.local", "m.room.member").First(&joinEvent).Error)
		return map[string]any{"pdus": append(room.wire(), json.RawMessage(joinEvent.FederationJSON)), "auth_chain": room.wire()}
	}
}

func TestOmitMembersJoinReturnsWhilePartialAndBackgroundCompletes(t *testing.T) {
	hs, live, room, users := asyncJoinEnv(t, 40)
	withOmit(true)
	room.serveOmit(live, []string{live.name, "other.example", "bad server!", "third.example"})
	release := make(chan struct{})
	base := fullStateResp(t, live, room)
	live.stateResp = func() any { <-release; return base() }
	logs := captureLogs(t)

	start := time.Now()
	joinAlice(t, hs, room, live)
	require.Less(t, time.Since(start), 5*time.Second)

	require.Equal(t, 1, live.count("send_join_omit"))
	require.Equal(t, 0, live.count("send_join_full"))
	require.Equal(t, []string{"omit_members=true"}, live.queries)
	require.True(t, joined(t, room.roomID, "@alice:test.local"))
	require.True(t, partialFlag(room.roomID), "room is partial while members are missing")
	require.Less(t, countMembers(t, room.roomID), int64(len(users)))
	// The hint is kept (cleaned) for destinations while the room is partial.
	require.Equal(t, []string{live.name, "other.example", "third.example"}, partialRoomServerHints(room.roomID, 10))
	require.Equal(t, []string{live.name}, partialRoomServerHints(room.roomID, 1))
	// Client operations work in the meantime.
	_, merr := hs.Event.SendRoomEvent(room.roomID, "@alice:test.local", "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "hi"}, nil)
	require.Nil(t, merr)

	close(release)
	asyncJoins.idle()
	require.False(t, partialFlag(room.roomID))
	require.Equal(t, int64(len(users)+2), countMembers(t, room.roomID))
	for _, u := range users {
		require.True(t, joined(t, room.roomID, u), u)
	}
	require.Equal(t, 1, live.count("state"))
	require.Empty(t, partialRoomServerHints(room.roomID, 10), "hint is dropped once the state is complete")

	text := logs.text()
	require.Contains(t, text, "send_join start omit_members=true")
	require.Contains(t, text, "members_omitted=true")
	require.Contains(t, text, "servers_in_room=")
	require.Contains(t, text, "bytes=")
}

func TestOmitMembersRemoteIgnoringParameterKeepsFullBehaviour(t *testing.T) {
	hs, live, room, users := asyncJoinEnv(t, 20)
	withOmit(true)
	asyncJoinSettingsSource = func() asyncJoinSettings { s := loadAsyncJoinSettings(); s.SyncThreshold = 1000; return s }
	room.serve(live) // plain handler: ignores the query
	joinAlice(t, hs, room, live)
	require.Equal(t, []string{"omit_members=true"}, live.queries)
	require.False(t, partialFlag(room.roomID), "a full answer needs no partial state")
	require.Equal(t, int64(len(users)+2), countMembers(t, room.roomID))
	require.Equal(t, 0, live.count("state"))
}

func TestOmitMembersDisabledSendsNoParameter(t *testing.T) {
	hs, live, room, _ := asyncJoinEnv(t, 5)
	withOmit(false)
	room.serveOmit(live, nil)
	joinAlice(t, hs, room, live)
	require.Equal(t, []string{""}, live.queries)
	require.Equal(t, 1, live.count("send_join_full"))
}

func TestOmitMembersRejectedParameterRetriesWithoutIt(t *testing.T) {
	hs, live, room, users := asyncJoinEnv(t, 15)
	withOmit(true)
	asyncJoinSettingsSource = func() asyncJoinSettings { s := loadAsyncJoinSettings(); s.SyncThreshold = 1000; return s }
	room.serve(live)
	live.sendJoinQ = func(q string, event json.RawMessage, w http.ResponseWriter, r *http.Request) (int, any) {
		if q != "" {
			return 400, map[string]any{"errcode": "M_UNRECOGNIZED", "error": "unknown parameter omit_members"}
		}
		return 200, map[string]any{"origin": live.name, "state": room.wire(), "auth_chain": room.wire(), "event": event}
	}
	joinAlice(t, hs, room, live)
	require.Equal(t, []string{"omit_members=true", ""}, live.queries)
	require.False(t, partialFlag(room.roomID))
	require.Equal(t, int64(len(users)+2), countMembers(t, room.roomID))
}

func TestOmitMembersNotInRoomStaysNotFound(t *testing.T) {
	hs, live, room, _ := asyncJoinEnv(t, 3)
	withOmit(true)
	room.serve(live)
	live.sendJoinQ = func(q string, event json.RawMessage, w http.ResponseWriter, r *http.Request) (int, any) {
		return 404, map[string]any{"errcode": "M_NOT_FOUND", "error": "Not in room"}
	}
	_, merr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{live.name}})
	require.NotNil(t, merr)
	require.Equal(t, "M_NOT_FOUND", merr.ErrCode)
	require.False(t, joined(t, room.roomID, "@alice:test.local"))
}

func TestOmitMembersInvalidPartialAnswerFallsBackToFullState(t *testing.T) {
	hs, live, room, users := asyncJoinEnv(t, 15)
	withOmit(true)
	asyncJoinSettingsSource = func() asyncJoinSettings { s := loadAsyncJoinSettings(); s.SyncThreshold = 1000; return s }
	room.serve(live)
	logs := captureLogs(t)
	live.sendJoinQ = func(q string, event json.RawMessage, w http.ResponseWriter, r *http.Request) (int, any) {
		if q != "" { // a broken partial answer: the create event is missing
			var state []json.RawMessage
			for _, p := range room.state {
				if p.Type() != "m.room.create" {
					state = append(state, json.RawMessage(p.JSON()))
				}
			}
			return 200, map[string]any{"origin": live.name, "state": state, "auth_chain": state, "event": event, "members_omitted": true, "servers_in_room": []string{live.name}}
		}
		return 200, map[string]any{"origin": live.name, "state": room.wire(), "auth_chain": room.wire(), "event": event}
	}
	joinAlice(t, hs, room, live)
	require.Equal(t, []string{"omit_members=true", ""}, live.queries)
	require.False(t, partialFlag(room.roomID))
	require.Equal(t, int64(len(users)+2), countMembers(t, room.roomID))
	require.Contains(t, logs.text(), "retrying send_join without omit_members")
}

func TestOmitMembersAnswerStillHonoursCaps(t *testing.T) {
	hs, live, room, _ := asyncJoinEnv(t, 10)
	withOmit(true)
	room.serveOmit(live, nil)
	old := sendJoinEventCap
	sendJoinEventCap = func() int { return 3 }
	defer func() { sendJoinEventCap = old }()
	_, merr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{live.name}})
	require.NotNil(t, merr)
	require.Equal(t, "M_TOO_LARGE", merr.ErrCode)

	sendJoinEventCap = old
	joinSettingsSource = func() joinSettings {
		s := loadJoinSettings()
		s.OmitMembers, s.MaxBodyBytes = true, 200 // body cap far below the answer
		s.MakeJoinTimeout, s.SendJoinTimeout, s.Total, s.Parallelism = 3*time.Second, 5*time.Second, 10*time.Second, 1
		return s
	}
	_, merr = hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{live.name}})
	require.NotNil(t, merr)
	require.Equal(t, "M_TOO_LARGE", merr.ErrCode)
}

func TestOmitMembersBackgroundRetriesWhenStateFetchFails(t *testing.T) {
	hs, live, room, users := asyncJoinEnv(t, 20)
	withOmit(true)
	room.serveOmit(live, []string{live.name})
	asyncJoinSettingsSource = func() asyncJoinSettings {
		s := loadAsyncJoinSettings()
		s.SyncThreshold, s.BatchEvents, s.RetryBase, s.Retries = 0, 10, 5*time.Millisecond, 4
		return s
	}
	var mu sync.Mutex
	calls := 0
	base := fullStateResp(t, live, room)
	live.stateResp = func() any {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			return "not a state document" // decodes as an error
		}
		return base()
	}
	joinAlice(t, hs, room, live)
	asyncJoins.idle()
	require.False(t, partialFlag(room.roomID))
	require.GreaterOrEqual(t, live.count("state"), 2)
	for _, u := range users {
		require.True(t, joined(t, room.roomID, u), u)
	}
}

func TestOmitMembersPartialMarkerSurvivesAndResumes(t *testing.T) {
	hs, live, room, users := asyncJoinEnv(t, 20)
	withOmit(true)
	room.serveOmit(live, []string{live.name})
	var broken = true
	var mu sync.Mutex
	base := fullStateResp(t, live, room)
	live.stateResp = func() any {
		mu.Lock()
		defer mu.Unlock()
		if broken {
			return "garbage"
		}
		return base()
	}
	asyncJoinSettingsSource = func() asyncJoinSettings {
		s := loadAsyncJoinSettings()
		s.SyncThreshold, s.BatchEvents, s.RetryBase, s.Retries = 0, 10, time.Millisecond, 0
		return s
	}
	joinAlice(t, hs, room, live)
	asyncJoins.idle()
	require.True(t, partialFlag(room.roomID), "background sync gave up: flag stays for the next start")
	mu.Lock()
	broken = false
	mu.Unlock()
	hs.Room.ResumePartialStateJoins() // "restart"
	asyncJoins.idle()
	require.False(t, partialFlag(room.roomID))
	require.True(t, joined(t, room.roomID, users[5]))
}

func TestOmitMembersServersInRoomHintIsKeptBoundedWhilePartial(t *testing.T) {
	hs, live, room, _ := asyncJoinEnv(t, 10)
	withOmit(true)
	room.serveOmit(live, []string{live.name, "hinted-a.example", "hinted-b.example"})
	stop := make(chan struct{})
	live.stateResp = func() any { <-stop; return "stop" } // sync never completes during the test
	defer close(stop)
	joinAlice(t, hs, room, live)
	require.True(t, partialFlag(room.roomID))
	got := partialRoomServerHints(room.roomID, maxPartialFanout())
	require.Equal(t, []string{live.name, "hinted-a.example", "hinted-b.example"}, got)
	t.Setenv("MATRIX_JOIN_PARTIAL_FANOUT", "1")
	require.Equal(t, []string{live.name}, partialRoomServerHints(room.roomID, maxPartialFanout()))
}

func TestSendJoinDecoderReadsServersInRoomAndToleratesGarbage(t *testing.T) {
	res, err := decodeSendJoinResponse(strings.NewReader(`{"state":[],"auth_chain":[],"members_omitted":true,"servers_in_room":["a.example","b.example"]}`))
	require.NoError(t, err)
	require.True(t, res.MembersOmitted)
	require.Equal(t, []string{"a.example", "b.example"}, res.ServersInRoom)
	res, err = decodeSendJoinResponse(strings.NewReader(`{"state":[],"auth_chain":[],"members_omitted":true,"servers_in_room":{"x":1}}`))
	require.NoError(t, err)
	require.Empty(t, res.ServersInRoom)
	require.Equal(t, []string{"a.example"}, cleanServersInRoom([]string{"a.example", "a.example", "me.example", "bad host!", ""}, "me.example"))
}

// ---- diagnostics ------------------------------------------------------------

func TestJoinFailureLogShowsPhaseBytesAndFullError(t *testing.T) {
	hang := newFakeFed(t)
	hs := joinTestEnv(t, hang)
	room := newFakeRoom(t, hang, hang)
	room.serve(hang)
	logs := captureLogs(t)
	hang.sendJoinQ = func(q string, event json.RawMessage, w http.ResponseWriter, r *http.Request) (int, any) {
		// Headers go out, then the body stalls half-way: the join must report
		// the bytes received so far and the phase.
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"origin":"x","state":[`))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		return 0, nil
	}
	joinSettingsSource = func() joinSettings {
		return joinSettings{OmitMembers: true, Total: 5 * time.Second, MakeJoinTimeout: 2 * time.Second, SendJoinTimeout: 400 * time.Millisecond,
			MaxAttempts: 1, Parallelism: 1, MaxConcurrent: 3, DeadHostTTL: time.Minute, MaxBodyBytes: 8 << 20}
	}
	_, merr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{hang.name}})
	require.NotNil(t, merr)
	text := logs.text()
	line := ""
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, "failed: class=") {
			line = l
		}
	}
	require.NotEmpty(t, line, text)
	require.Contains(t, line, "class=timeout")
	require.Contains(t, line, "phase=send_join(reading response body)")
	require.Contains(t, line, "omit_members=true")
	require.Contains(t, line, "status=200")
	require.Contains(t, line, "bytes=23")
	require.Contains(t, line, "elapsed=")
	require.Contains(t, line, "send_join_elapsed=")
	require.Contains(t, line, "addr=")
	require.Contains(t, line, "deadline exceeded", "the error text must not be cut short")
}

func TestJoinFailureLogReportsMakeJoinPhaseAndDeadlineVictims(t *testing.T) {
	hang := newFakeFed(t)
	hs := joinTestEnv(t, hang)
	hang.makeJoinHook = func(w http.ResponseWriter, r *http.Request) bool { <-r.Context().Done(); return true }
	room := newFakeRoom(t, hang, hang)
	room.serve(hang)
	logs := captureLogs(t)
	joinSettingsSource = func() joinSettings {
		s := loadJoinSettings()
		s.Total, s.MakeJoinTimeout, s.Parallelism = 400*time.Millisecond, 30*time.Second, 1
		return s
	}
	_, merr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{hang.name}})
	require.NotNil(t, merr)
	require.Contains(t, logs.text(), "phase=make_join")
	require.Contains(t, logs.text(), "class=timeout", "attempts cut off by the total deadline are logged too")
}

func TestShortErrDoesNotCutOffLongErrors(t *testing.T) {
	long := strings.Repeat("a", 900) + " context deadline exceeded"
	out := shortErr(&federationTransportError{err: context.DeadlineExceeded})
	require.NotContains(t, out, "truncated")
	out = shortErr(errString(long))
	require.True(t, strings.HasSuffix(out, "context deadline exceeded"), out)
	require.Contains(t, shortErr(errString(strings.Repeat("b", 5000))), "(truncated)")
}

type errString string

func (e errString) Error() string { return string(e) }

var _ = gmsl.RoomVersion("10")
