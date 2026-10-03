package homeserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/kasuganosora/community/db"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type localPDUVerifier struct{ hs *HomeServer }

func (v localPDUVerifier) VerifyJSONs(_ context.Context, requests []gmsl.VerifyJSONRequest) ([]gmsl.VerifyJSONResult, error) {
	results := make([]gmsl.VerifyJSONResult, len(requests))
	for i, r := range requests {
		results[i].Error = gmsl.VerifyJSON(string(r.ServerName), gmsl.KeyID(v.hs.keyID), v.hs.publicKey, r.Message)
	}
	return results, nil
}

func TestLocalRoomPDUsHaveAuthorizedSignedDAG(t *testing.T) {
	hs := createTestHomeServer(t)
	for _, version := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"} {
		t.Run(version, func(t *testing.T) {
			room, e := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: version})
			require.Nil(t, e)
			var rows []MatrixEvent
			require.NoError(t, db.GetDB().Where("room_id = ?", room.RoomID).Order("depth ASC").Find(&rows).Error)
			require.GreaterOrEqual(t, len(rows), 5)
			seen := map[string]gmsl.PDU{}
			currentState := map[string]string{}
			for i, row := range rows {
				require.NotEmpty(t, row.FederationJSON)
				require.True(t, row.StateSnapshotKnown)
				expectedState := []string{}
				for _, id := range currentState {
					expectedState = append(expectedState, id)
				}
				require.ElementsMatch(t, expectedState, row.StateBefore)
				if row.StateKey != nil {
					currentState[string(row.EventType)+"\x00"+*row.StateKey] = row.EventID
				}
				pdu, err := parseAndValidatePDU(version, []byte(row.FederationJSON))
				require.NoError(t, err)
				assert.Equal(t, row.EventID, pdu.EventID())
				require.NoError(t, gmsl.VerifyEventSignatures(context.Background(), pdu, localPDUVerifier{hs}, matrixUserIDForSender))
				auth := []gmsl.PDU{}
				for _, id := range pdu.AuthEventIDs() {
					a, ok := seen[id]
					require.True(t, ok, "auth must precede event")
					auth = append(auth, a)
				}
				provider, err := gmsl.NewAuthEvents(auth)
				require.NoError(t, err)
				require.NoError(t, gmsl.Allowed(pdu, provider, matrixUserIDForSender))
				for _, id := range pdu.PrevEventIDs() {
					_, ok := seen[id]
					require.True(t, ok, "previous event must be present")
				}
				if i == 0 {
					assert.Empty(t, pdu.PrevEventIDs())
				} else {
					assert.NotEmpty(t, pdu.PrevEventIDs())
				}
				assert.NotEmpty(t, row.Hashes["sha256"])
				seen[pdu.EventID()] = pdu
			}
			assert.Equal(t, EventTypeRoomMember, rows[1].EventType, "creator must join before sending power levels")
			txn := "stable-client-txn"
			sent, err := hs.Event.SendRoomEventFromDevice(room.RoomID, "@alice:test.local", "device-a", "m.room.message", &txn, map[string]any{"msgtype": "m.text", "body": "signed <message> & 🎉"}, nil)
			require.Nil(t, err)
			again, err := hs.Event.SendRoomEventFromDevice(room.RoomID, "@alice:test.local", "device-a", "m.room.message", &txn, map[string]any{"msgtype": "m.text", "body": "must not create twice"}, nil)
			require.Nil(t, err)
			assert.Equal(t, sent.EventID, again.EventID)
			var message MatrixEvent
			require.NoError(t, db.GetDB().Where("event_id = ?", sent.EventID).First(&message).Error)
			wire, err2 := buildEventPDU(&message)
			require.NoError(t, err2)
			pdu, err2 := parseAndValidatePDU(version, wire)
			require.NoError(t, err2)
			require.NoError(t, gmsl.VerifyEventSignatures(context.Background(), pdu, localPDUVerifier{hs}, matrixUserIDForSender))
			var content map[string]any
			require.NoError(t, json.Unmarshal(pdu.Content(), &content))
			assert.Equal(t, "signed <message> & 🎉", content["body"])
		})
	}
}

func TestLegacyRoomKeepsLocalHistoryWithoutInventingPDU(t *testing.T) {
	hs := createTestHomeServer(t)
	room := MatrixRoom{RoomID: "!legacy-local:test.local", RoomVersion: "10", Creator: "@alice:test.local"}
	require.NoError(t, db.GetDB().Create(&room).Error)
	// A pre-existing unsigned UUID event is historical data, not a valid PDU.
	old := MatrixEvent{EventID: "$legacy-uuid", RoomID: room.RoomID, Sender: "@alice:test.local", EventType: EventTypeRoomCreate, Content: map[string]any{"creator": "@alice:test.local"}}
	require.NoError(t, db.GetDB().Session(&gorm.Session{SkipHooks: true}).Create(&old).Error)
	require.NoError(t, db.GetDB().Create(&MatrixRoomMember{RoomID: room.RoomID, UserID: "@alice:test.local", Membership: MembershipJoin}).Error)
	sent, e := hs.Event.SendRoomEvent(room.RoomID, "@alice:test.local", "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "legacy local works"}, nil)
	require.Nil(t, e)
	var stored MatrixEvent
	require.NoError(t, db.GetDB().Where("event_id = ?", sent.EventID).First(&stored).Error)
	assert.Equal(t, "legacy local works", stored.Content["body"])
	require.True(t, stored.StateSnapshotKnown)
	page, pageErr := hs.Event.GetRoomMessages(room.RoomID, "@alice:test.local", &GetRoomMessagesRequest{Dir: "b", Limit: 20, Filter: `{"lazy_load_members":false}`})
	require.Nil(t, pageErr)
	found := false
	for _, event := range page.Chunk {
		if event.EventID == stored.EventID {
			found = true
		}
	}
	require.True(t, found)
	syncResponse, syncErr := hs.Sync.Sync("@alice:test.local", "LEGACYDEVICE", &SyncRequest{})
	require.Nil(t, syncErr)
	require.NotNil(t, syncResponse.Rooms.Join[room.RoomID])
	found = false
	for _, event := range syncResponse.Rooms.Join[room.RoomID].Timeline.Events {
		if event.EventID == stored.EventID {
			found = true
		}
	}
	require.True(t, found)

	_, err := matrixEventToPDU(&stored)
	assert.ErrorContains(t, err, "no verifiable federation PDU")
	require.NoError(t, db.GetDB().Where("event_id = ?", "$legacy-uuid").First(&MatrixEvent{}).Error)
}

func TestRedactionPreservesSignedIdentityAcrossRoomVersions(t *testing.T) {
	hs := createTestHomeServer(t)
	for _, version := range []string{"9", "10", "11"} {
		t.Run(version, func(t *testing.T) {
			room, e := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: version})
			require.Nil(t, e)
			id := sendMessage(t, hs, room.RoomID, "@alice:test.local", "secret")
			response, e := hs.Event.RedactEvent(room.RoomID, id, "@alice:test.local", "remove", nil)
			require.Nil(t, e)
			var redaction, target MatrixEvent
			require.NoError(t, db.GetDB().Where("event_id = ?", response.EventID).First(&redaction).Error)
			require.NoError(t, db.GetDB().Where("event_id = ?", id).First(&target).Error)
			require.Empty(t, target.Content)
			for _, row := range []MatrixEvent{redaction, target} {
				p, err := parseAndValidatePDU(version, []byte(row.FederationJSON))
				require.NoError(t, err)
				require.Equal(t, row.EventID, p.EventID())
				require.NoError(t, gmsl.VerifyEventSignatures(context.Background(), p, localPDUVerifier{hs}, matrixUserIDForSender))
				if row.EventID == redaction.EventID {
					require.Equal(t, id, pduRedactionTarget(p))
				}
			}
		})
	}
}

func TestParsePDURejectsContentHashTampering(t *testing.T) {
	hs := createTestHomeServer(t)
	room := createRoomWithUser(t, hs, "@alice:test.local")
	id := sendMessage(t, hs, room, "@alice:test.local", "original")
	var row MatrixEvent
	require.NoError(t, db.GetDB().Where("event_id = ?", id).First(&row).Error)
	wire, err := matrixEventToPDU(&row)
	require.NoError(t, err)
	wire["content"].(map[string]any)["body"] = "tampered"
	raw, err := json.Marshal(wire)
	require.NoError(t, err)
	_, err = parseAndValidatePDU("10", raw)
	require.ErrorContains(t, err, "content hash mismatch")
}

func TestLocalPDUDoesNotDowngradeDatabaseFailuresToLegacy(t *testing.T) {
	hs := createTestHomeServer(t)
	room := createRoomWithUser(t, hs, "@alice:test.local")
	database := db.GetDB()
	injected := errors.New("create-event lookup unavailable")
	require.NoError(t, database.Callback().Query().Before("gorm:query").Register("test:failed-create-lookup", func(tx *gorm.DB) {
		if tx.Statement.Table == "matrix_events" {
			tx.AddError(injected)
		}
	}))
	t.Cleanup(func() { _ = database.Callback().Query().Remove("test:failed-create-lookup") })
	event := MatrixEvent{RoomID: room, Sender: "@alice:test.local", EventType: EventTypeRoomMessage, Content: map[string]any{"body": "must not persist unsigned"}}
	require.ErrorIs(t, database.Set("matrix:signer", hs).Create(&event).Error, injected)
}
