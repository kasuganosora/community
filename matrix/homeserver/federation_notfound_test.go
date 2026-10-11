package homeserver

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/require"
)

func fedStateRouter() *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("origin", "test.local"); c.Next() })
	router.GET("/state/:roomId", handleGetState)
	router.GET("/state_ids/:roomId", handleGetStateIDs)
	router.GET("/event/:eventId", handleGetFederationEvent)
	router.PUT("/send/:txnId", handleFederationSend)
	return router
}

func TestFederationStateUnknownEventIsNotFound(t *testing.T) {
	hs := createTestHomeServer(t)
	room, e := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: "10"})
	require.Nil(t, e)
	router := fedStateRouter()
	for _, path := range []string{"/state_ids/", "/state/"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path+room.RoomID+"?event_id="+url.QueryEscape("$doesnotexist"), nil))
		require.Equal(t, 404, w.Code, w.Body.String())
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Equal(t, "M_NOT_FOUND", body["errcode"])
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/event/$doesnotexist", nil))
	require.Equal(t, 404, w.Code)
	require.Contains(t, w.Body.String(), "M_NOT_FOUND")
}

func TestFederationStateIDsKnownEvent(t *testing.T) {
	hs := createTestHomeServer(t)
	room, e := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: "10", Name: new("Before")})
	require.Nil(t, e)
	name, e := hs.Event.SendRoomEvent(room.RoomID, "@alice:test.local", "m.room.name", nil, map[string]any{"name": "Mid"}, new(""))
	require.Nil(t, e)
	router := fedStateRouter()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/state_ids/"+room.RoomID+"?event_id="+url.QueryEscape(name.EventID), nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	var body struct {
		PDUIDs  []string `json:"pdu_ids"`
		AuthIDs []string `json:"auth_chain_ids"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.NotEmpty(t, body.PDUIDs)
	require.NotNil(t, body.AuthIDs)
	// State at a state event includes that event itself.
	require.Contains(t, body.PDUIDs, name.EventID)
}

func TestFederationSendRejectsUnknownRoomPerPDUNot400(t *testing.T) {
	hs := createTestHomeServer(t)
	_ = hs
	pdu := map[string]any{"room_id": "!unknownroom:other.example", "type": "m.reaction", "sender": "@bob:other.example", "origin_server_ts": 1, "depth": 1,
		"content": map[string]any{"m.relates_to": map[string]any{"rel_type": "m.annotation", "event_id": "$x", "key": "👍"}}}
	payload, _ := json.Marshal(map[string]any{"origin": "test.local", "pdus": []any{pdu}})
	w := httptest.NewRecorder()
	fedStateRouter().ServeHTTP(w, httptest.NewRequest("PUT", "/send/1", bytes.NewReader(payload)))
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

func TestFederationSendAcceptsSignedReaction(t *testing.T) {
	hs := createTestHomeServer(t)
	room, e := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: "10"})
	require.Nil(t, e)
	msg, e := hs.Event.SendRoomEvent(room.RoomID, "@alice:test.local", "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "hi"}, nil)
	require.Nil(t, e)
	reaction := &MatrixEvent{RoomID: room.RoomID, Sender: "@alice:test.local", EventType: "m.reaction", Content: map[string]any{"m.relates_to": map[string]any{"rel_type": "m.annotation", "event_id": msg.EventID, "key": "👍"}}}
	require.NoError(t, buildLocalPDU(db.GetDB(), hs, reaction, "10"))
	wire, err := matrixEventToPDU(reaction)
	require.NoError(t, err)
	payload, _ := json.Marshal(map[string]any{"origin": "test.local", "pdus": []any{wire}})
	w := httptest.NewRecorder()
	fedStateRouter().ServeHTTP(w, httptest.NewRequest("PUT", "/send/2", bytes.NewReader(payload)))
	require.Equal(t, 200, w.Code, w.Body.String())
	var result struct {
		PDUs map[string]map[string]any `json:"pdus"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Contains(t, result.PDUs, reaction.EventID)
	require.Empty(t, result.PDUs[reaction.EventID], "reaction must be accepted: %v", result.PDUs)
}
