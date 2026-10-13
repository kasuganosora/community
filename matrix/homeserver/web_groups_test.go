package homeserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupWebGroupCore(t *testing.T) (*HomeServer, *gorm.DB, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	hs, conn := directBridgeDB(t)
	hs.Config.Enabled = true
	hs.Account = NewAccountService(hs)
	hs.Push = NewPushService(hs)
	require.NoError(t, conn.AutoMigrate(&models.User{}))
	for _, item := range []struct {
		id   uint64
		name string
	}{{2, "alice"}, {3, "bob"}, {4, "outsider"}} {
		require.NoError(t, conn.Create(&models.User{Id: item.id, UserName: item.name, EmailHash: item.name}).Error)
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Account{}).Where("id = ?", item.id).UpdateColumn("user_id", item.id).Error)
	}
	router := gin.New()
	group := router.Group("/chat", func(c *gin.Context) {
		user := c.GetHeader("X-Test-User")
		if user == "" {
			user = "@alice:test.local"
		}
		c.Set("user_id", user)
		c.Set("device_id", "community-web")
	})
	RegisterCommunityGroupRoutes(group)
	return hs, conn, router
}
func webGroupTestRequest(t *testing.T, r *gin.Engine, method, path, body, user string) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	r.ServeHTTP(w, req)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out), w.Body.String())
	return w.Code, out
}
func webGroupTestCreate(t *testing.T, r *gin.Engine) string {
	t.Helper()
	status, out := webGroupTestRequest(t, r, "POST", "/chat/groups", `{"transaction_id":"create-case-1","name":"Native group","visibility":"private","plaintext_consent":true}`, "")
	require.Equal(t, 200, status, out)
	return out["id"].(string)
}

func TestWebGroupCreationConsentAndIdempotency(t *testing.T) {
	_, conn, r := setupWebGroupCore(t)
	status, _ := webGroupTestRequest(t, r, "POST", "/chat/groups", `{"transaction_id":"create-case-1","name":"Native group","visibility":"private"}`, "")
	require.Equal(t, 400, status)
	id := webGroupTestCreate(t, r)
	require.Equal(t, id, webGroupTestCreate(t, r))
	status, out := webGroupTestRequest(t, r, "POST", "/chat/groups", `{"transaction_id":"create-case-1","name":"Changed","visibility":"private","plaintext_consent":true}`, "")
	require.Equal(t, 409, status, out)
	roomID, err := webGroupRoomID(id)
	require.NoError(t, err)
	var room MatrixRoom
	require.NoError(t, conn.First(&room, "room_id = ?", roomID).Error)
	require.False(t, room.IsEncrypted)
	require.Equal(t, "joined", room.HistoryVisibility)
	require.Equal(t, "forbidden", room.GuestAccess)
	require.Equal(t, "private", room.Visibility)
	for _, table := range []any{&BridgeConversation{}, &models.Status{}, &models.MatrixDMDelivery{}, &MatrixDMOutbound{}} {
		var count int64
		require.NoError(t, conn.Model(table).Count(&count).Error)
		require.Zero(t, count)
	}
	status, out = webGroupTestRequest(t, r, "GET", "/chat/groups/"+id, "", "")
	require.Equal(t, 200, status, out)
	require.Equal(t, "unencrypted", out["encryption"])
	require.Equal(t, true, out["capabilities"].(map[string]any)["send_message"])
}
func TestWebGroupLocalGhostExcludedButRemoteUsersAllowed(t *testing.T) {
	hs, conn, _ := setupWebGroupCore(t)
	for _, value := range []string{"remote@remote.example", "@remote_remote_example_ap:test.local", "@_ap_bridge_unknown:test.local"} {
		_, err := webGroupInvitee(hs, conn, value)
		require.NotNil(t, err, value)
	}
	for _, value := range []string{"bob", "@bob", "@bob:test.local"} {
		id, err := webGroupInvitee(hs, conn, value)
		require.Nil(t, err)
		require.Equal(t, "@bob:test.local", id)
	}
	id, err := webGroupInvitee(hs, conn, "@a_bridge:remote.example")
	require.Nil(t, err)
	require.Equal(t, "@a_bridge:remote.example", id)
}
func TestWebGroupMembershipAuthorizationAndLeave(t *testing.T) {
	hs, _, r := setupWebGroupCore(t)
	id := webGroupTestCreate(t, r)
	roomID, _ := webGroupRoomID(id)
	status, _ := webGroupTestRequest(t, r, "GET", "/chat/groups/"+id, "", "@outsider:test.local")
	require.Equal(t, 403, status)
	status, out := webGroupTestRequest(t, r, "POST", "/chat/groups/"+id+"/members", `{"user_id":"bob","action":"invite"}`, "")
	require.Equal(t, 200, status, out)
	status, out = webGroupTestRequest(t, r, "GET", "/chat/groups", "", "@bob:test.local")
	require.Equal(t, 200, status, out)
	require.Len(t, out["groups"], 1)
	group := out["groups"].([]any)[0].(map[string]any)
	require.Equal(t, "invite", group["membership"])
	require.Equal(t, "unknown", group["encryption"])
	_, err := hs.Room.JoinRoom("@bob:test.local", roomID, &JoinRoomRequest{})
	require.Nil(t, err)
	status, _ = webGroupTestRequest(t, r, "PATCH", "/chat/groups/"+id, `{"name":"unauthorized"}`, "@bob:test.local")
	require.Equal(t, 403, status)
	status, out = webGroupTestRequest(t, r, "POST", "/chat/groups/"+id+"/leave", `{}`, "@bob:test.local")
	require.Equal(t, 200, status, out)
	status, out = webGroupTestRequest(t, r, "GET", "/chat/groups", "", "@bob:test.local")
	require.Equal(t, 200, status, out)
	require.Empty(t, out["groups"])
	membership, _ := hs.Room.GetUserMembership(roomID, "@bob:test.local")
	require.Equal(t, MembershipLeave, membership)
}
func TestWebGroupLifecycleAndCapabilities(t *testing.T) {
	hs, conn, r := setupWebGroupCore(t)
	id := webGroupTestCreate(t, r)
	roomID, _ := webGroupRoomID(id)
	empty := ""
	_, err := hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.encryption", nil, map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}, &empty)
	require.Nil(t, err)
	status, out := webGroupTestRequest(t, r, "GET", "/chat/groups/"+id, "", "")
	require.Equal(t, 200, status, out)
	require.Equal(t, "encrypted", out["encryption"])
	require.False(t, out["capabilities"].(map[string]any)["send_message"].(bool))
	for _, capability := range []string{"invite", "set_avatar", "set_name", "manage_permissions"} {
		require.Equal(t, false, out["capabilities"].(map[string]any)[capability])
	}
	status, out = webGroupTestRequest(t, r, "PATCH", "/chat/groups/"+id, `{"name":"must not change encrypted room"}`, "")
	require.Equal(t, 409, status, out)
	require.Equal(t, "encrypted", out["code"])
	status, out = webGroupTestRequest(t, r, "POST", "/chat/groups/"+id+"/permissions", `{"user_id":"@alice:test.local","power_level":50}`, "")
	require.Equal(t, 409, status, out)
	status, out = webGroupTestRequest(t, r, "POST", "/chat/groups/"+id+"/typing", `{"typing":true}`, "")
	require.Equal(t, 409, status, out)
	require.Equal(t, "encrypted", out["code"])
	var room MatrixRoom
	require.NoError(t, conn.First(&room, "room_id = ?", roomID).Error)
	require.Equal(t, "M_GROUP_ENCRYPTED", webGroupCanWrite(hs, &room, "@alice:test.local").ErrCode)
	state := map[EventType]MatrixRoomState{EventTypeRoomCreate: {Content: map[string]any{}}}
	require.Equal(t, "M_GROUP_UNKNOWN", webGroupPlaintextState(&MatrixRoom{}, nil).ErrCode)
	require.Equal(t, "M_GROUP_SPACE", webGroupPlaintextState(&MatrixRoom{RoomType: new("m.space")}, state).ErrCode)
	state["m.room.tombstone"] = MatrixRoomState{Content: map[string]any{"replacement_room": "!new:remote.example"}}
	require.Equal(t, "M_GROUP_UPGRADED", webGroupPlaintextState(&MatrixRoom{}, state).ErrCode)
}

func TestWebGroupStateAndOutboxCommitTogether(t *testing.T) {
	_, conn, r := setupWebGroupCore(t)
	id := webGroupTestCreate(t, r)
	roomID, _ := webGroupRoomID(id)
	var initial MatrixRoom
	require.NoError(t, conn.Where("room_id = ?", roomID).Take(&initial).Error)
	require.NoError(t, conn.Create(&MatrixRoomMember{RoomID: roomID, UserID: "@remote:other.example", Membership: MembershipJoin}).Error)
	require.NoError(t, conn.Callback().Create().Before("gorm:create").Register("web_state_outbox_failure", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "BridgeFederationDelivery" {
			tx.AddError(errors.New("injected federation outbox failure"))
		}
	}))
	t.Cleanup(func() { conn.Callback().Create().Remove("web_state_outbox_failure") })
	status, out := webGroupTestRequest(t, r, "PATCH", "/chat/groups/"+id, `{"name":"Atomic update"}`, "")
	require.Equal(t, 500, status, out)
	var room MatrixRoom
	require.NoError(t, conn.Where("room_id = ?", roomID).Take(&room).Error)
	require.Equal(t, initial.Name, room.Name, "failed delivery enqueue must roll back the visible room change")
	require.NoError(t, conn.Callback().Create().Remove("web_state_outbox_failure"))
	status, out = webGroupTestRequest(t, r, "PATCH", "/chat/groups/"+id, `{"name":"Atomic update"}`, "")
	require.Equal(t, 200, status, out)
	var state MatrixRoomState
	require.NoError(t, conn.Where("room_id = ? AND event_type = ?", roomID, EventTypeRoomName).Take(&state).Error)
	var queued BridgeFederationDelivery
	require.NoError(t, conn.Where("event_id = ? AND server_name = ?", state.EventID, "other.example").Take(&queued).Error)
	require.True(t, queued.Native)
}
func TestWebGroupKnownBridgeCannotBeOpened(t *testing.T) {
	_, conn, r := setupWebGroupCore(t)
	id := webGroupTestCreate(t, r)
	roomID, _ := webGroupRoomID(id)
	require.NoError(t, conn.Create(&BridgeConversation{ID: "bridge-case", RoomID: roomID, CreatorAccountID: 2, Controller: "@controller:test.local", RequestKey: "unique-key", State: "active"}).Error)
	status, _ := webGroupTestRequest(t, r, "GET", "/chat/groups/"+id, "", "")
	require.Equal(t, 404, status)
	status, out := webGroupTestRequest(t, r, "GET", "/chat/groups", "", "")
	require.Equal(t, 200, status, out)
	require.Empty(t, out["groups"])
}
func TestWebGroupPowerChangesPreserveOtherRules(t *testing.T) {
	hs, conn, r := setupWebGroupCore(t)
	id := webGroupTestCreate(t, r)
	roomID, _ := webGroupRoomID(id)
	require.Nil(t, hs.Room.InviteUser(roomID, "@alice:test.local", &InviteUserRequest{UserID: "@bob:test.local"}))
	_, err := hs.Room.JoinRoom("@bob:test.local", roomID, &JoinRoomRequest{})
	require.Nil(t, err)
	status, out := webGroupTestRequest(t, r, "POST", "/chat/groups/"+id+"/permissions", `{"user_id":"@bob:test.local","power_level":50}`, "")
	require.Equal(t, 200, status, out)
	status, _ = webGroupTestRequest(t, r, "POST", "/chat/groups/"+id+"/permissions", `{"user_id":"@alice:test.local","power_level":0}`, "@bob:test.local")
	require.Equal(t, 403, status)
	var state MatrixRoomState
	require.NoError(t, conn.First(&state, "room_id = ? AND event_type = ?", roomID, EventTypeRoomPowerLevels).Error)
	require.Equal(t, float64(50), state.Content["state_default"])
}
func TestWebGroupMuteUsesNativePushRules(t *testing.T) {
	_, conn, r := setupWebGroupCore(t)
	id := webGroupTestCreate(t, r)
	status, out := webGroupTestRequest(t, r, "POST", "/chat/groups/"+id+"/preferences", `{"muted":true}`, "")
	require.Equal(t, 200, status, out)
	status, out = webGroupTestRequest(t, r, "GET", "/chat/groups/"+id, "", "")
	require.Equal(t, 200, status, out)
	require.Equal(t, true, out["muted"])
	status, out = webGroupTestRequest(t, r, "POST", "/chat/groups/"+id+"/preferences", `{"muted":false}`, "")
	require.Equal(t, 200, status, out)
	var count int64
	require.NoError(t, conn.Model(&PushRule{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestWebGroupUnreadUsesReceiptOrderAndHistoricalMembership(t *testing.T) {
	hs, conn, r := setupWebGroupCore(t)
	id := webGroupTestCreate(t, r)
	roomID, _ := webGroupRoomID(id)
	before, err := hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.message", nil, map[string]any{"body": "before invited", "msgtype": "m.text"}, nil)
	require.Nil(t, err)
	require.Nil(t, hs.Room.InviteUser(roomID, "@alice:test.local", &InviteUserRequest{UserID: "@bob:test.local"}))
	_, err = hs.Room.JoinRoom("@bob:test.local", roomID, &JoinRoomRequest{})
	require.Nil(t, err)
	first, err := hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.message", nil, map[string]any{"body": "clock skew 1", "msgtype": "m.text"}, nil)
	require.Nil(t, err)
	second, err := hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.message", nil, map[string]any{"body": "clock skew 2", "msgtype": "m.text"}, nil)
	require.Nil(t, err)
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("event_id IN ?", []string{before.EventID, first.EventID, second.EventID}).UpdateColumn("origin_server_ts", 1).Error)
	counts, e := webGroupUnreadCounts(conn, []string{roomID}, "@bob:test.local")
	require.NoError(t, e)
	require.EqualValues(t, 2, counts[roomID], "prejoin events must not be disclosed; equal remote clocks must not merge events")
	require.Nil(t, hs.Sync.SetReceipt(roomID, first.EventID, "@bob:test.local", "m.read"))
	counts, e = webGroupUnreadCounts(conn, []string{roomID}, "@bob:test.local")
	require.NoError(t, e)
	require.EqualValues(t, 1, counts[roomID])
	require.Nil(t, hs.Sync.SetReceipt(roomID, second.EventID, "@bob:test.local", "m.read.private"))
	counts, e = webGroupUnreadCounts(conn, []string{roomID}, "@bob:test.local")
	require.NoError(t, e)
	require.Zero(t, counts[roomID])
}

func TestWebGroupInvitationPreviewIsNeverAuthoritativeState(t *testing.T) {
	hs, conn, r := setupWebGroupCore(t)
	id := webGroupTestCreate(t, r)
	roomID, _ := webGroupRoomID(id)
	var event MatrixEvent
	require.NoError(t, conn.First(&event, "room_id = ? AND event_type = ?", roomID, EventTypeRoomMember).Error)
	p, err := parseAndValidatePDU("10", []byte(event.FederationJSON))
	require.NoError(t, err)
	require.NoError(t, conn.Where("room_id = ?", roomID).Delete(&MatrixRoomState{}).Error)
	require.NoError(t, conn.Unscoped().Where("room_id = ?", roomID).Delete(&MatrixRoom{}).Error)
	preview := []json.RawMessage{json.RawMessage(`{"type":"m.room.name","state_key":"","content":{"name":"Remote invitation"}}`), json.RawMessage(`{"type":"m.room.create","state_key":"","content":{"creator":"@fake:evil.example"}}`)}
	require.NoError(t, persistWebInvitePreview(conn, p, preview))
	var room MatrixRoom
	require.NoError(t, conn.First(&room, "room_id = ?", roomID).Error)
	require.Equal(t, "Remote invitation", *room.Name)
	require.Empty(t, room.Creator)
	require.Equal(t, "private", room.Visibility)
	var count int64
	require.NoError(t, conn.Model(&MatrixRoomState{}).Where("room_id = ?", roomID).Count(&count).Error)
	require.Zero(t, count)
	require.Equal(t, "M_GROUP_UNKNOWN", webGroupPlaintext(hs, &room).ErrCode)
}

func TestWebGroupCreateRetryDoesNotReinviteDeclinedMember(t *testing.T) {
	hs, conn, r := setupWebGroupCore(t)
	body := `{"transaction_id":"create-with-invite","name":"Invitations","visibility":"private","invite":["bob"],"plaintext_consent":true}`
	status, out := webGroupTestRequest(t, r, "POST", "/chat/groups", body, "")
	require.Equal(t, 200, status, out)
	require.Empty(t, out["invite_failures"])
	id := out["id"].(string)
	roomID, _ := webGroupRoomID(id)
	require.Nil(t, hs.Room.LeaveRoom("@bob:test.local", roomID, &LeaveRoomRequest{}))
	status, out = webGroupTestRequest(t, r, "POST", "/chat/groups", body, "")
	require.Equal(t, 200, status, out)
	require.Equal(t, id, out["id"])
	require.Empty(t, out["invite_failures"])
	membership, err := hs.Room.GetUserMembership(roomID, "@bob:test.local")
	require.NoError(t, err)
	require.Equal(t, MembershipLeave, membership)
	var jobs []WebGroupInvitation
	require.NoError(t, conn.Find(&jobs).Error)
	require.Len(t, jobs, 1)
	require.Equal(t, "completed", jobs[0].State)
	require.Empty(t, jobs[0].PreparedPDU)
	var sessions int64
	require.NoError(t, conn.Model(&MatrixUserSession{}).Count(&sessions).Error)
	require.Zero(t, sessions, "web API never mints Matrix access tokens")
}

func TestWebGroupInviteCommitFailureCanRetrySameOperation(t *testing.T) {
	hs, conn, r := setupWebGroupCore(t)
	id := webGroupTestCreate(t, r)
	roomID, _ := webGroupRoomID(id)
	require.NoError(t, conn.Callback().Create().Before("gorm:create").Register("web_invite_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "matrix_events" {
			tx.AddError(errors.New("injected invitation commit failure"))
		}
	}))
	require.NotNil(t, hs.inviteWebGroupUser(context.Background(), roomID, "@alice:test.local", "@bob:test.local", "", "retryable-invite"))
	var member MatrixRoomMember
	require.ErrorIs(t, conn.Where("room_id = ? AND user_id = ?", roomID, "@bob:test.local").First(&member).Error, gorm.ErrRecordNotFound)
	var job WebGroupInvitation
	require.NoError(t, conn.First(&job).Error)
	require.Equal(t, "pending", job.State)
	prepared := job.PreparedPDU
	require.NotEmpty(t, prepared)
	require.NoError(t, conn.Callback().Create().Remove("web_invite_failure"))
	require.Nil(t, hs.inviteWebGroupUser(context.Background(), roomID, "@alice:test.local", "@bob:test.local", "", "retryable-invite"))
	require.NoError(t, conn.Where("room_id = ? AND user_id = ?", roomID, "@bob:test.local").First(&member).Error)
	require.Equal(t, MembershipInvite, member.Membership)
	var invite MatrixEvent
	require.NoError(t, conn.First(&invite, "event_id = ?", member.EventID).Error)
	p, err := parseAndValidatePDU("10", []byte(prepared))
	require.NoError(t, err)
	require.Equal(t, p.EventID(), invite.EventID)
}

func TestWebGroupInboundJoinAndLeaveDurablyReachOtherServers(t *testing.T) {
	hs, conn, _ := setupWebGroupCore(t)
	created, me := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Visibility: "public", Preset: "public_chat"})
	require.Nil(t, me)
	resident := minimalHS("resident.example")
	origin := minimalHS("joining.example")
	for _, remote := range []*HomeServer{resident, origin} {
		GetKeyCache().Set(remote.Config.ServerName, remote.keyID, remote.publicKey, time.Now().Add(time.Hour))
	}
	// Key seeding persists asynchronously. Finish that fixture write before
	// exercising the independent join/leave transaction on SQLite.
	keyCtx, cancelKeys := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelKeys()
	require.NoError(t, GetKeyCache().WaitSaved(keyCtx))
	makeMember := func(remote *HomeServer, user, membership string) *MatrixEvent {
		event := &MatrixEvent{RoomID: created.RoomID, Sender: user, EventType: EventTypeRoomMember, StateKey: &user, Content: map[string]any{"membership": membership}, OriginServerTS: time.Now().UnixMilli()}
		require.NoError(t, buildLocalPDU(conn, remote, event, "10"))
		return event
	}
	residentEvent := makeMember(resident, "@resident:resident.example", "join")
	p, err := parseAndValidatePDU("10", []byte(residentEvent.FederationJSON))
	require.NoError(t, err)
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error { return persistVerifiedPDU(tx, hs, p, true) }))
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("origin", origin.Config.ServerName) })
	router.PUT("/join/:roomId/:eventId", handleSendJoin)
	router.PUT("/leave/:roomId/:eventId", handleSendLeave)
	for _, membership := range []string{"join", "leave"} {
		event := makeMember(origin, "@visitor:joining.example", membership)
		status, out := webGroupTestRequest(t, router, "PUT", "/"+membership+"/"+created.RoomID+"/"+event.EventID, event.FederationJSON, "")
		require.Equal(t, 200, status, out)
		var jobs []BridgeFederationDelivery
		require.NoError(t, conn.Where("event_id = ?", event.EventID).Find(&jobs).Error)
		require.Len(t, jobs, 1)
		require.True(t, jobs[0].Native)
		require.Equal(t, resident.Config.ServerName, jobs[0].ServerName)
	}
}

func TestWebGroupModeratorCanRejectKnock(t *testing.T) {
	hs, _, r := setupWebGroupCore(t)
	id := webGroupTestCreate(t, r)
	roomID, _ := webGroupRoomID(id)
	empty := ""
	_, me := hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.join_rules", nil, map[string]any{"join_rule": "knock"}, &empty)
	require.Nil(t, me)
	// Exercise the normal signed knock path exposed by the group join endpoint.
	status, out := webGroupTestRequest(t, r, "POST", "/chat/groups/join", `{"target":"`+roomID+`","action":"knock"}`, "@bob:test.local")
	require.Equal(t, 200, status, out)
	membership, err := hs.Room.GetUserMembership(roomID, "@bob:test.local")
	require.NoError(t, err)
	require.Equal(t, MembershipKnock, membership)
	status, out = webGroupTestRequest(t, r, "POST", "/chat/groups/"+id+"/members", `{"user_id":"@bob:test.local","action":"kick"}`, "")
	require.Equal(t, 200, status, out)
	membership, err = hs.Room.GetUserMembership(roomID, "@bob:test.local")
	require.NoError(t, err)
	require.Equal(t, MembershipLeave, membership)
}

func TestWebGroupProfilesPreferRoomNameThenLocalAccount(t *testing.T) {
	hs, conn, r := setupWebGroupCore(t)
	id := webGroupTestCreate(t, r)
	roomID, _ := webGroupRoomID(id)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Account{}).Where("username = ?", "bob").UpdateColumn("display_name", "鲍勃").Error)
	require.Nil(t, hs.Room.InviteUser(roomID, "@alice:test.local", &InviteUserRequest{UserID: "@bob:test.local"}))
	_, me := hs.Room.JoinRoom("@bob:test.local", roomID, &JoinRoomRequest{})
	require.Nil(t, me)
	var members []MatrixRoomMember
	require.NoError(t, conn.Where("room_id = ?", roomID).Find(&members).Error)
	profiles, err := webGroupMemberProfiles(hs, conn, roomID, members)
	require.NoError(t, err)
	require.Equal(t, "鲍勃", profiles["@bob:test.local"].DisplayName)
	require.Contains(t, profiles["@bob:test.local"].AvatarURL, "/members/")
	bob := "@bob:test.local"
	_, me = hs.Event.SendRoomEvent(roomID, bob, "m.room.member", nil, map[string]any{"membership": "join", "displayname": "群内昵称"}, &bob)
	require.Nil(t, me)
	profiles, err = webGroupMemberProfiles(hs, conn, roomID, members)
	require.NoError(t, err)
	require.Equal(t, "群内昵称", profiles[bob].DisplayName)
}
