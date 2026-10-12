package homeserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/stretchr/testify/require"
)

// Only auth IDs are used for a node whose ancestry is already invalid. An
// embedded nil PDU makes accidental authorization of an orphan fail loudly.
type orphanJoinPDU struct {
	gmsl.PDU
	refs []string
}

func (p orphanJoinPDU) AuthEventIDs() []string { return p.refs }

func TestJoinAuthPruningPropagatesEntireInvalidChain(t *testing.T) {
	for _, criticalTail := range []bool{false, true} {
		events := map[string]gmsl.PDU{}
		state := map[string]bool{}
		previous := "$missing"
		for i := 0; i < 10000; i++ {
			id := fmt.Sprintf("$event%d", i)
			events[id] = orphanJoinPDU{refs: []string{previous}}
			state[id] = true
			previous = id
		}
		critical := map[string]bool{}
		if criticalTail {
			critical[previous] = true
		}
		dropped, err := pruneInvalidJoinAuth(context.Background(), events, state, critical)
		if criticalTail {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, 10000, dropped)
			require.Empty(t, events)
			require.Empty(t, state)
		}
	}
}

func TestJoinAuthPruningRejectsCyclesAndDescendants(t *testing.T) {
	events := map[string]gmsl.PDU{
		"$a":     orphanJoinPDU{refs: []string{"$b"}},
		"$b":     orphanJoinPDU{refs: []string{"$a"}},
		"$child": orphanJoinPDU{refs: []string{"$a"}},
	}
	state := map[string]bool{"$child": true}
	dropped, err := pruneInvalidJoinAuth(context.Background(), events, state, nil)
	require.NoError(t, err)
	require.Equal(t, 3, dropped)
	require.Empty(t, state)
}

func TestSendJoinDecoderRejectsAmbiguousAndTruncatedDocuments(t *testing.T) {
	for _, raw := range []string{
		`[403,{"state":[],"auth_chain":[]}]`,
		`[200,{"state":[],"auth_chain":[]}`,
		`[200,{"state":[],"auth_chain":[]},{}]`,
		`{"state":[],"auth_chain":[]} {}`,
		`{"state":[],"state":[],"auth_chain":[]}`,
		`{"state":[],"auth_chain":[]} garbage`,
	} {
		_, err := decodeSendJoinResponse(strings.NewReader(raw))
		require.Error(t, err, raw)
	}
}

func TestSendJoinDecoderBoundsTinyEventCountAndEventSize(t *testing.T) {
	raw := `{"state":[` + strings.Repeat(`null,`, maxSendJoinEvents) + `null],"auth_chain":[]}`
	_, err := decodeSendJoinResponse(strings.NewReader(raw))
	require.ErrorContains(t, err, "too many")
	raw = `{"state":[{"padding":"` + strings.Repeat("a", 65536) + `"}],"auth_chain":[]}`
	_, err = decodeSendJoinResponse(strings.NewReader(raw))
	require.ErrorContains(t, err, "size limit")
}

func TestJoinPersistenceHonorsCancelledContext(t *testing.T) {
	hs := createTestHomeServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := hs.Room.persistFederationJoinContext(ctx, &preparedFederationJoin{roomID: "!cancelled:test.local", version: "10"})
	require.ErrorIs(t, err, context.Canceled)
	var count int64
	require.NoError(t, db.GetDB().Model(&MatrixRoom{}).Where("room_id = ?", "!cancelled:test.local").Count(&count).Error)
	require.Zero(t, count)
}

func TestJoinDoesNotDropInvalidCurrentSecurityStateToUseDefaults(t *testing.T) {
	for _, kind := range []string{"m.room.power_levels", "m.room.server_acl", "m.room.history_visibility", "m.room.encryption"} {
		t.Run(kind, func(t *testing.T) {
			live := newFakeFed(t)
			hs := joinTestEnv(t, live)
			room := newFakeRoom(t, live, live)
			originalState := append([]gmsl.PDU(nil), room.state...)
			security := room.add("@owner:"+live.name, kind, new(""), map[string]any{}, live)
			room.state = originalState
			room.serve(live)
			original := live.sendJoin
			var wire map[string]any
			require.NoError(t, json.Unmarshal(security.JSON(), &wire))
			delete(wire, "signatures")
			bad, err := json.Marshal(wire)
			require.NoError(t, err)
			live.sendJoin = func(event json.RawMessage) any {
				result := original(event).(map[string]any)
				result["state"] = append(room.wire(), json.RawMessage(bad))
				return result
			}
			_, matrixErr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{live.name}})
			require.NotNil(t, matrixErr, "invalid current security state must not be dropped in favour of permissive defaults")
			require.False(t, joined(t, room.roomID, "@alice:test.local"))
		})
	}
}

func TestJoinOptionalHangingKeyServersHaveBoundedBudget(t *testing.T) {
	live := newFakeFed(t)
	hs := joinTestEnv(t, live)
	room := newFakeRoom(t, live, live)
	optionalIDs := []string{}
	for i := 0; i < 12; i++ {
		slow := newFakeFed(t)
		slow.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
		user := "@old:" + slow.name
		event := room.add(user, "m.room.member", new(user), map[string]any{"membership": "join"}, slow)
		optionalIDs = append(optionalIDs, event.EventID())
	}
	room.serve(live)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_, matrixErr := hs.Room.JoinRoomContext(ctx, "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{live.name}})
	require.Nil(t, matrixErr, "optional offline servers must not consume the join deadline")
	require.Less(t, time.Since(start), 2*time.Second)
	require.True(t, joined(t, room.roomID, "@alice:test.local"))
	for _, id := range optionalIDs {
		require.False(t, eventStored(t, id))
	}
}

func TestJoinExtraSignaturesDoNotFetchUnneededServers(t *testing.T) {
	live := newFakeFed(t)
	noise := newFakeFed(t)
	hs := joinTestEnv(t, live)
	room := newFakeRoom(t, live, live)
	user := "@old:" + live.name
	room.add(user, "m.room.member", new(user), map[string]any{"membership": "join"}, live)
	for i, event := range room.state {
		room.state[i] = event.Sign(noise.name, gmsl.KeyID(noise.hs.keyID), noise.hs.privateKey)
	}
	room.serve(live)
	_, matrixErr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", room.roomID, &JoinRoomRequest{Servers: []string{live.name}})
	require.Nil(t, matrixErr)
	require.Zero(t, noise.count("key"), "extra signature servers must never be prefetched for critical or optional events")
}

func TestJoinRejectsMalformedOrHashTamperedCurrentState(t *testing.T) {
	live := newFakeFed(t)
	hs := joinTestEnv(t, live)
	room := newFakeRoom(t, live, live)
	user := "@alice:test.local"
	local, err := room.builder(user, "m.room.member", new(user), map[string]any{"membership": "join"}).Build(time.Now(), spec.ServerName(hs.Config.ServerName), gmsl.KeyID(hs.keyID), hs.privateKey)
	require.NoError(t, err)
	invalid := []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`{"type":"m.room.power_levels","content":{}}`), json.RawMessage(`{"state_key":"","content":{}}`)}
	for _, kind := range []string{"m.room.power_levels", "m.room.server_acl"} {
		originalState := append([]gmsl.PDU(nil), room.state...)
		event := room.add("@owner:"+live.name, kind, new(""), map[string]any{}, live)
		room.state = originalState
		var wire map[string]any
		require.NoError(t, json.Unmarshal(event.JSON(), &wire))
		wire["content"] = map[string]any{"tampered": "untrusted replacement"}
		raw, err := json.Marshal(wire)
		require.NoError(t, err)
		_, err = parseAndValidatePDU("10", raw)
		require.Error(t, err, "fixture must fail the content hash check, not only signature validation")
		invalid = append(invalid, raw)
	}
	for _, raw := range invalid {
		response := &sendJoinResponse{State: append(room.wire(), raw), AuthChain: room.wire()}
		prepared, err := hs.Room.validateFederationJoin(context.Background(), room.roomID, user, "10", json.RawMessage(local.JSON()), response)
		require.Error(t, err)
		require.Nil(t, prepared, "malformed current state must never be replaced by defaults")
	}
}
