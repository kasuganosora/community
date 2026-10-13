package homeserver

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/middleware"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/stretchr/testify/require"
)

// ---- fake federation servers (httptest TLS) --------------------------------

type fakeFed struct {
	t    *testing.T
	srv  *httptest.Server
	name string
	hs   *HomeServer

	mu   sync.Mutex
	hits map[string]int

	// behaviour switches
	noKeyDoc     bool                // /key/v2/server answers 500
	notaryDocs   map[string]*fakeFed // servers this one vouches for via /key/v2/query
	notaryBadSig bool                // sign notary answers with a stranger's key
	makeJoinHook func(w http.ResponseWriter, r *http.Request) bool
	template     func(user string) any           // make_join event template
	sendJoin     func(event json.RawMessage) any // send_join response body
	stateResp    func() any                      // federation /state response body
	// sendJoinQ, when set, answers send_join with its own status and body and
	// sees the raw query string (omit_members).
	sendJoinQ func(query string, event json.RawMessage, w http.ResponseWriter, r *http.Request) (status int, body any)
	queries   []string
	// leave handling: template builds the make_leave event, sendLeaveStatus (0 =
	// 200) answers send_leave, leaveEvents records the PDUs sent.
	leaveTemplate   func(user string) any
	sendLeaveStatus int
	sendLeaveBody   string
	leaveEvents     []json.RawMessage
	// bulkNotary answers POST /key/v2/query; notaryPosts counts them.
	bulkNotary bool
}

func newFakeFed(t *testing.T) *fakeFed {
	t.Helper()
	f := &fakeFed{t: t, hits: map[string]int{}, notaryDocs: map[string]*fakeFed{}}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	f.name = strings.TrimPrefix(f.srv.URL, "https://")
	f.hs = minimalHS(f.name)
	// A reused httptest port is a new signing identity, not a key rotation
	// under the immutable key ID cached for the previous fake server.
	f.hs.keyID = "ed25519:test_" + hex.EncodeToString(f.hs.publicKey[:16])
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeFed) hit(kind string) {
	f.mu.Lock()
	f.hits[kind]++
	f.mu.Unlock()
}

func (f *fakeFed) count(kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[kind]
}

func (f *fakeFed) kill() { f.srv.Close() }

func (f *fakeFed) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case path == "/_matrix/key/v2/server":
		f.hit("key")
		if f.noKeyDoc {
			http.Error(w, "boom", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(NewFederationService(f.hs).GetServerKey())
	case path == "/_matrix/key/v2/query" && r.Method == http.MethodPost:
		f.hit("bulk_query")
		var req struct {
			ServerKeys map[string]map[string]any `json:"server_keys"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if !f.bulkNotary {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"errcode":"M_UNRECOGNIZED"}`))
			return
		}
		var docs []any
		for server := range req.ServerKeys {
			target := f.notaryDocs[server]
			if target == nil {
				continue
			}
			doc := NewFederationService(target.hs).GetServerKey()
			NewFederationService(f.hs).signEvent(doc)
			docs = append(docs, doc)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"server_keys": docs})
	case strings.Contains(path, "/federation/v1/make_leave/"):
		f.hit("make_leave")
		if f.leaveTemplate == nil {
			w.WriteHeader(404)
			return
		}
		user := path[strings.LastIndex(path, "/")+1:]
		user = strings.ReplaceAll(strings.Replace(user, "%40", "@", 1), "%3A", ":")
		_ = json.NewEncoder(w).Encode(map[string]any{"room_version": "10", "event": f.leaveTemplate(user)})
	case strings.Contains(path, "/federation/v2/send_leave/") || strings.Contains(path, "/federation/v1/send_leave/"):
		f.hit("send_leave")
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.leaveEvents = append(f.leaveEvents, body)
		f.mu.Unlock()
		if f.sendLeaveStatus != 0 {
			w.WriteHeader(f.sendLeaveStatus)
			_, _ = w.Write([]byte(f.sendLeaveBody))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	case strings.HasPrefix(path, "/_matrix/key/v2/query/"):
		f.hit("query")
		parts := strings.Split(strings.TrimPrefix(path, "/_matrix/key/v2/query/"), "/")
		target := f.notaryDocs[parts[0]]
		if target == nil {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"errcode":"M_NOT_FOUND"}`))
			return
		}
		doc := NewFederationService(target.hs).GetServerKey()
		signer := f.hs
		if f.notaryBadSig {
			signer = minimalHS(f.name)
		}
		NewFederationService(signer).signEvent(doc)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"server_keys": []any{doc}})
	case strings.Contains(path, "/federation/v1/make_join/"):
		f.hit("make_join")
		if f.makeJoinHook != nil && f.makeJoinHook(w, r) {
			return
		}
		user := path[strings.LastIndex(path, "/")+1:]
		_ = json.NewEncoder(w).Encode(map[string]any{"room_version": "10", "event": f.template(user)})
	case strings.Contains(path, "/federation/v2/send_join/"):
		f.hit("send_join")
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.queries = append(f.queries, r.URL.RawQuery)
		f.mu.Unlock()
		if f.sendJoinQ != nil {
			status, resp := f.sendJoinQ(r.URL.RawQuery, body, w, r)
			if status == 0 {
				return
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		_ = json.NewEncoder(w).Encode(f.sendJoin(body))
	case strings.Contains(path, "/federation/v1/state/") && f.stateResp != nil:
		f.hit("state")
		_ = json.NewEncoder(w).Encode(f.stateResp())
	default:
		w.WriteHeader(404)
	}
}

// fakeRoom is a v10 room hosted by `host`, with optional extra state events
// signed by other servers.
type fakeRoom struct {
	t      *testing.T
	host   *fakeFed
	roomID string
	state  []gmsl.PDU
	ver    gmsl.IRoomVersion
}

// newFakeRoom builds a room created by creator's server; host serves it.
func newFakeRoom(t *testing.T, host *fakeFed, creator *fakeFed) *fakeRoom {
	t.Helper()
	ver, err := gmsl.GetRoomVersion("10")
	require.NoError(t, err)
	// The shared test database can outlive a fake listener. A reused port
	// must not find another fixture's already-joined room and skip validation.
	fr := &fakeRoom{t: t, host: host, roomID: "!room_" + hex.EncodeToString(creator.hs.publicKey[:16]) + ":" + creator.name, ver: ver}
	owner := "@owner:" + creator.name
	fr.add(owner, "m.room.create", new(""), map[string]any{"creator": owner, "room_version": "10"}, creator)
	fr.add(owner, "m.room.member", new(owner), map[string]any{"membership": "join"}, creator)
	fr.add(owner, "m.room.join_rules", new(""), map[string]any{"join_rule": "public"}, creator)
	return fr
}

func (fr *fakeRoom) builder(sender, kind string, key *string, content map[string]any) *gmsl.EventBuilder {
	b := fr.ver.NewEventBuilder()
	b.RoomID, b.SenderID, b.Type, b.StateKey = fr.roomID, sender, kind, key
	b.Depth = int64(len(fr.state) + 1)
	if len(fr.state) > 0 {
		b.PrevEvents = []string{fr.state[len(fr.state)-1].EventID()}
	}
	require.NoError(fr.t, b.SetContent(content))
	auth, err := gmsl.NewAuthEvents(fr.state)
	require.NoError(fr.t, err)
	require.NoError(fr.t, b.AddAuthEvents(auth))
	return b
}

func (fr *fakeRoom) add(sender, kind string, key *string, content map[string]any, signer *fakeFed) gmsl.PDU {
	b := fr.builder(sender, kind, key, content)
	p, err := b.Build(time.Now(), spec.ServerName(signer.hs.Config.ServerName), gmsl.KeyID(signer.hs.keyID), signer.hs.privateKey)
	require.NoError(fr.t, err)
	fr.state = append(fr.state, p)
	return p
}

func (fr *fakeRoom) wire() []json.RawMessage {
	out := make([]json.RawMessage, 0, len(fr.state))
	for _, p := range fr.state {
		out = append(out, json.RawMessage(p.JSON()))
	}
	return out
}

// serve makes host answer make_join/send_join for this room.
func (fr *fakeRoom) serve(f *fakeFed) {
	f.template = func(user string) any {
		u := user
		if i := strings.Index(u, "%40"); i == 0 {
			u = "@" + u[3:]
		}
		u = strings.ReplaceAll(u, "%3A", ":")
		b := fr.builder(u, "m.room.member", &u, map[string]any{"membership": "join"})
		return b
	}
	f.sendJoin = func(event json.RawMessage) any {
		return map[string]any{"origin": f.name, "state": fr.wire(), "auth_chain": fr.wire(), "event": event}
	}
}

// ---- test environment -------------------------------------------------------

func joinTestEnv(t *testing.T, anyServer *fakeFed) *HomeServer {
	t.Helper()
	hs := createTestHomeServer(t)
	oldFactory, oldNotaries, oldSettings, oldWait := newFederationClient, defaultKeyNotaries, joinSettingsSource, joinGateWait
	newFederationClient = func(h *HomeServer) *FederationClient {
		c := NewFederationClient(h)
		c.fedHTTPClient = anyServer.srv.Client()
		return c
	}
	defaultKeyNotaries = nil // never reach the real matrix.org
	joinSettingsSource = func() joinSettings {
		return joinSettings{Total: 10 * time.Second, MakeJoinTimeout: 3 * time.Second, SendJoinTimeout: 5 * time.Second,
			MaxAttempts: 5, Parallelism: 1, MaxConcurrent: 3, DeadHostTTL: time.Minute, MaxBodyBytes: 8 << 20}
	}
	deadFederationHosts.reset()
	keyFetchFailures.reset()
	t.Cleanup(func() {
		newFederationClient, defaultKeyNotaries, joinSettingsSource, joinGateWait = oldFactory, oldNotaries, oldSettings, oldWait
		deadFederationHosts.reset()
		keyFetchFailures.reset()
	})
	return hs
}

func joined(t *testing.T, roomID, user string) bool {
	t.Helper()
	var n int64
	require.NoError(t, db.GetDB().Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ? AND membership = ?", roomID, user, MembershipJoin).Count(&n).Error)
	return n > 0
}

func eventStored(t *testing.T, id string) bool {
	t.Helper()
	var n int64
	require.NoError(t, db.GetDB().Model(&MatrixEvent{}).Where("event_id = ?", id).Count(&n).Error)
	return n > 0
}

// ---- tests ------------------------------------------------------------------

func TestOrderJoinServersPolicy(t *testing.T) {
	got := orderJoinServers("me.example", "!abc:room.example", []string{"via1.example", "room.example", "me.example"}, []string{"dir1.example", "via1.example"}, "inviter.example")
	require.Equal(t, []string{"room.example", "via1.example", "dir1.example", "inviter.example"}, got)
	// Room v12 IDs carry no domain.
	require.Equal(t, []string{"via1.example"}, orderJoinServers("me.example", "!hash", []string{"via1.example"}, nil, ""))
}

func TestSelectJoinCandidatesSkipsDeadHostsAndCaps(t *testing.T) {
	deadFederationHosts.reset()
	defer deadFederationHosts.reset()
	s := joinSettings{MaxAttempts: 3, Parallelism: 2}
	deadFederationHosts.mark("dead.example", time.Minute, "refused")
	require.Equal(t, []string{"a.example", "b.example", "c.example"},
		selectJoinCandidates([]string{"dead.example", "a.example", "b.example", "c.example", "d.example"}, s))
	// If everything is dead, still probe the best candidates.
	require.Equal(t, []string{"dead.example"}, selectJoinCandidates([]string{"dead.example"}, s))
	deadFederationHosts.mark("a.example", time.Minute, "x")
	require.Equal(t, []string{"dead.example", "a.example"}, selectJoinCandidates([]string{"dead.example", "a.example", "c2.example"}[:2], s))
}

// httptest can reuse a closed server's address. The key cache survives tests,
// so a previous fake at that address must not impersonate the new fixture key.
func TestJoinIgnoresCachedKeysFromPreviousFakeServerIdentity(t *testing.T) {
	previous := newFakeFed(t)
	live := newFakeFed(t)
	hs := joinTestEnv(t, live)
	room := newFakeRoom(t, live, live)
	room.serve(live)
	GetKeyCache().Set(live.name, previous.hs.keyID, previous.hs.publicKey, time.Now().Add(time.Hour))

	id, merr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{live.name}})
	require.Nil(t, merr)
	require.Equal(t, room.roomID, id)
	require.True(t, joined(t, room.roomID, "@alice:test.local"))
	require.Positive(t, live.count("key"), "the current fixture key must be fetched and its signatures verified")
}

func TestJoinSkipsDeadHostAndSucceedsViaNextServer(t *testing.T) {
	live := newFakeFed(t)
	dead := newFakeFed(t)
	dead.kill()
	hs := joinTestEnv(t, live)
	// The room's creator server is down now, but its key is already known.
	GetKeyCache().Set(dead.name, dead.hs.keyID, dead.hs.publicKey, time.Now().Add(24*time.Hour))
	room := newFakeRoom(t, live, dead) // room ID domain is the dead server: tried first
	room.serve(live)

	start := time.Now()
	id, merr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{live.name}})
	require.Nil(t, merr)
	require.Equal(t, room.roomID, id)
	require.Less(t, time.Since(start), 5*time.Second)
	require.True(t, joined(t, room.roomID, "@alice:test.local"))
	_, isDead := deadFederationHosts.active(dead.name)
	require.True(t, isDead, "the unreachable room-domain server must be negatively cached")
	require.Equal(t, int64(0), joinAttemptsRunning.Load())

	// A later join does not even try it.
	require.Equal(t, []string{live.name}, selectJoinCandidates([]string{dead.name, live.name}, joinSettingsSource()))
}

func TestJoinParallelAttemptsFirstSuccessWinsAndCancelsRest(t *testing.T) {
	slow := newFakeFed(t)
	live := newFakeFed(t)
	hs := joinTestEnv(t, live)
	release := make(chan struct{})
	slow.makeJoinHook = func(w http.ResponseWriter, r *http.Request) bool {
		select {
		case <-r.Context().Done(): // cancelled by the winner
		case <-release:
		}
		return true
	}
	defer close(release)
	room := newFakeRoom(t, live, live)
	room.serve(live)
	joinSettingsSource = func() joinSettings {
		s := loadJoinSettings()
		s.Parallelism = 3
		return s
	}
	start := time.Now()
	_, merr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{slow.name, live.name}})
	require.Nil(t, merr)
	require.Less(t, time.Since(start), 5*time.Second, "a hanging server must not delay a working one")
	require.Equal(t, int64(0), joinAttemptsRunning.Load(), "no attempt goroutine may outlive the join")
	require.Equal(t, 1, live.count("send_join"), "exactly one send_join")
}

func TestJoinTotalDeadlineReturnsTimeoutAndStopsEverything(t *testing.T) {
	hang := newFakeFed(t)
	hs := joinTestEnv(t, hang)
	hang.makeJoinHook = func(w http.ResponseWriter, r *http.Request) bool {
		<-r.Context().Done()
		return true
	}
	room := newFakeRoom(t, hang, hang)
	room.serve(hang)
	joinSettingsSource = func() joinSettings {
		s := loadJoinSettings()
		s.Total = 700 * time.Millisecond
		s.MakeJoinTimeout = 30 * time.Second
		return s
	}
	start := time.Now()
	_, merr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{hang.name}})
	elapsed := time.Since(start)
	require.NotNil(t, merr)
	require.Equal(t, "M_UNKNOWN", merr.ErrCode)
	require.Contains(t, merr.Error, "Timed out")
	require.Equal(t, http.StatusGatewayTimeout, matrixErrorStatus(merr))
	require.Less(t, elapsed, 3*time.Second)
	require.Equal(t, int64(0), joinAttemptsRunning.Load(), "attempt goroutines must be gone when JoinRoom returns")
	require.False(t, joined(t, room.roomID, "@alice:test.local"))
}

func TestJoinRequestContextCancellationAbortsJoin(t *testing.T) {
	hang := newFakeFed(t)
	hs := joinTestEnv(t, hang)
	hang.makeJoinHook = func(w http.ResponseWriter, r *http.Request) bool { <-r.Context().Done(); return true }
	room := newFakeRoom(t, hang, hang)
	room.serve(hang)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	start := time.Now()
	_, merr := hs.Room.JoinRoomContext(ctx, "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{hang.name}})
	require.NotNil(t, merr)
	require.Less(t, time.Since(start), 2*time.Second)
	require.Equal(t, int64(0), joinAttemptsRunning.Load())
}

func TestJoinNotaryFallbackProvidesDeadServerKey(t *testing.T) {
	live := newFakeFed(t)
	dead := newFakeFed(t)
	dead.kill()
	hs := joinTestEnv(t, live)
	t.Setenv("MATRIX_TRUSTED_KEY_SERVERS", live.name) // explicitly trusted by the administrator
	room := newFakeRoom(t, live, live)
	old := room.add("@old:"+dead.name, "m.room.member", new("@old:"+dead.name), map[string]any{"membership": "join"}, dead)
	room.serve(live)
	live.notaryDocs[dead.name] = dead // live (the room server) vouches for the dead server's keys

	_, merr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{live.name}})
	require.Nil(t, merr)
	require.Greater(t, live.count("query"), 0, "the notary must have been asked")
	require.True(t, eventStored(t, old.EventID()), "the event verified through the notary is kept")
	_, bad := keyFetchFailures.active(dead.name)
	require.False(t, bad)
}

func TestJoinLenientValidationSkipsUnverifiableHistory(t *testing.T) {
	live := newFakeFed(t)
	dead := newFakeFed(t)
	dead.kill()
	hs := joinTestEnv(t, live)
	room := newFakeRoom(t, live, live)
	old := room.add("@old:"+dead.name, "m.room.member", new("@old:"+dead.name), map[string]any{"membership": "join"}, dead)
	room.serve(live) // no notary knows the dead server

	_, merr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{live.name}})
	require.Nil(t, merr, "an unreachable signer of non-essential state must not block the join")
	require.True(t, joined(t, room.roomID, "@alice:test.local"))
	require.False(t, eventStored(t, old.EventID()), "unverified events are skipped, never stored")
	_, unreachable := keyFetchFailures.active(dead.name)
	require.True(t, unreachable, "the dead signer costs one failure and is then remembered")
}

func TestJoinRejectsNotaryAnswerWithBadSignature(t *testing.T) {
	live := newFakeFed(t)
	dead := newFakeFed(t)
	dead.kill()
	hs := joinTestEnv(t, live)
	t.Setenv("MATRIX_TRUSTED_KEY_SERVERS", live.name) // explicitly trusted by the administrator
	room := newFakeRoom(t, live, live)
	old := room.add("@old:"+dead.name, "m.room.member", new("@old:"+dead.name), map[string]any{"membership": "join"}, dead)
	room.serve(live)
	live.notaryDocs[dead.name] = dead
	live.notaryBadSig = true

	_, merr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{live.name}})
	require.Nil(t, merr)
	require.False(t, eventStored(t, old.EventID()), "a notary answer not signed by the notary must not vouch for keys")
}

func TestJoinRequiresCreateEventSignature(t *testing.T) {
	live := newFakeFed(t)
	dead := newFakeFed(t)
	dead.kill()
	hs := joinTestEnv(t, live)
	room := newFakeRoom(t, live, dead) // create event signed by an unreachable server
	room.serve(live)

	_, merr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{live.name}})
	require.NotNil(t, merr, "the create event and the join's auth chain must verify")
	require.Equal(t, "M_UNKNOWN", merr.ErrCode)
	require.False(t, joined(t, room.roomID, "@alice:test.local"))
}

func TestJoinResponseBodyIsCapped(t *testing.T) {
	live := newFakeFed(t)
	hs := joinTestEnv(t, live)
	room := newFakeRoom(t, live, live)
	room.serve(live)
	live.sendJoin = func(event json.RawMessage) any {
		return map[string]any{"origin": live.name, "state": []any{}, "auth_chain": []any{}, "pad": strings.Repeat("x", 64<<10)}
	}
	joinSettingsSource = func() joinSettings {
		s := loadJoinSettings()
		s.MaxBodyBytes = 16 << 10
		s.Parallelism = 1
		return s
	}
	_, merr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{live.name}})
	require.NotNil(t, merr)
	require.Equal(t, "M_TOO_LARGE", merr.ErrCode)
}

func TestJoinConcurrencyIsBounded(t *testing.T) {
	hang := newFakeFed(t)
	hs := joinTestEnv(t, hang)
	started := make(chan struct{}, 1)
	release, unblock := joinTestGate(t)
	hang.makeJoinHook = func(w http.ResponseWriter, r *http.Request) bool {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		return true
	}
	room := newFakeRoom(t, hang, hang)
	room.serve(hang)
	joinSettingsSource = func() joinSettings {
		s := loadJoinSettings()
		s.MaxConcurrent = 1
		s.Total = 5 * time.Second
		return s
	}
	joinGateWait = 100 * time.Millisecond
	done := make(chan *MatrixError, 1)
	go func() {
		_, e := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{hang.name}})
		done <- e
	}()
	<-started
	_, merr := hs.Room.JoinRoomContext(context.Background(), "@bob:test.local", room.roomID, &JoinRoomRequest{Servers: []string{hang.name}})
	require.NotNil(t, merr)
	require.Equal(t, "M_LIMIT_EXCEEDED", merr.ErrCode)
	unblock()
	<-done
}

func TestSendJoinStreamingDecoderHandlesV1AndV2(t *testing.T) {
	v2 := `{"origin":"a.example","state":[{"type":"m.room.create"}],"auth_chain":[{"x":1},{"y":2}],"event":{"k":"v"},"members_omitted":true,"servers_in_room":["a"]}`
	res, err := decodeSendJoinResponse(strings.NewReader(v2))
	require.NoError(t, err)
	require.Equal(t, "a.example", res.Origin)
	require.Len(t, res.State, 1)
	require.Len(t, res.AuthChain, 2)
	require.True(t, res.MembersOmitted)
	require.JSONEq(t, `{"k":"v"}`, string(res.Event))
	res, err = decodeSendJoinResponse(strings.NewReader(`[200, ` + v2 + `]`))
	require.NoError(t, err)
	require.Len(t, res.AuthChain, 2)
	_, err = decodeSendJoinResponse(strings.NewReader(`{"state":[`))
	require.Error(t, err)
}

func TestMakeJoinOffersEverySupportedStableRoomVersion(t *testing.T) {
	q := makeJoinVersionQuery()
	for _, v := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"} {
		require.Contains(t, "&"+q+"&", "&ver="+v+"&")
	}
	require.NotContains(t, q, "org.matrix")
}

func TestKeyCacheGetForHonoursRoomVersionValidity(t *testing.T) {
	kc := NewKeyCache()
	pub, _ := ed25519Generate()
	hs := createTestHomeServer(t)
	_ = hs
	validUntil := time.Now().Add(-24 * time.Hour)
	kc.cache["old.example:ed25519:k"] = &KeyCacheEntry{PublicKey: pub, ValidUntil: validUntil}
	before := spec.AsTimestamp(validUntil.Add(-time.Hour))
	after := spec.AsTimestamp(validUntil.Add(time.Hour))
	_, ok := kc.GetFor("old.example", "ed25519:k", before, gmsl.StrictValiditySignatureCheck)
	require.True(t, ok, "v5+: an expired document still vouches for events before valid_until_ts")
	_, ok = kc.GetFor("old.example", "ed25519:k", after, gmsl.StrictValiditySignatureCheck)
	require.False(t, ok, "v5+: not valid at an event after valid_until_ts")
	_, ok = kc.GetFor("old.example", "ed25519:k", after, gmsl.NoStrictValidityCheck)
	require.True(t, ok, "v1-v4 accept expired keys")
	_, ok = kc.GetFor("old.example", "ed25519:k", spec.AsTimestamp(time.Now()), gmsl.StrictValiditySignatureCheck)
	require.False(t, ok, "a key must not authenticate a request made now after it expired")
}

func TestServerKeyDocumentIsWellFormedAndSelfSigned(t *testing.T) {
	hs := createTestHomeServer(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/_matrix/key/v2/server", handleGetServerKey)
	r.GET("/_matrix/key/v2/server/:keyId", handleGetServerKey)
	for _, path := range []string{"/_matrix/key/v2/server", "/_matrix/key/v2/server/" + hs.keyID} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		require.Equal(t, 200, w.Code)
		require.True(t, strings.HasPrefix(w.Header().Get("Content-Type"), "application/json"), w.Header().Get("Content-Type"))
		var doc ServerKeyResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
		require.Equal(t, hs.Config.ServerName, doc.ServerName)
		require.Greater(t, doc.ValidUntil, time.Now().UnixMilli())
		require.Contains(t, doc.VerifyKeys, hs.keyID)
		require.NotNil(t, doc.OldVerifyKeys, "old_verify_keys must be present (possibly empty)")
		require.Contains(t, doc.Signatures[hs.Config.ServerName], hs.keyID)
		require.NoError(t, gmsl.VerifyJSON(hs.Config.ServerName, gmsl.KeyID(hs.keyID), hs.publicKey, bytes.Clone(w.Body.Bytes())))
	}
}

// End to end through the real HTTP handler and the 30s timeout middleware: a
// remote that never answers yields a clear Matrix 504 before the middleware
// would give up, and nothing keeps running afterwards.
func TestJoinHandlerReturnsMatrixTimeoutBeforeHTTPTimeout(t *testing.T) {
	hang := newFakeFed(t)
	hs := joinTestEnv(t, hang)
	hang.makeJoinHook = func(w http.ResponseWriter, r *http.Request) bool { <-r.Context().Done(); return true }
	room := newFakeRoom(t, hang, hang)
	room.serve(hang)
	joinSettingsSource = func() joinSettings {
		s := loadJoinSettings()
		s.Total = 600 * time.Millisecond
		return s
	}
	GlobalHS = hs
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.TimeoutMiddleware(30 * time.Second))
	r.POST("/join/:roomIdOrAlias", func(c *gin.Context) { c.Set("user_id", "@alice:test.local"); handleJoinRoom(c) })
	srv := httptest.NewServer(r)
	defer srv.Close()

	start := time.Now()
	resp, err := http.Post(srv.URL+"/join/"+room.roomID+"?via="+hang.name, "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusGatewayTimeout, resp.StatusCode, string(body))
	var merr MatrixError
	require.NoError(t, json.Unmarshal(body, &merr))
	require.Equal(t, "M_UNKNOWN", merr.ErrCode)
	require.Contains(t, merr.Error, "Timed out")
	require.Less(t, time.Since(start), 3*time.Second)
	require.Equal(t, int64(0), joinAttemptsRunning.Load())
}
