package homeserver

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestFederationRequiresRequestSignature(t *testing.T) {
	hs := createTestHomeServer(t)
	hs.Config.Enabled = true
	router := gin.New()
	RegisterFederationRoutes(router)
	for _, path := range []string{"/_matrix/federation/v1/state/!room:test.local", "/_matrix/federation/v1/make_join/!room:test.local/@alice:test.local"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		require.Equal(t, 401, w.Code)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/_matrix/federation/v1/version", nil))
	require.Equal(t, 200, w.Code)
}
func TestFederationRejectsMissingAndTamperedSignatures(t *testing.T) {
	hs := createTestHomeServer(t)
	room, e := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: "10"})
	require.Nil(t, e)
	var event MatrixEvent
	require.NoError(t, db.GetDB().Where("room_id = ?", room.RoomID).Order("depth DESC").First(&event).Error)
	valid, err := parseAndValidatePDU("10", []byte(event.FederationJSON))
	require.NoError(t, err)
	require.NoError(t, validateIncomingPDU(context.Background(), hs, valid, true))
	for _, mode := range []string{"missing", "tampered", "unknown-key"} {
		t.Run(mode, func(t *testing.T) {
			var wire map[string]any
			require.NoError(t, json.Unmarshal([]byte(event.FederationJSON), &wire))
			switch mode {
			case "missing":
				delete(wire, "signatures")
			case "tampered":
				wire["sender"] = "@mallory:test.local"
			case "unknown-key":
				wire["signatures"] = map[string]any{"test.local": map[string]any{"ed25519:unknown": "AAAAAAAA"}}
			}
			raw, _ := json.Marshal(wire)
			p, err := parseAndValidatePDU("10", raw)
			if err == nil {
				require.Error(t, validateIncomingPDU(context.Background(), hs, p, true))
			}
		})
	}
}

func TestFederationRejectsSignedUnauthorizedSender(t *testing.T) {
	hs := createTestHomeServer(t)
	room, e := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: "10"})
	require.Nil(t, e)
	state, err := currentStatePDUs(db.GetDB(), room.RoomID, "10")
	require.NoError(t, err)
	auth, err := gmsl.NewAuthEvents(state)
	require.NoError(t, err)
	version, err := gmsl.GetRoomVersion("10")
	require.NoError(t, err)
	builder := version.NewEventBuilder()
	builder.RoomID = room.RoomID
	builder.SenderID = "@mallory:test.local"
	builder.Type = "m.room.message"
	builder.Depth = 100
	require.NoError(t, builder.SetContent(map[string]any{"msgtype": "m.text", "body": "unauthorized"}))
	require.NoError(t, builder.AddAuthEvents(auth))
	p, err := builder.Build(time.Now(), spec.ServerName(hs.Config.ServerName), gmsl.KeyID(hs.keyID), hs.privateKey)
	require.NoError(t, err)
	require.NoError(t, gmsl.VerifyEventSignatures(context.Background(), p, federationJSONVerifier{hs}, federationUserID))
	require.Error(t, validateIncomingPDU(context.Background(), hs, p, true))
	wire, err := wirePDU(p)
	require.NoError(t, err)
	_, err = storeFederationPDU(hs, wire)
	require.Error(t, err)
	var n int64
	require.NoError(t, db.GetDB().Model(&MatrixEvent{}).Where("event_id = ?", p.EventID()).Count(&n).Error)
	require.Zero(t, n)
}

func TestFederationRedactionEnforcesTargetPermission(t *testing.T) {
	hs := createTestHomeServer(t)
	room, e := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: "10"})
	require.Nil(t, e)
	_, e = hs.Room.JoinRoom("@bob:test.local", room.RoomID, &JoinRoomRequest{})
	require.Nil(t, e)
	message := &MatrixEvent{RoomID: room.RoomID, Sender: "@alice:test.local", EventType: EventTypeRoomMessage, Content: map[string]any{"msgtype": "m.text", "body": "preserve until authorized"}}
	require.NoError(t, buildLocalPDU(db.GetDB(), hs, message, "10"))
	wire, err := matrixEventToPDU(message)
	require.NoError(t, err)
	_, err = storeFederationPDU(hs, wire)
	require.NoError(t, err)
	for _, sender := range []string{"@bob:test.local", "@alice:test.local"} {
		redact := &MatrixEvent{RoomID: room.RoomID, Sender: sender, EventType: EventTypeRoomRedaction, Redacts: &message.EventID, Content: map[string]any{}}
		require.NoError(t, buildLocalPDU(db.GetDB(), hs, redact, "10"))
		wire, err = matrixEventToPDU(redact)
		require.NoError(t, err)
		_, err = storeFederationPDU(hs, wire)
		require.NoError(t, err)
		var row MatrixEvent
		require.NoError(t, db.GetDB().Where("event_id = ?", message.EventID).First(&row).Error)
		if sender == "@bob:test.local" {
			require.Equal(t, "preserve until authorized", row.Content["body"])
		} else {
			require.Empty(t, row.Content)
			p, err := parseAndValidatePDU("10", []byte(row.FederationJSON))
			require.NoError(t, err)
			require.NoError(t, gmsl.VerifyEventSignatures(context.Background(), p, federationJSONVerifier{hs}, federationUserID))
		}
	}
}

func TestFederationStateEndpointUsesRequestedEventSnapshot(t *testing.T) {
	hs := createTestHomeServer(t)
	room, e := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: "10", Name: new("Before")})
	require.Nil(t, e)
	message, e := hs.Event.SendRoomEvent(room.RoomID, "@alice:test.local", "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "snapshot anchor"}, nil)
	require.Nil(t, e)
	_, e = hs.Event.SendRoomEvent(room.RoomID, "@alice:test.local", "m.room.name", nil, map[string]any{"name": "After"}, new(""))
	require.Nil(t, e)
	_, e = hs.Room.JoinRoom("@bob:test.local", room.RoomID, &JoinRoomRequest{})
	require.Nil(t, e)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("origin", "test.local"); c.Next() })
	router.GET("/state/:roomId", handleGetState)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/state/"+room.RoomID+"?event_id="+url.QueryEscape(message.EventID), nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	var result struct {
		PDUs []map[string]any `json:"pdus"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	found := false
	for _, p := range result.PDUs {
		if p["type"] == "m.room.name" {
			found = true
			require.Equal(t, "Before", p["content"].(map[string]any)["name"])
		}
		if p["type"] == "m.room.member" {
			require.NotEqual(t, "@bob:test.local", p["state_key"])
		}
	}
	require.True(t, found)
}

func TestFederationTransactionDoesNotAcknowledgeMalformedPDU(t *testing.T) {
	hs := createTestHomeServer(t)
	room, e := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: "10"})
	require.Nil(t, e)
	payload, _ := json.Marshal(map[string]any{"origin": "test.local", "pdus": []any{map[string]any{"room_id": room.RoomID, "type": "m.room.message", "sender": "@alice:test.local", "content": map[string]any{"body": "missing hashes"}}}})
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("origin", "test.local"); c.Next() })
	router.PUT("/send", handleFederationSend)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("PUT", "/send", bytes.NewReader(payload)))
	// One bad PDU is rejected individually; it must not fail the whole transaction.
	require.Equal(t, 200, w.Code, w.Body.String())
	var result struct {
		PDUs map[string]map[string]any `json:"pdus"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Len(t, result.PDUs, 1)
	for _, v := range result.PDUs {
		require.NotEmpty(t, v["error"])
	}
}
