package homeserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/stretchr/testify/require"
)

func TestLocalKnockAndPrivateRejoinAuthorization(t *testing.T) {
	hs := createTestHomeServer(t)
	alice, bob := "@alice:test.local", "@bob:test.local"
	room, merr := hs.Room.CreateRoom(alice, &CreateRoomRequest{Preset: "public_chat", RoomVersion: "10"})
	require.Nil(t, merr)
	_, merr = hs.Room.JoinRoom(bob, room.RoomID, nil)
	require.Nil(t, merr)
	require.Nil(t, hs.Room.LeaveRoom(bob, room.RoomID, nil))
	key := ""
	_, merr = hs.Event.SendRoomEvent(room.RoomID, alice, "m.room.join_rules", nil, map[string]any{"join_rule": "invite"}, &key)
	require.Nil(t, merr)
	_, merr = hs.Room.JoinRoom(bob, room.RoomID, nil)
	require.NotNil(t, merr)
	require.Equal(t, "M_FORBIDDEN", merr.ErrCode, "past membership is not an invitation")
	_, merr = hs.Event.SendRoomEvent(room.RoomID, alice, "m.room.join_rules", nil, map[string]any{"join_rule": "knock"}, &key)
	require.Nil(t, merr)
	id, merr := hs.Room.KnockRoomContext(context.Background(), bob, room.RoomID, nil, "please invite")
	require.Nil(t, merr)
	require.Equal(t, room.RoomID, id)
	var member MatrixRoomMember
	require.NoError(t, db.GetDB().Where("room_id = ? AND user_id = ?", id, bob).First(&member).Error)
	require.Equal(t, MembershipKnock, member.Membership)
	_, merr = hs.Room.JoinRoom(bob, id, nil)
	require.NotNil(t, merr, "a knock is not a successful join")
	var event MatrixEvent
	require.NoError(t, db.GetDB().Where("event_id = ?", member.EventID).First(&event).Error)
	require.NotEmpty(t, event.FederationJSON)
	before := event.EventID
	_, merr = hs.Room.KnockRoomContext(context.Background(), bob, id, nil, "")
	require.Nil(t, merr)
	require.NoError(t, db.GetDB().Where("room_id = ? AND user_id = ?", id, bob).First(&member).Error)
	require.Equal(t, before, member.EventID, "repeat knock is idempotent")
	require.Nil(t, hs.Room.LeaveRoom(bob, id, nil))
	require.NoError(t, db.GetDB().Where("room_id = ? AND user_id = ?", id, bob).First(&member).Error)
	require.Equal(t, MembershipLeave, member.Membership)
}
func TestFederationKnockAcceptedPersistsOnlyPreview(t *testing.T) {
	setupMinimalTestDB(t)
	hs := fullHS("test.local")
	restore := resetGlobalHS(hs)
	defer restore()
	requestCount := 0
	roomID, user := "!knock-hash", "@alice:test.local"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "make_leave") {
			_ = json.NewEncoder(w).Encode(map[string]any{"room_version": "10", "event": map[string]any{"room_id": roomID, "sender": user, "type": "m.room.member", "state_key": user, "content": map[string]any{"membership": "leave"}, "prev_events": []any{}, "auth_events": []any{}, "depth": 2}})
			return
		}
		if strings.Contains(r.URL.Path, "send_leave") {
			var leave map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&leave))
			require.Equal(t, "leave", leave["content"].(map[string]any)["membership"])
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if strings.Contains(r.URL.Path, "make_knock") {
			require.Contains(t, r.URL.Query()["ver"], "12")
			_ = json.NewEncoder(w).Encode(map[string]any{"room_version": "10", "event": map[string]any{"room_id": roomID, "sender": "@wrong:remote", "type": "m.room.member", "state_key": "@wrong:remote", "content": map[string]any{"membership": "join"}, "prev_events": []any{}, "auth_events": []any{}, "depth": 1}})
			return
		}
		require.Contains(t, r.URL.Path, "send_knock")
		require.Equal(t, "PUT", r.Method)
		var wire map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&wire))
		require.Equal(t, user, wire["sender"])
		require.Equal(t, user, wire["state_key"])
		require.Equal(t, "knock", wire["content"].(map[string]any)["membership"])
		require.NotNil(t, wire["signatures"])
		_, _ = w.Write([]byte(`{"knock_room_state":[{"type":"m.room.create","state_key":"","sender":"@owner:remote","content":{"room_version":"10"}},{"type":"m.room.name","state_key":"","sender":"@owner:remote","content":{"name":"Remote knock group"}},{"type":"m.room.join_rules","state_key":"","sender":"@owner:remote","content":{"join_rule":"knock"}}]}`))
	}))
	defer server.Close()
	original := newFederationClient
	newFederationClient = func(h *HomeServer) *FederationClient {
		c := NewFederationClient(h)
		c.fedHTTPClient = server.Client()
		return c
	}
	defer func() { newFederationClient = original }()
	host := strings.TrimPrefix(server.URL, "https://")
	roomID = "!knock:" + host
	id, merr := hs.Room.KnockRoomContext(context.Background(), user, roomID, []string{host}, "")
	require.Nil(t, merr)
	require.Equal(t, roomID, id)
	require.Equal(t, 2, requestCount)
	var room MatrixRoom
	require.NoError(t, db.GetDB().Where("room_id = ?", roomID).First(&room).Error)
	require.Equal(t, "Remote knock group", *room.Name)
	require.Equal(t, host, knockPreviewServer(&room))
	var createCount int64
	require.NoError(t, db.GetDB().Model(&MatrixRoomState{}).Where("room_id = ? AND event_type = ?", roomID, EventTypeRoomCreate).Count(&createCount).Error)
	require.Zero(t, createCount, "stripped preview must not become verified room state")
	member, membershipErr := hs.Room.GetUserMembership(roomID, user)
	require.NoError(t, membershipErr)
	require.Equal(t, MembershipKnock, member)
	_, merr = hs.Room.KnockRoomContext(context.Background(), user, roomID, []string{host}, "")
	require.Nil(t, merr)
	require.Equal(t, 2, requestCount, "accepted requests do not resubmit")
	require.Nil(t, hs.Room.LeaveRoomContext(context.Background(), user, roomID, nil))
	require.Equal(t, 4, requestCount, "withdrawal completes real make_leave/send_leave")
	membership, e := hs.Room.GetUserMembership(roomID, user)
	require.NoError(t, e)
	require.Equal(t, MembershipLeave, membership)
	// Another account already joined this remote room, so a verified local
	// create/state row now exists. That must not turn a second account's knock
	// into an unconfirmed local-only membership update.
	require.NoError(t, db.GetDB().Model(&room).Updates(map[string]any{"creator": "@owner:" + host, "current_state_id": nil}).Error)
	empty := ""
	require.NoError(t, db.GetDB().Create(&MatrixEvent{EventID: "$remote-create", RoomID: roomID, EventType: EventTypeRoomCreate, StateKey: &empty, Sender: "@owner:" + host, Content: map[string]any{"room_version": "10"}, FederationJSON: `{"present":true}`}).Error)
	require.NoError(t, db.GetDB().Create(&MatrixRoomState{RoomID: roomID, EventType: EventTypeRoomCreate, EventID: "$remote-create", StateKey: "", Content: map[string]any{"room_version": "10"}}).Error)
	user = "@second:test.local"
	_, merr = hs.Room.KnockRoomContext(context.Background(), user, roomID, nil, "")
	require.Nil(t, merr)
	require.Equal(t, 6, requestCount, "known remote rooms still confirm make_knock/send_knock, deriving via from verified creator")
	require.NoError(t, db.GetDB().Where("room_id = ?", roomID).First(&room).Error)
	require.Empty(t, knockPreviewServer(&room), "complete room is never downgraded to stripped preview")
	require.Nil(t, hs.Room.LeaveRoomContext(context.Background(), user, roomID, nil))
	require.Equal(t, 8, requestCount, "accepted second knock can be withdrawn on the remote server")
}
func TestFederationKnockRejectedNeverCreatesPendingMembership(t *testing.T) {
	setupMinimalTestDB(t)
	hs := fullHS("test.local")
	restore := resetGlobalHS(hs)
	defer restore()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"errcode":"M_FORBIDDEN","error":"invite only"}`, 403)
	}))
	defer server.Close()
	original := newFederationClient
	newFederationClient = func(h *HomeServer) *FederationClient {
		c := NewFederationClient(h)
		c.fedHTTPClient = server.Client()
		return c
	}
	defer func() { newFederationClient = original }()
	_, merr := hs.Room.KnockRoomContext(context.Background(), "@alice:test.local", "!unknown", []string{strings.TrimPrefix(server.URL, "https://")}, "")
	require.NotNil(t, merr)
	require.Equal(t, "M_FORBIDDEN", merr.ErrCode)
	var n int64
	require.NoError(t, db.GetDB().Model(&MatrixRoomMember{}).Count(&n).Error)
	require.Zero(t, n)
}
func TestInboundFederationKnockAuthenticatesAndReturnsMinimalPreview(t *testing.T) {
	hs := createTestHomeServer(t)
	require.NoError(t, db.GetDB().AutoMigrate(&BridgeFederationDelivery{}))
	room, merr := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: "10", Name: new("Knock group")})
	require.Nil(t, merr)
	key := ""
	_, merr = hs.Event.SendRoomEvent(room.RoomID, "@alice:test.local", "m.room.join_rules", nil, map[string]any{"join_rule": "knock"}, &key)
	require.Nil(t, merr)
	require.NoError(t, db.GetDB().Create(&MatrixRoomMember{RoomID: room.RoomID, UserID: "@resident:other.example", Membership: MembershipJoin}).Error)
	remote := minimalHS("remote.example")
	user := "@bob:remote.example"
	GetKeyCache().Set("remote.example", remote.keyID, remote.publicKey, time.Now().Add(time.Hour))
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("origin", "remote.example"); c.Next() })
	router.GET("/make/:roomId/:userId", handleMakeKnock)
	router.PUT("/send/:roomId/:eventId", handleSendKnock)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/make/"+url.PathEscape(room.RoomID)+"/"+url.PathEscape(user)+"?ver=10", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	var template struct {
		Event json.RawMessage `json:"event"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &template))
	impl, err := gmsl.GetRoomVersion("10")
	require.NoError(t, err)
	builder := impl.NewEventBuilder()
	require.NoError(t, json.Unmarshal(template.Event, builder))
	p, err := builder.Build(time.Now(), spec.ServerName(remote.Config.ServerName), gmsl.KeyID(remote.keyID), remote.privateKey)
	require.NoError(t, err)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("PUT", "/send/"+url.PathEscape(room.RoomID)+"/"+url.PathEscape(p.EventID()), strings.NewReader(string(p.JSON()))))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "Knock group")
	require.NotContains(t, w.Body.String(), "m.room.member")
	membership, err := hs.Room.GetUserMembership(room.RoomID, user)
	require.NoError(t, err)
	require.Equal(t, MembershipKnock, membership)
	var outbox int64
	require.NoError(t, db.GetDB().Model(&BridgeFederationDelivery{}).Where("event_id = ?", p.EventID()).Count(&outbox).Error)
	require.Positive(t, outbox, "accepted membership changes are durably federated")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/make/"+url.PathEscape(room.RoomID)+"/"+url.PathEscape("@attacker:other.example")+"?ver=10", nil))
	require.Equal(t, 403, w.Code)
}
