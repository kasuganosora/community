package homeserver

import (
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func historicalPDU(t *testing.T, id, version string) gmsl.PDU {
	t.Helper()
	p, err := eventPDUFromDB(db.GetDB(), id, version)
	require.NoError(t, err)
	return p
}
func historicalIDs(events []gmsl.PDU) []string {
	ids := make([]string, 0, len(events))
	for _, e := range events {
		ids = append(ids, e.EventID())
	}
	return ids
}
func historicalTuple(events []gmsl.PDU, eventType, key string) gmsl.PDU {
	for _, p := range events {
		if p.Type() == eventType && p.StateKey() != nil && *p.StateKey() == key {
			return p
		}
	}
	return nil
}

func TestHistoricalStateSnapshotAndSinglePredecessor(t *testing.T) {
	hs := createTestHomeServer(t)
	roomID := createRoomWithUser(t, hs, "@alice:test.local")
	var room MatrixRoom
	require.NoError(t, db.GetDB().Where("room_id = ?", roomID).First(&room).Error)
	empty := ""
	old, e := hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.topic", nil, map[string]any{"topic": "old topic"}, &empty)
	require.Nil(t, e)
	messageID := sendMessage(t, hs, roomID, "@alice:test.local", "snapshot boundary")
	latest, e := hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.topic", nil, map[string]any{"topic": "future topic"}, &empty)
	require.Nil(t, e)
	target := historicalPDU(t, messageID, room.RoomVersion)
	state, err := stateBeforePDU(db.GetDB(), target)
	require.NoError(t, err)
	require.Equal(t, old.EventID, historicalTuple(state, "m.room.topic", "").EventID())
	require.NotContains(t, historicalIDs(state), latest.EventID)
	// Legacy rows without snapshots must reconstruct from the signed DAG, not
	// the newer MatrixRoomState or timestamps on normalized database rows.
	require.NoError(t, db.GetDB().Model(&MatrixEvent{}).Where("room_id = ?", roomID).UpdateColumn("state_snapshot_known", false).Error)
	reconstructed, err := stateBeforePDU(db.GetDB(), target)
	require.NoError(t, err)
	require.ElementsMatch(t, historicalIDs(state), historicalIDs(reconstructed))
}

func makeHistoricalBranch(t *testing.T, hs *HomeServer, roomID, version, eventType string, stateKey *string, content map[string]any, prev []string, authState []gmsl.PDU, depth int64, at time.Time, extraAuth string) gmsl.PDU {
	t.Helper()
	impl, err := gmsl.GetRoomVersion(gmsl.RoomVersion(version))
	require.NoError(t, err)
	builder := impl.NewEventBuilder()
	builder.RoomID = roomID
	builder.SenderID = "@alice:test.local"
	builder.Type = eventType
	builder.StateKey = stateKey
	builder.PrevEvents = prev
	builder.Depth = depth
	require.NoError(t, builder.SetContent(content))
	provider, err := gmsl.NewAuthEvents(authState)
	require.NoError(t, err)
	require.NoError(t, builder.AddAuthEvents(provider))
	if extraAuth != "" {
		ids, ok := builder.AuthEvents.([]string)
		require.True(t, ok)
		builder.AuthEvents = append(ids, extraAuth)
	}
	p, err := builder.Build(at, spec.ServerName(hs.Config.ServerName), gmsl.KeyID(hs.keyID), hs.privateKey)
	require.NoError(t, err)
	return p
}

func TestHistoricalStateForkUsesRoomVersionResolution(t *testing.T) {
	for _, version := range []string{"1", "10", "11", "12"} {
		t.Run(version, func(t *testing.T) {
			hs := createTestHomeServer(t)
			room, e := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: version})
			require.Nil(t, e)
			state, err := currentStatePDUs(db.GetDB(), room.RoomID, version)
			require.NoError(t, err)
			var last MatrixEvent
			require.NoError(t, db.GetDB().Where("room_id = ?", room.RoomID).Order("depth DESC").First(&last).Error)
			empty := ""
			at := time.UnixMilli(last.OriginServerTS + 1000)
			left := makeHistoricalBranch(t, hs, room.RoomID, version, "m.room.topic", &empty, map[string]any{"topic": "left"}, []string{last.EventID}, state, last.Depth+1, at, "")
			right := makeHistoricalBranch(t, hs, room.RoomID, version, "m.room.topic", &empty, map[string]any{"topic": "right"}, []string{last.EventID}, state, last.Depth+1, at.Add(time.Second), "")
			require.NoError(t, db.GetDB().Session(&gorm.Session{SkipHooks: true}).Create(pduToMatrixEvent(left)).Error)
			require.NoError(t, db.GetDB().Session(&gorm.Session{SkipHooks: true}).Create(pduToMatrixEvent(right)).Error)
			merge := makeHistoricalBranch(t, hs, room.RoomID, version, "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "merge"}, []string{left.EventID(), right.EventID()}, state, last.Depth+2, at.Add(2*time.Second), "")
			actual, err := stateBeforePDU(db.GetDB(), merge)
			require.NoError(t, err)
			branches := [][]gmsl.PDU{append(append([]gmsl.PDU{}, state...), left), append(append([]gmsl.PDU{}, state...), right)}
			chain, err := completeAuthChain(db.GetDB(), append(append([]gmsl.PDU{}, state...), left, right), version)
			require.NoError(t, err)
			// v2.1 traverses conflicted state roots using its auth-event index.
			// Include roots as well as their full transitive auth dependencies.
			resolutionEvents := append(append([]gmsl.PDU{}, chain...), state...)
			resolutionEvents = append(resolutionEvents, left, right)
			expected, err := gmsl.ResolveConflictsNew(gmsl.RoomVersion(version), branches, resolutionEvents, matrixUserIDForSender, func(string) bool { return false })
			require.NoError(t, err)
			require.ElementsMatch(t, historicalIDs(expected), historicalIDs(actual))
			winner := historicalTuple(actual, "m.room.topic", "")
			require.NotNil(t, winner)
			require.Contains(t, []string{left.EventID(), right.EventID()}, winner.EventID())
		})
	}
}

func TestHistoricalStateMissingAndCrossRoomFailClosed(t *testing.T) {
	hs := createTestHomeServer(t)
	room := createRoomWithUser(t, hs, "@alice:test.local")
	other := createRoomWithUser(t, hs, "@alice:test.local")
	var roomRow MatrixRoom
	require.NoError(t, db.GetDB().Where("room_id = ?", room).First(&roomRow).Error)
	version := roomRow.RoomVersion
	state, err := currentStatePDUs(db.GetDB(), room, version)
	require.NoError(t, err)
	var otherEvent MatrixEvent
	require.NoError(t, db.GetDB().Where("room_id = ?", other).First(&otherEvent).Error)
	for _, tc := range []struct{ name, prev string }{{"missing predecessor", "$unavailable"}, {"cross-room predecessor", otherEvent.EventID}} {
		t.Run(tc.name, func(t *testing.T) {
			p := makeHistoricalBranch(t, hs, room, version, "m.room.message", nil, map[string]any{"body": tc.name, "msgtype": "m.text"}, []string{tc.prev}, state, 100, time.Now(), "")
			_, err := stateBeforePDU(db.GetDB(), p)
			require.Error(t, err)
		})
	}
	target := historicalPDU(t, sendMessage(t, hs, room, "@alice:test.local", "snapshot validation"), version)
	for _, tc := range []struct {
		name string
		ids  []string
	}{{"missing snapshot material", []string{"$missing-state"}}, {"cross-room snapshot", []string{otherEvent.EventID}}, {"self-containing snapshot", []string{target.EventID()}}, {"empty non-create snapshot", []string{}}} {
		t.Run(tc.name, func(t *testing.T) {
			row := MatrixEvent{EventID: target.EventID(), StateBefore: tc.ids, StateSnapshotKnown: true}
			require.NoError(t, db.GetDB().Model(&MatrixEvent{}).Where("event_id = ?", target.EventID()).Select("state_before", "state_snapshot_known").Updates(&row).Error)
			_, err := stateBeforePDU(db.GetDB(), target)
			require.Error(t, err)
		})
	}
}

func TestHistoricalStateForkRequiresEntireAuthChain(t *testing.T) {
	hs := createTestHomeServer(t)
	room, e := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: "10"})
	require.Nil(t, e)
	state, err := currentStatePDUs(db.GetDB(), room.RoomID, "10")
	require.NoError(t, err)
	var last MatrixEvent
	require.NoError(t, db.GetDB().Where("room_id = ?", room.RoomID).Order("depth DESC").First(&last).Error)
	empty := ""
	at := time.Now()
	left := makeHistoricalBranch(t, hs, room.RoomID, "10", "m.room.topic", &empty, map[string]any{"topic": "missing auth"}, []string{last.EventID}, state, last.Depth+1, at, "$missing-auth")
	right := makeHistoricalBranch(t, hs, room.RoomID, "10", "m.room.topic", &empty, map[string]any{"topic": "other"}, []string{last.EventID}, state, last.Depth+1, at.Add(time.Second), "")
	require.NoError(t, db.GetDB().Create(pduToMatrixEvent(left)).Error)
	require.NoError(t, db.GetDB().Create(pduToMatrixEvent(right)).Error)
	merge := makeHistoricalBranch(t, hs, room.RoomID, "10", "m.room.message", nil, map[string]any{"body": "merge", "msgtype": "m.text"}, []string{left.EventID(), right.EventID()}, state, last.Depth+2, at.Add(2*time.Second), "")
	_, err = stateBeforePDU(db.GetDB(), merge)
	require.ErrorContains(t, err, "$missing-auth")
}

type historicalLoopPDU struct{ gmsl.PDU }

func (p historicalLoopPDU) PrevEventIDs() []string { return []string{p.EventID()} }
func TestHistoricalStateTraversalGuards(t *testing.T) {
	hs := createTestHomeServer(t)
	room := createRoomWithUser(t, hs, "@alice:test.local")
	var row MatrixRoom
	require.NoError(t, db.GetDB().Where("room_id = ?", room).First(&row).Error)
	p := historicalPDU(t, sendMessage(t, hs, room, "@alice:test.local", "cycle boundary"), row.RoomVersion)
	require.NoError(t, db.GetDB().Model(&MatrixEvent{}).Where("event_id = ?", p.EventID()).UpdateColumn("state_snapshot_known", false).Error)
	_, err := stateBeforePDU(db.GetDB(), historicalLoopPDU{p})
	require.ErrorContains(t, err, "cycle")
	reader := historicalStateReader{}
	_, err = reader.stateBefore(p, historicalStateMaxDepth+1)
	require.ErrorContains(t, err, "depth")
}

type historicalWidePDU struct{ gmsl.PDU }

func (p historicalWidePDU) PrevEventIDs() []string {
	return make([]string, historicalStateMaxPredecessors+1)
}

func TestHistoricalStateResourceLimits(t *testing.T) {
	hs := createTestHomeServer(t)
	room := createRoomWithUser(t, hs, "@alice:test.local")
	var row MatrixRoom
	require.NoError(t, db.GetDB().Where("room_id = ?", room).First(&row).Error)
	p := historicalPDU(t, sendMessage(t, hs, room, "@alice:test.local", "resource boundary"), row.RoomVersion)
	snapshot := MatrixEvent{StateBefore: make([]string, historicalStateMaxEvents+1), StateSnapshotKnown: true}
	require.NoError(t, db.GetDB().Model(&MatrixEvent{}).Where("event_id = ?", p.EventID()).Select("state_before", "state_snapshot_known").Updates(&snapshot).Error)
	_, err := stateBeforePDU(db.GetDB(), p)
	require.ErrorContains(t, err, "snapshot exceeds event limit")
	require.NoError(t, db.GetDB().Model(&MatrixEvent{}).Where("event_id = ?", p.EventID()).UpdateColumn("state_snapshot_known", false).Error)
	_, err = stateBeforePDU(db.GetDB(), historicalWidePDU{p})
	require.ErrorContains(t, err, "predecessor limit")
}
