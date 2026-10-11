package homeserver

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/stretchr/testify/require"
)

// Build a remote v12 auth chain independently of Community's room creation so
// the join importer must handle the actual wire shape (domainless ID and no
// room_id in the create PDU).
func v12RemoteJoinFixture(t *testing.T) (*HomeServer, *HomeServer, string, []gmsl.PDU, gmsl.PDU) {
	t.Helper()
	hs := createTestHomeServer(t)
	remote := minimalHS("v12-remote.example")
	GetKeyCache().Set(remote.Config.ServerName, remote.keyID, remote.publicKey, time.Now().Add(time.Hour))
	version, err := gmsl.GetRoomVersion("12")
	require.NoError(t, err)
	var state []gmsl.PDU
	roomID := ""
	add := func(sender, kind string, key *string, content map[string]any, signer *HomeServer) gmsl.PDU {
		builder := version.NewEventBuilder()
		builder.RoomID = roomID
		builder.SenderID = sender
		builder.Type = kind
		builder.StateKey = key
		builder.Depth = int64(len(state) + 1)
		if len(state) > 0 {
			builder.PrevEvents = []string{state[len(state)-1].EventID()}
		}
		require.NoError(t, builder.SetContent(content))
		auth, authErr := gmsl.NewAuthEvents(state)
		require.NoError(t, authErr)
		require.NoError(t, builder.AddAuthEvents(auth))
		p, buildErr := builder.Build(time.Now(), spec.ServerName(signer.Config.ServerName), gmsl.KeyID(signer.keyID), signer.privateKey)
		require.NoError(t, buildErr)
		return p
	}
	creator := "@owner:v12-remote.example"
	create := add(creator, "m.room.create", new(""), map[string]any{"room_version": "12"}, remote)
	roomID = create.RoomID().String()
	state = append(state, create)
	state = append(state, add(creator, "m.room.member", new(creator), map[string]any{"membership": "join"}, remote))
	state = append(state, add(creator, "m.room.join_rules", new(""), map[string]any{"join_rule": "public"}, remote))
	joining := "@alice:test.local"
	join := add(joining, "m.room.member", new(joining), map[string]any{"membership": "join"}, hs)
	return hs, remote, roomID, state, join
}

func TestFederationV12ExternalJoinAndIncomingEvents(t *testing.T) {
	hs, remote, roomID, state, join := v12RemoteJoinFixture(t)
	require.NotContains(t, roomID, ":")
	raw := make([]any, 0, len(state))
	for _, p := range state {
		wire, err := wirePDU(p)
		require.NoError(t, err)
		raw = append(raw, wire)
	}
	createWire := raw[0].(map[string]any)
	require.NotContains(t, createWire, "room_id")
	response := map[string]any{"_verified_room_version": "12", "_local_join": json.RawMessage(join.JSON()), "state": raw, "auth_chain": raw}
	require.NoError(t, hs.Room.processFederationJoinResponse(roomID, "@alice:test.local", response))
	var room MatrixRoom
	require.NoError(t, db.GetDB().Where("room_id = ?", roomID).First(&room).Error)
	require.Equal(t, "12", room.RoomVersion)
	require.Equal(t, "@owner:v12-remote.example", room.Creator)
	id, err := storeFederationPDU(hs, createWire)
	require.NoError(t, err)
	require.Equal(t, state[0].EventID(), id)
	require.NotContains(t, createWire, "room_id", "routing must not mutate signed JSON")
	message := &MatrixEvent{RoomID: roomID, Sender: "@owner:v12-remote.example", EventType: EventTypeRoomMessage, Content: map[string]any{"msgtype": "m.text", "body": "hello v12"}}
	require.NoError(t, buildLocalPDU(db.GetDB(), remote, message, "12"))
	wire, err := matrixEventToPDU(message)
	require.NoError(t, err)
	_, err = storeFederationPDU(hs, wire)
	require.NoError(t, err)
	var stored MatrixEvent
	require.NoError(t, db.GetDB().Where("event_id = ?", message.EventID).First(&stored).Error)
	require.Equal(t, roomID, stored.RoomID)
	require.Equal(t, "hello v12", stored.Content["body"])
}

func TestFederationV12RoutingRejectsUnknownAndUnrelatedRooms(t *testing.T) {
	hs, _, roomID, state, _ := v12RemoteJoinFixture(t)
	wire, err := wirePDU(state[0])
	require.NoError(t, err)
	resolved, err := federationEventRoomID(wire)
	require.NoError(t, err)
	require.Equal(t, roomID, resolved)
	_, err = storeFederationPDU(hs, wire)
	require.Error(t, err, "an unsolicited create cannot establish a room")
	require.NoError(t, db.GetDB().Create(&MatrixRoom{RoomID: "!different", RoomVersion: "12"}).Error)
	wire["room_id"] = "!different"
	_, err = storeFederationPDU(hs, wire)
	require.Error(t, err)
	_, err = federationEventRoomID(map[string]any{"type": "m.room.message", "content": map[string]any{"room_version": "12"}})
	require.Error(t, err)
}

func TestFederationV12ForkBanRejectsMergedMessage(t *testing.T) {
	hs := createTestHomeServer(t)
	room, matrixErr := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: "12"})
	require.Nil(t, matrixErr)
	_, matrixErr = hs.Room.JoinRoom("@bob:test.local", room.RoomID, nil)
	require.Nil(t, matrixErr)
	state, err := currentStatePDUs(db.GetDB(), room.RoomID, "12")
	require.NoError(t, err)
	var last MatrixEvent
	require.NoError(t, db.GetDB().Where("room_id = ?", room.RoomID).Order("depth DESC").First(&last).Error)
	at := time.UnixMilli(last.OriginServerTS + 1000)
	ban := makeHistoricalBranch(t, hs, room.RoomID, "12", "m.room.member", new("@bob:test.local"), map[string]any{"membership": "ban"}, []string{last.EventID}, state, last.Depth+1, at, "")
	right := makeHistoricalBranch(t, hs, room.RoomID, "12", "m.room.topic", new(""), map[string]any{"topic": "concurrent topic"}, []string{last.EventID}, state, last.Depth+1, at.Add(time.Second), "")
	require.NoError(t, db.GetDB().Create(pduToMatrixEvent(ban)).Error)
	require.NoError(t, db.GetDB().Create(pduToMatrixEvent(right)).Error)
	version, err := gmsl.GetRoomVersion("12")
	require.NoError(t, err)
	for _, sender := range []string{"@bob:test.local", "@alice:test.local"} {
		builder := version.NewEventBuilder()
		builder.RoomID = room.RoomID
		builder.SenderID = sender
		builder.Type = "m.room.message"
		builder.PrevEvents = []string{ban.EventID(), right.EventID()}
		builder.Depth = last.Depth + 2
		require.NoError(t, builder.SetContent(map[string]any{"msgtype": "m.text", "body": "merged message"}))
		provider, err := gmsl.NewAuthEvents(state)
		require.NoError(t, err)
		require.NoError(t, builder.AddAuthEvents(provider))
		message, err := builder.Build(at.Add(2*time.Second), spec.ServerName(hs.Config.ServerName), gmsl.KeyID(hs.keyID), hs.privateKey)
		require.NoError(t, err)
		resolved, err := stateBeforePDU(db.GetDB(), message)
		require.NoError(t, err)
		member := historicalTuple(resolved, "m.room.member", "@bob:test.local")
		require.NotNil(t, member)
		require.Equal(t, ban.EventID(), member.EventID(), "v12 state resolution must select the ban over the other branch's earlier join")
		wire, err := wirePDU(message)
		require.NoError(t, err)
		_, err = storeFederationPDU(hs, wire)
		if sender == "@bob:test.local" {
			require.Error(t, err, "banned user's merged message must not be accepted")
			var count int64
			require.NoError(t, db.GetDB().Model(&MatrixEvent{}).Where("event_id = ?", message.EventID()).Count(&count).Error)
			require.Zero(t, count)
		} else {
			require.NoError(t, err, "creator remains authorized on the resolved branches")
			var saved MatrixEvent
			require.NoError(t, db.GetDB().Where("event_id = ?", message.EventID()).First(&saved).Error)
			require.True(t, saved.StateSnapshotKnown)
			require.Contains(t, saved.StateBefore, ban.EventID())
		}
	}
}
