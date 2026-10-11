package homeserver

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestExplicitConversationConsentSnapshotAndRemoval(t *testing.T) {
	hs, conn, _ := outboundBridgeFixture(t)
	require.NoError(t, conn.AutoMigrate(&BridgeConversation{}, &BridgeConversationMember{}, &MatrixExternalActor{}))
	req := BridgeConversationRequest{TransactionID: "proactive", Members: []BridgeMemberInput{{AccountID: "1"}}}
	_, err := hs.CreateBridgeConversation(context.Background(), "@alice:test.local", req)
	require.Error(t, err)
	req.PlaintextConsent = true
	conv, err := hs.CreateBridgeConversation(context.Background(), "@alice:test.local", req)
	require.NoError(t, err)
	same, err := hs.CreateBridgeConversation(context.Background(), "@alice:test.local", req)
	require.NoError(t, err)
	require.Equal(t, conv.ID, same.ID)
	sent := sendOutboundTest(t, hs, conv.RoomID, map[string]any{"msgtype": "m.text", "body": "proactive @outsider"})
	var job MatrixDMOutbound
	require.NoError(t, conn.First(&job, "event_id = ?", sent.EventID).Error)
	require.Equal(t, conv.ID, job.ConversationID)
	require.Equal(t, []uint64{1}, job.RecipientIDs)
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), sent.EventID))
	require.NoError(t, conn.First(&job, "event_id = ?", sent.EventID).Error)
	var status models.Status
	require.NoError(t, conn.Preload("ToAccounts").First(&status, job.StatusID).Error)
	require.Len(t, status.ToAccounts, 1)
	require.Equal(t, uint64(1), status.ToAccounts[0].Id)
	txn := "before-remove"
	queued, me := hs.Event.SendRoomEventFromDevice(conv.RoomID, "@alice:test.local", "DEVICE", string(EventTypeRoomMessage), &txn, map[string]any{"msgtype": "m.text", "body": "must not deliver after removal"}, nil)
	require.Nil(t, me)
	var member BridgeConversationMember
	require.NoError(t, conn.First(&member, "conversation_id = ? AND account_id = ?", conv.ID, 1).Error)
	require.NoError(t, conn.Model(&member).Update("left_revision", 2).Error)
	require.Error(t, hs.ProcessOutboundDirect(context.Background(), queued.EventID))
	job = MatrixDMOutbound{}
	require.NoError(t, conn.First(&job, "event_id = ?", queued.EventID).Error)
	require.Equal(t, "skipped", job.State)
}

func TestExplicitConversationNewMemberCannotReceiveQueuedHistory(t *testing.T) {
	hs, conn, _ := outboundBridgeFixture(t)
	require.NoError(t, conn.AutoMigrate(&BridgeConversation{}, &BridgeConversationMember{}, &MatrixExternalActor{}))
	conv, err := hs.CreateBridgeConversation(context.Background(), "@alice:test.local", BridgeConversationRequest{TransactionID: "snapshot", PlaintextConsent: true, Members: []BridgeMemberInput{{AccountID: "1"}}})
	require.NoError(t, err)
	sent := sendOutboundTest(t, hs, conv.RoomID, map[string]any{"msgtype": "m.text", "body": "before added member"})
	// A third local member is admitted only after the original event committed.
	require.NoError(t, conn.Create(&models.User{Id: 3, UserName: "bob", EmailHash: "bob-snapshot"}).Error)
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", 3).UpdateColumn("user_id", 3).Error)
	require.NoError(t, hs.ChangeBridgeMembers(context.Background(), conv.ID, "@alice:test.local", BridgeConversationRequest{PlaintextConsent: true, Members: []BridgeMemberInput{{AccountID: "3"}}}, 0))
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), sent.EventID))
	var job MatrixDMOutbound
	require.NoError(t, conn.First(&job, "event_id = ?", sent.EventID).Error)
	var status models.Status
	require.NoError(t, conn.Preload("ToAccounts").First(&status, job.StatusID).Error)
	require.Len(t, status.ToAccounts, 1)
	require.Equal(t, uint64(1), status.ToAccounts[0].Id)
	var bob models.Account
	require.NoError(t, conn.First(&bob, 3).Error)
	allowed, err := status.CanViewEx(conn, &bob)
	require.NoError(t, err)
	require.False(t, allowed)
	// Removing and re-adding creates a new generation, never reviving a queued
	// audience captured before the removal.
	txn := "generation"
	second, me := hs.Event.SendRoomEventFromDevice(conv.RoomID, "@alice:test.local", "DEVICE", "m.room.message", &txn, map[string]any{"msgtype": "m.text", "body": "old generation"}, nil)
	require.Nil(t, me)
	var remote BridgeConversationMember
	require.NoError(t, conn.First(&remote, "conversation_id = ? AND account_id = ? AND left_revision = 0", conv.ID, 1).Error)
	require.NoError(t, hs.ChangeBridgeMembers(context.Background(), conv.ID, "@alice:test.local", BridgeConversationRequest{}, remote.ID))
	require.NoError(t, hs.ChangeBridgeMembers(context.Background(), conv.ID, "@alice:test.local", BridgeConversationRequest{PlaintextConsent: true, Members: []BridgeMemberInput{{AccountID: "1"}}}, 0))
	require.Error(t, hs.ProcessOutboundDirect(context.Background(), second.EventID))
	job = MatrixDMOutbound{}
	require.NoError(t, conn.First(&job, "event_id = ?", second.EventID).Error)
	require.Equal(t, "skipped", job.State)
}

func TestExplicitConversationHTTPListAndAuthenticatedMessages(t *testing.T) {
	hs, _, _ := outboundBridgeFixture(t)
	conv, err := hs.CreateBridgeConversation(context.Background(), "@alice:test.local", BridgeConversationRequest{TransactionID: "http", PlaintextConsent: true, Members: []BridgeMemberInput{{AccountID: "1"}}})
	require.NoError(t, err)
	sent := sendOutboundTest(t, hs, conv.RoomID, map[string]any{"msgtype": "m.text", "body": "native pending content"})
	router := gin.New()
	user := "@alice:test.local"
	group := router.Group("/", func(c *gin.Context) { c.Set("user_id", user); c.Next() })
	RegisterCommunityConversationRoutes(group)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/conversations", nil))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var listed struct {
		Conversations []struct {
			ID string `json:"id"`
		} `json:"conversations"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	require.NotEmpty(t, listed.Conversations)
	require.NotContains(t, rec.Body.String(), "__LOCAL__")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/conversations/"+conv.ID+"/messages", nil))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), sent.EventID)
	require.Contains(t, rec.Body.String(), "native pending content")
	user = "@outsider:test.local"
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/conversations/"+conv.ID+"/messages", nil))
	require.NotEqual(t, 200, rec.Code)
	require.NotContains(t, rec.Body.String(), "native pending content")
}

func TestExplicitConversationMySQL(t *testing.T) {
	TestExplicitConversationConsentSnapshotAndRemoval(t)
}

func TestExplicitConversationAttachmentReplacePreservesMediaAndReply(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	// Import the existing trusted room to exercise the same HTTP API as new rooms.
	require.NoError(t, hs.importLegacyBridgeConversations(context.Background(), "@alice:test.local"))
	var conv BridgeConversation
	require.NoError(t, conn.First(&conv, "room_id = ?", source.RoomID).Error)
	sent := sendOutboundTest(t, hs, source.RoomID, map[string]any{"msgtype": "m.text", "body": "original", "m.relates_to": map[string]any{"m.in_reply_to": map[string]any{"event_id": source.EventID}}})
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), sent.EventID))
	media := MatrixMedia{MediaID: "preserved", ServerName: "test.local", UploaderID: "@alice:test.local", FilePath: "matrix-media/private.png", FileSize: 10, ContentType: "image/png", FileName: "original.png"}
	require.NoError(t, conn.Create(&media).Error)
	var event MatrixEvent
	require.NoError(t, conn.First(&event, "event_id = ?", sent.EventID).Error)
	event.Content["msgtype"] = "m.image"
	event.Content["url"] = "mxc://test.local/preserved"
	event.Content["filename"] = "original.png"
	require.NoError(t, conn.Model(&event).Select("Content").Updates(&event).Error)
	router := gin.New()
	RegisterCommunityConversationRoutes(router.Group("/", func(c *gin.Context) { c.Set("user_id", "@alice:test.local"); c.Set("device_id", "DEVICE"); c.Next() }))
	req := httptest.NewRequest("POST", "/conversations/"+conv.ID+"/messages/"+sent.EventID+"/replace", strings.NewReader(`{"transaction_id":"attachment-edit","body":"new caption"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var response struct {
		EventID string `json:"event_id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	var edit MatrixEvent
	require.NoError(t, conn.First(&edit, "event_id = ?", response.EventID).Error)
	content := edit.Content["m.new_content"].(map[string]any)
	require.Equal(t, "m.image", content["msgtype"])
	require.Equal(t, "mxc://test.local/preserved", content["url"])
	require.Equal(t, "original.png", content["filename"])
	require.Equal(t, source.EventID, content["m.relates_to"].(map[string]any)["m.in_reply_to"].(map[string]any)["event_id"])
}

func TestExplicitConversationProvisionSessionMySQL(t *testing.T) {
	_, conn := directBridgeDB(t)
	release, err := lockBridgeProvision(context.Background(), conn, "same-conversation")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = lockBridgeProvision(ctx, conn, "same-conversation")
	require.Error(t, err)
	release()
	release, err = lockBridgeProvision(context.Background(), conn, "same-conversation")
	require.NoError(t, err)
	release()
}

func TestExplicitConversationReconciliationCannotActivateChangedRevision(t *testing.T) {
	hs, conn, _ := outboundBridgeFixture(t)
	conv, err := hs.CreateBridgeConversation(context.Background(), "@alice:test.local", BridgeConversationRequest{TransactionID: "revision", PlaintextConsent: true, Members: []BridgeMemberInput{{AccountID: "1"}}})
	require.NoError(t, err)
	require.NoError(t, conn.Create(&models.User{Id: 3, UserName: "bob", EmailHash: "bob-revision"}).Error)
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", 3).UpdateColumn("user_id", 3).Error)
	// Simulate a committed membership mutation while the room-state service is
	// completing an earlier reconciliation, before the final revision CAS.
	changed := false
	require.NoError(t, conn.Callback().Create().After("gorm:create").Register("advance-conversation-revision", func(tx *gorm.DB) {
		event, ok := tx.Statement.Dest.(*MatrixEvent)
		if ok && !changed && event.RoomID == conv.RoomID && event.EventType == EventTypeRoomMember && event.StateKey != nil && *event.StateKey == "@bob:test.local" {
			changed = true
			tx.AddError(tx.Session(&gorm.Session{NewDB: true}).Model(&BridgeConversation{}).Where("id = ?", conv.ID).UpdateColumn("revision", gorm.Expr("revision + 1")).Error)
		}
	}))
	err = hs.ChangeBridgeMembers(context.Background(), conv.ID, "@alice:test.local", BridgeConversationRequest{PlaintextConsent: true, Members: []BridgeMemberInput{{AccountID: "3"}}}, 0)
	require.Error(t, err)
	require.True(t, changed)
	require.NoError(t, conn.Callback().Create().Remove("advance-conversation-revision"))
	var current BridgeConversation
	require.NoError(t, conn.First(&current, "id = ?", conv.ID).Error)
	require.Equal(t, "provisioning", current.State)
	require.EqualValues(t, 3, current.Revision)
	require.NoError(t, hs.provisionBridgeConversation(context.Background(), &current))
	require.Equal(t, "active", current.State)
}

func TestExplicitConversationPendingEditReturnsRetryableConflict(t *testing.T) {
	hs, _, _ := outboundBridgeFixture(t)
	conv, err := hs.CreateBridgeConversation(context.Background(), "@alice:test.local", BridgeConversationRequest{TransactionID: "pending-edit", PlaintextConsent: true, Members: []BridgeMemberInput{{AccountID: "1"}}})
	require.NoError(t, err)
	sent := sendOutboundTest(t, hs, conv.RoomID, map[string]any{"msgtype": "m.text", "body": "pending original"})
	router := gin.New()
	RegisterCommunityConversationRoutes(router.Group("/", func(c *gin.Context) { c.Set("user_id", "@alice:test.local"); c.Set("device_id", "DEVICE"); c.Next() }))
	req := httptest.NewRequest("POST", "/conversations/"+conv.ID+"/messages/"+sent.EventID+"/replace", strings.NewReader(`{"transaction_id":"pending-replace","body":"edited"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, 409, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "M_NOT_YET_READY")
	require.NotContains(t, rec.Body.String(), "M_FORBIDDEN")
}

func TestExplicitConversationPowerLevelsAreFederationObjects(t *testing.T) {
	hs, conn, _ := outboundBridgeFixture(t)
	conv, err := hs.CreateBridgeConversation(context.Background(), "@alice:test.local", BridgeConversationRequest{TransactionID: "valid-power-levels", PlaintextConsent: true, Members: []BridgeMemberInput{{AccountID: "1"}}})
	require.NoError(t, err)
	var state MatrixRoomState
	require.NoError(t, conn.First(&state, "room_id = ? AND event_type = ? AND state_key = ?", conv.RoomID, EventTypeRoomPowerLevels, "").Error)
	var event MatrixEvent
	require.NoError(t, conn.First(&event, "event_id = ?", state.EventID).Error)
	// Check the exact persisted, signed PDU, not only the Go input structure.
	var pdu struct {
		Content map[string]json.RawMessage `json:"content"`
	}
	require.NoError(t, json.Unmarshal([]byte(event.FederationJSON), &pdu))
	for _, key := range []string{"users", "events"} {
		require.NotEqual(t, "null", string(pdu.Content[key]), key)
		var levels map[string]int64
		require.NoError(t, json.Unmarshal(pdu.Content[key], &levels))
		require.NotNil(t, levels, key)
	}
	var users map[string]int64
	require.NoError(t, json.Unmarshal(pdu.Content["users"], &users))
	require.EqualValues(t, 100, users[conv.Controller])
}

func TestExplicitConversationReplacementWebViewAndPagination(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	require.NoError(t, conn.AutoMigrate(&activitypub.Delivery{}))
	require.NoError(t, hs.importLegacyBridgeConversations(context.Background(), "@alice:test.local"))
	var conv BridgeConversation
	require.NoError(t, conn.First(&conv, "room_id = ?", source.RoomID).Error)
	original := sendOutboundTest(t, hs, source.RoomID, map[string]any{"msgtype": "m.text", "body": "old web content"})
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), original.EventID))
	txn := "web-replacement"
	replacement, me := hs.Event.SendRoomEventFromDevice(source.RoomID, "@alice:test.local", "DEVICE", "m.room.message", &txn, map[string]any{"msgtype": "m.text", "body": "* new web content", "m.new_content": map[string]any{"msgtype": "m.text", "body": "new web content"}, "m.relates_to": map[string]any{"rel_type": "m.replace", "event_id": original.EventID}}, nil)
	require.Nil(t, me)
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), replacement.EventID))
	router := gin.New()
	RegisterCommunityConversationRoutes(router.Group("/", func(c *gin.Context) { c.Set("user_id", "@alice:test.local"); c.Next() }))
	fetch := func(path string) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		require.Equal(t, 200, rec.Code, rec.Body.String())
		var out map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return out
	}
	path := "/conversations/" + conv.ID + "/messages"
	for reload := 0; reload < 2; reload++ {
		page := fetch(path)
		seen := map[string]map[string]any{}
		for _, raw := range page["messages"].([]any) {
			item := raw.(map[string]any)
			require.NotEqual(t, conv.Controller, item["sender"])
			seen[item["event_id"].(string)] = item
		}
		require.Equal(t, true, seen[original.EventID]["redacted"])
		require.Equal(t, "", seen[original.EventID]["body"])
		require.Nil(t, seen[original.EventID]["status"])
		require.Equal(t, "new web content", seen[replacement.EventID]["body"])
		require.NotNil(t, seen[replacement.EventID]["status_id"])
	}
	before := ""
	seen := map[string]bool{}
	for pages := 0; pages < 10; pages++ {
		request := path + "?limit=1"
		if before != "" {
			request += "&before=" + url.QueryEscape(before)
		}
		page := fetch(request)
		for _, raw := range page["messages"].([]any) {
			item := raw.(map[string]any)
			id := item["event_id"].(string)
			require.False(t, seen[id])
			seen[id] = true
			require.NotEqual(t, conv.Controller, item["sender"])
		}
		next, _ := page["next_cursor"].(string)
		if next == "" {
			break
		}
		before = next
	}
	require.True(t, seen[original.EventID])
	require.True(t, seen[replacement.EventID])
	require.True(t, seen[source.EventID])
	require.Len(t, seen, 3)
}

func TestExplicitConversationNativeLeaveClosesSnapshotGeneration(t *testing.T) {
	hs, conn, _ := outboundBridgeFixture(t)
	require.NoError(t, conn.Create(&models.User{Id: 3, UserName: "bob", EmailHash: "bob-native-leave"}).Error)
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", 3).UpdateColumn("user_id", 3).Error)
	conv, err := hs.CreateBridgeConversation(context.Background(), "@alice:test.local", BridgeConversationRequest{TransactionID: "native-leave", PlaintextConsent: true, Members: []BridgeMemberInput{{AccountID: "1"}, {AccountID: "3"}}})
	require.NoError(t, err)
	sent := sendOutboundTest(t, hs, conv.RoomID, map[string]any{"msgtype": "m.text", "body": "old joined generation"})
	var old BridgeConversationMember
	require.NoError(t, conn.First(&old, "conversation_id = ? AND account_id = ? AND left_revision = 0", conv.ID, 3).Error)
	require.Nil(t, hs.Room.LeaveRoom("@bob:test.local", conv.RoomID, &LeaveRoomRequest{}))
	require.NoError(t, conn.First(&old, "id = ?", old.ID).Error)
	require.NotZero(t, old.LeftRevision)
	// Even an otherwise accepted native membership join cannot resurrect this
	// generation: only an explicit owner invitation adds a new row.
	key := "@bob:test.local"
	err = conn.Transaction(func(tx *gorm.DB) error {
		return hs.syncBridgeMembershipDeparture(tx, &MatrixEvent{RoomID: conv.RoomID, EventType: EventTypeRoomMember, StateKey: &key, Content: map[string]any{"membership": "join"}})
	})
	require.ErrorIs(t, err, errDirectMessageSkipped)
	require.NoError(t, hs.ChangeBridgeMembers(context.Background(), conv.ID, "@alice:test.local", BridgeConversationRequest{PlaintextConsent: true, Members: []BridgeMemberInput{{AccountID: "3"}}}, 0))
	var current BridgeConversationMember
	require.NoError(t, conn.First(&current, "conversation_id = ? AND account_id = ? AND left_revision = 0", conv.ID, 3).Error)
	require.NotEqual(t, old.ID, current.ID)
	require.Error(t, hs.ProcessOutboundDirect(context.Background(), sent.EventID))
	var job MatrixDMOutbound
	require.NoError(t, conn.First(&job, "event_id = ?", sent.EventID).Error)
	require.Equal(t, "skipped", job.State)
}
