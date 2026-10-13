package homeserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kasuganosora/community/emojiasset"
	"image"
	"image/png"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func webMessageFixture(t *testing.T) (*HomeServer, *gin.Engine, string) {
	t.Helper()
	hs, router, room := webGroupMediaTestHS(t)
	hs.Push = nil
	require.NoError(t, db.GetDB().AutoMigrate(&BridgeFederationDelivery{}, &models.Account{}, &models.Emoji{}, &EventRelation{}))
	registerWebGroupMessageRoutes(router.Group("/api/v1/chat"))
	return hs, router, room
}
func webMessageRequest(t *testing.T, r *gin.Engine, method, room, path, user string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, "/api/v1/chat/groups/"+webGroupID(room)+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", user)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
func webMessageSend(t *testing.T, r *gin.Engine, room, txn, body string) webGroupMessage {
	t.Helper()
	w := webMessageRequest(t, r, "POST", room, "/messages", "@alice:test.local", webGroupSendRequest{TransactionID: txn, Body: body})
	require.Equal(t, 200, w.Code, w.Body.String())
	var response struct{ Messages []webGroupMessage }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Len(t, response.Messages, 1)
	return response.Messages[0]
}
func TestWebGroupMessageIdempotencyAndEncryption(t *testing.T) {
	hs, r, room := webMessageFixture(t)
	first := webMessageSend(t, r, room, "one", "hello")
	retry := webMessageSend(t, r, room, "one", "hello")
	require.Equal(t, first.ID, retry.ID)
	conflict := webMessageRequest(t, r, "POST", room, "/messages", "@alice:test.local", webGroupSendRequest{TransactionID: "one", Body: "different"})
	require.Equal(t, 409, conflict.Code, conflict.Body.String())
	var count int64
	require.NoError(t, db.GetDB().Model(&MatrixEvent{}).Where("room_id = ? AND event_type = ?", room, EventTypeRoomMessage).Count(&count).Error)
	require.EqualValues(t, 1, count)
	key := ""
	_, err := hs.Event.SendRoomEvent(room, "@alice:test.local", "m.room.encryption", nil, map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}, &key)
	require.Nil(t, err)
	blocked := webMessageRequest(t, r, "POST", room, "/messages", "@alice:test.local", webGroupSendRequest{TransactionID: "one", Body: "hello"})
	require.Equal(t, 409, blocked.Code, blocked.Body.String())
	require.Contains(t, blocked.Body.String(), "encrypted")
	blocked = webMessageRequest(t, r, "POST", room, "/messages", "@alice:test.local", webGroupSendRequest{TransactionID: "two", Body: "must not send"})
	require.Equal(t, 409, blocked.Code, blocked.Body.String())
	require.NoError(t, db.GetDB().Model(&MatrixEvent{}).Where("room_id = ? AND event_type = ?", room, EventTypeRoomMessage).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
func TestWebGroupStreamRollbackAndClockSkewPagination(t *testing.T) {
	hs, r, room := webMessageFixture(t)
	conn := db.GetDB()
	var before MatrixRoom
	require.NoError(t, conn.Where("room_id = ?", room).Take(&before).Error)
	err := conn.Transaction(func(tx *gorm.DB) error {
		returnErr := tx.Create(&MatrixEvent{RoomID: room, Sender: "@alice:test.local", EventType: EventTypeRoomMessage, Content: map[string]any{"msgtype": "m.text", "body": "rolled back"}}).Error
		require.NoError(t, returnErr)
		return errors.New("rollback")
	})
	require.Error(t, err)
	var after MatrixRoom
	require.NoError(t, conn.Where("room_id = ?", room).Take(&after).Error)
	require.Equal(t, before.WebStreamPosition, after.WebStreamPosition)
	first := webMessageSend(t, r, room, "a", "first")
	second := webMessageSend(t, r, room, "b", "second")
	third := webMessageSend(t, r, room, "c", "third")
	// Origin timestamp changes must not move events across server stream pages.
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("event_id = ?", first.ID).UpdateColumn("origin_server_ts", time.Now().Add(48*time.Hour).UnixMilli()).Error)
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("event_id = ?", third.ID).UpdateColumn("origin_server_ts", 1).Error)
	events, cursor, err := webGroupHistory(conn, room, "@alice:test.local", nil, 2)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, second.ID, events[0].EventID)
	require.Equal(t, third.ID, events[1].EventID)
	events, _, err = webGroupHistory(conn, room, "@alice:test.local", cursor, 2)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, first.ID, events[0].EventID)
	_ = hs
}
func TestWebGroupIncrementalMoreThanRecentPageAndOldMutations(t *testing.T) {
	hs, r, room := webMessageFixture(t)
	conn := db.GetDB()
	initial, err := webGroupSyncSnapshot(hs, conn, "@alice:test.local", webGroupSyncCursor{User: "@alice:test.local", Rooms: map[string]webGroupSyncRoom{}}, true)
	require.NoError(t, err)
	var cursor webGroupSyncCursor
	require.NoError(t, webGroupDecode(initial.NextCursor, &cursor))
	ids := []string{}
	for i := range 105 {
		event, merr := webGroupPersist(hs, conn, room, "@alice:test.local", fmt.Sprintf("bulk:%d", i), webGroupHash(i), "m.room.message", map[string]any{"msgtype": "m.text", "body": fmt.Sprintf("message %d", i)}, "", "")
		require.Nil(t, merr)
		ids = append(ids, event.EventID)
	}
	got := map[string]bool{}
	for i := 0; i < 5; i++ {
		response, err := webGroupSyncSnapshot(hs, conn, "@alice:test.local", cursor, false)
		require.NoError(t, err)
		for _, m := range response.Messages[webGroupID(room)] {
			got[m.ID] = true
		}
		require.NoError(t, webGroupDecode(response.NextCursor, &cursor))
		if !response.HasMore {
			break
		}
	}
	require.Len(t, got, 105, "a reconnect must not discard events beyond the last page")
	edit := map[string]any{"msgtype": "m.text", "body": "* changed", "m.new_content": map[string]any{"msgtype": "m.text", "body": "changed"}, "m.relates_to": map[string]any{"rel_type": "m.replace", "event_id": ids[0]}}
	_, merr := hs.Event.SendRoomEvent(room, "@alice:test.local", "m.room.message", nil, edit, nil)
	require.Nil(t, merr)
	response, err := webGroupSyncSnapshot(hs, conn, "@alice:test.local", cursor, false)
	require.NoError(t, err)
	require.Len(t, response.Messages[webGroupID(room)], 1)
	require.Equal(t, ids[0], response.Messages[webGroupID(room)][0].ID)
	require.Equal(t, "changed", response.Messages[webGroupID(room)][0].Body)
	require.True(t, response.Messages[webGroupID(room)][0].Edited)
	require.NoError(t, webGroupDecode(response.NextCursor, &cursor))
	reaction := webMessageRequest(t, r, "POST", room, "/messages/"+ids[0]+"/reactions", "@alice:test.local", gin.H{"key": "👍", "transaction_id": "react-a"})
	require.Equal(t, 200, reaction.Code, reaction.Body.String())
	reaction = webMessageRequest(t, r, "POST", room, "/messages/"+ids[0]+"/reactions", "@alice:test.local", gin.H{"key": "👍", "transaction_id": "react-b"})
	require.Equal(t, 200, reaction.Code, reaction.Body.String())
	response, err = webGroupSyncSnapshot(hs, conn, "@alice:test.local", cursor, false)
	require.NoError(t, err)
	require.Len(t, response.Messages[webGroupID(room)], 1)
	require.Equal(t, 1, response.Messages[webGroupID(room)][0].Reactions[0].Count, "same sender replay is one reaction")
	require.NoError(t, webGroupDecode(response.NextCursor, &cursor))
	removed := webMessageRequest(t, r, "DELETE", room, "/messages/"+ids[0]+"/reactions?key=👍", "@alice:test.local", nil)
	require.Equal(t, 200, removed.Code, removed.Body.String())
	response, err = webGroupSyncSnapshot(hs, conn, "@alice:test.local", cursor, false)
	require.NoError(t, err)
	require.Len(t, response.Messages[webGroupID(room)], 1)
	require.Empty(t, response.Messages[webGroupID(room)][0].Reactions)
	require.NoError(t, webGroupDecode(response.NextCursor, &cursor))
	removed = webMessageRequest(t, r, "DELETE", room, "/messages/"+ids[0], "@alice:test.local", nil)
	require.Equal(t, 200, removed.Code, removed.Body.String())
	response, err = webGroupSyncSnapshot(hs, conn, "@alice:test.local", cursor, false)
	require.NoError(t, err)
	require.Len(t, response.Messages[webGroupID(room)], 1)
	require.True(t, response.Messages[webGroupID(room)][0].Redacted)
}
func TestWebGroupRepliesAndVisibility(t *testing.T) {
	hs, r, room := webMessageFixture(t)
	key := ""
	_, merr := hs.Event.SendRoomEvent(room, "@alice:test.local", "m.room.history_visibility", nil, map[string]any{"history_visibility": "joined"}, &key)
	require.Nil(t, merr)
	old := webMessageSend(t, r, room, "before-bob", "not for bob")
	_, merr = hs.Room.JoinRoom("@bob:test.local", room, &JoinRoomRequest{})
	require.Nil(t, merr)
	blocked := webMessageRequest(t, r, "POST", room, "/messages", "@bob:test.local", webGroupSendRequest{TransactionID: "reply", Body: "reply", ReplyTo: old.ID})
	require.Equal(t, 403, blocked.Code, blocked.Body.String())
	readable := webMessageSend(t, r, room, "after-bob", "hello bob")
	reply := webMessageRequest(t, r, "POST", room, "/messages", "@bob:test.local", webGroupSendRequest{TransactionID: "reply", Body: "hello", ReplyTo: readable.ID, Mentions: []string{"@alice:test.local"}})
	require.Equal(t, 200, reply.Code, reply.Body.String())
	var response struct{ Messages []webGroupMessage }
	require.NoError(t, json.Unmarshal(reply.Body.Bytes(), &response))
	require.Equal(t, readable.ID, response.Messages[0].ReplyTo)
	var event MatrixEvent
	require.NoError(t, db.GetDB().Where("event_id = ?", response.Messages[0].ID).Take(&event).Error)
	require.Equal(t, readable.ID, webGroupRelation(event.Content)["m.in_reply_to"].(map[string]any)["event_id"])
	invalid := webMessageRequest(t, r, "POST", room, "/messages", "@bob:test.local", webGroupSendRequest{TransactionID: "bad-mention", Body: "hello", Mentions: []string{"@stranger:remote.example"}})
	require.Equal(t, 400, invalid.Code, invalid.Body.String())
	events, _, err := webGroupHistory(db.GetDB(), room, "@bob:test.local", nil, 100)
	require.NoError(t, err)
	for _, e := range events {
		require.NotEqual(t, old.ID, e.EventID)
	}
}
func TestWebGroupReplacePartialAndAttachmentRetry(t *testing.T) {
	_, r, room := webMessageFixture(t)
	original := webMessageSend(t, r, room, "original", "old text")
	req := webGroupSendRequest{TransactionID: "replace", Body: "new text", UploadIDs: []string{"missing-upload"}}
	result := webMessageRequest(t, r, "POST", room, "/messages/"+original.ID+"/replace", "@alice:test.local", req)
	require.Equal(t, 409, result.Code, result.Body.String())
	require.Contains(t, result.Body.String(), "partial_failure")
	require.Contains(t, result.Body.String(), `"deleted":true`)
	retry := webMessageRequest(t, r, "POST", room, "/messages/"+original.ID+"/replace", "@alice:test.local", req)
	require.Equal(t, 409, retry.Code, retry.Body.String())
	var count int64
	require.NoError(t, db.GetDB().Model(&MatrixEvent{}).Where("room_id = ? AND event_type = ?", room, EventTypeRoomMessage).Count(&count).Error)
	require.EqualValues(t, 2, count, "accepted replacement text must not be sent again")
	upload := webGroupFixtureUploadID(t, uploadWebGroupFixture(t, r, room, "@alice:test.local", "hello.txt", []byte("hello attachment")))
	send := webGroupSendRequest{TransactionID: "file", UploadIDs: []string{upload}}
	file := webMessageRequest(t, r, "POST", room, "/messages", "@alice:test.local", send)
	require.Equal(t, 200, file.Code, file.Body.String())
	var response struct{ Messages []webGroupMessage }
	require.NoError(t, json.Unmarshal(file.Body.Bytes(), &response))
	require.Len(t, response.Messages[0].Attachments, 1)
	again := webMessageRequest(t, r, "POST", room, "/messages", "@alice:test.local", send)
	require.Equal(t, 200, again.Code, again.Body.String())
	downloaded := webGroupFixtureDownload(r, response.Messages[0].Attachments[0].URL, "@alice:test.local")
	require.Equal(t, 200, downloaded.Code, downloaded.Body.String())
	require.Equal(t, "hello attachment", downloaded.Body.String())
}
func TestWebGroupCustomEmojiAndUnsafeHTML(t *testing.T) {
	hs, r, room := webMessageFixture(t)
	conn := db.GetDB()
	var imageData bytes.Buffer
	require.NoError(t, png.Encode(&imageData, image.NewNRGBA(image.Rect(0, 0, 8, 8))))
	asset, assetErr := emojiasset.StoreEmojiImage(context.Background(), bytes.NewReader(imageData.Bytes()), "image/png")
	require.NoError(t, assetErr)
	require.NoError(t, conn.Create(&models.Emoji{Shortcode: "party", Url: asset.URL, StaticUrl: asset.StaticURL}).Error)
	sent := webMessageRequest(t, r, "POST", room, "/messages", "@alice:test.local", webGroupSendRequest{TransactionID: "emoji", Body: "<img onerror=alert(1)> :party:", CustomEmojis: []string{"party"}})
	require.Equal(t, 200, sent.Code, sent.Body.String())
	var response struct{ Messages []webGroupMessage }
	require.NoError(t, json.Unmarshal(sent.Body.Bytes(), &response))
	require.Len(t, response.Messages[0].CustomEmojis, 1)
	require.NotContains(t, response.Messages[0].FormattedBody, "<img")
	var event MatrixEvent
	require.NoError(t, conn.Where("event_id = ?", response.Messages[0].ID).Take(&event).Error)
	require.Contains(t, event.Content["formatted_body"], "data-mx-emoticon")
	require.Contains(t, event.Content["formatted_body"], "&lt;img")
	external, err := hs.Event.SendRoomEvent(room, "@alice:test.local", "m.room.message", nil, map[string]any{"msgtype": "m.text", "body": "external", "format": "org.matrix.custom.html", "formatted_body": "<script>alert(1)</script><a href='javascript:evil()'>link</a><img src='https://tracking.example/secret'>"}, nil)
	require.Nil(t, err)
	event = MatrixEvent{}
	require.NoError(t, conn.Where("event_id = ?", external.EventID).Take(&event).Error)
	messages, e := webGroupMessagesDTO(hs, conn, room, "@alice:test.local", []MatrixEvent{event})
	require.NoError(t, e)
	require.NotContains(t, messages[0].FormattedBody, "javascript:")
	require.NotContains(t, messages[0].FormattedBody, "tracking.example")
}
func TestWebGroupStreamConcurrentCommitOrder(t *testing.T) {
	hs, _, room := webMessageFixture(t)
	conn := db.GetDB()
	if conn.Dialector.Name() == "sqlite" {
		t.Skip("row-lock concurrency requires MySQL or PostgreSQL; SQLite serializes writers")
	}
	started, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := conn.Transaction(func(tx *gorm.DB) error {
			e := MatrixEvent{RoomID: room, Sender: "@alice:test.local", EventType: EventTypeRoomMessage, Content: map[string]any{"msgtype": "m.text", "body": "first"}}
			if err := tx.Create(&e).Error; err != nil {
				return err
			}
			close(started)
			<-release
			return nil
		})
		require.NoError(t, err)
	}()
	<-started
	done := make(chan *MatrixEvent, 1)
	go func() {
		event, err := webGroupPersist(hs, conn, room, "@alice:test.local", "second", webGroupHash("second"), "m.room.message", map[string]any{"msgtype": "m.text", "body": "second"}, "", "")
		require.Nil(t, err)
		done <- event
	}()
	select {
	case <-done:
		t.Fatal("same-room insert committed before an earlier transaction")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	second := <-done
	var events []MatrixEvent
	require.NoError(t, conn.Where("room_id = ? AND event_type = ?", room, EventTypeRoomMessage).Order("stream_position").Find(&events).Error)
	require.Len(t, events, 2)
	require.Less(t, events[0].StreamPosition, second.StreamPosition)
}

func TestWebGroupSyncMembershipEncryptionAndReceiptWithoutNotifications(t *testing.T) {
	hs, r, room := webMessageFixture(t)
	conn := db.GetDB()
	user := "@alice:test.local"
	initial, err := webGroupSyncSnapshot(hs, conn, user, webGroupSyncCursor{User: user, Rooms: map[string]webGroupSyncRoom{}}, true)
	require.NoError(t, err)
	var cursor webGroupSyncCursor
	require.NoError(t, webGroupDecode(initial.NextCursor, &cursor))
	// No process-local notification is needed to discover durable state changes.
	hs.SyncNotifier = nil
	message := webMessageSend(t, r, room, "receipt", "hello")
	update, err := webGroupSyncSnapshot(hs, conn, user, cursor, false)
	require.NoError(t, err)
	require.NoError(t, webGroupDecode(update.NextCursor, &cursor))
	require.Nil(t, hs.Sync.SetReceipt(room, message.ID, user, "m.read.private"))
	update, err = webGroupSyncSnapshot(hs, conn, user, cursor, false)
	require.NoError(t, err)
	require.Contains(t, update.Changed, webGroupID(room))
	require.NoError(t, webGroupDecode(update.NextCursor, &cursor))
	rule := PushRule{UserID: user, Scope: "global", Kind: "room", RuleID: room, Enabled: true, Actions: `["dont_notify"]`}
	require.NoError(t, conn.Create(&rule).Error)
	update, err = webGroupSyncSnapshot(hs, conn, user, cursor, false)
	require.NoError(t, err)
	require.Contains(t, update.Changed, webGroupID(room))
	require.Equal(t, true, update.Groups[0]["muted"])
	require.NoError(t, webGroupDecode(update.NextCursor, &cursor))
	key := ""
	_, merr := hs.Event.SendRoomEvent(room, user, "m.room.encryption", nil, map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}, &key)
	require.Nil(t, merr)
	update, err = webGroupSyncSnapshot(hs, conn, user, cursor, false)
	require.NoError(t, err)
	require.Equal(t, "encrypted", update.Groups[0]["encryption"])
	require.NoError(t, webGroupDecode(update.NextCursor, &cursor))
	require.Nil(t, hs.Room.LeaveRoom(user, room, &LeaveRoomRequest{}))
	update, err = webGroupSyncSnapshot(hs, conn, user, cursor, false)
	require.NoError(t, err)
	require.Equal(t, []string{webGroupID(room)}, update.Left)
	require.Empty(t, update.Messages)
}

func TestWebGroupCustomReactionExportsMXCAndCanBeRemoved(t *testing.T) {
	_, r, room := webMessageFixture(t)
	conn := db.GetDB()
	var data bytes.Buffer
	require.NoError(t, png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 8, 8))))
	asset, err := emojiasset.StoreEmojiImage(context.Background(), bytes.NewReader(data.Bytes()), "image/png")
	require.NoError(t, err)
	require.NoError(t, conn.Create(&models.Emoji{Shortcode: "party", Url: asset.URL, StaticUrl: asset.StaticURL}).Error)
	msg := webMessageSend(t, r, room, "with-reaction", "hello")
	sent := webMessageRequest(t, r, "POST", room, "/messages/"+msg.ID+"/reactions", "@alice:test.local", gin.H{"key": ":party:", "transaction_id": "party"})
	require.Equal(t, 200, sent.Code, sent.Body.String())
	var reaction MatrixEvent
	require.NoError(t, conn.Where("room_id = ? AND event_type = ?", room, "m.reaction").Take(&reaction).Error)
	require.Contains(t, webGroupRelation(reaction.Content)["key"], "mxc://test.local/emoji_")
	history := webMessageRequest(t, r, "GET", room, "/messages", "@alice:test.local", nil)
	require.Equal(t, 200, history.Code, history.Body.String())
	var result struct{ Messages []webGroupMessage }
	require.NoError(t, json.Unmarshal(history.Body.Bytes(), &result))
	require.Equal(t, ":party:", result.Messages[0].Reactions[0].Key)
	require.NotEmpty(t, result.Messages[0].Reactions[0].URL)
	removed := webMessageRequest(t, r, "DELETE", room, "/messages/"+msg.ID+"/reactions?key=:party:", "@alice:test.local", nil)
	require.Equal(t, 200, removed.Code, removed.Body.String())
	history = webMessageRequest(t, r, "GET", room, "/messages", "@alice:test.local", nil)
	require.NoError(t, json.Unmarshal(history.Body.Bytes(), &result))
	require.Empty(t, result.Messages[0].Reactions)
}

func TestWebGroupExternalEditsSelectLatestEvenWhenArrivingOutOfOrder(t *testing.T) {
	hs, r, room := webMessageFixture(t)
	conn := db.GetDB()
	original := webMessageSend(t, r, room, "original", "original")
	for i, item := range []struct {
		body string
		ts   int64
	}{{"newest", time.Now().Add(time.Hour).UnixMilli()}, {"older", time.Now().UnixMilli()}} {
		e := MatrixEvent{RoomID: room, Sender: "@alice:test.local", EventType: EventTypeRoomMessage, OriginServerTS: item.ts, Content: map[string]any{"msgtype": "m.text", "body": "* " + item.body, "m.new_content": map[string]any{"msgtype": "m.text", "body": item.body}, "m.relates_to": map[string]any{"rel_type": "m.replace", "event_id": original.ID}}}
		require.NoError(t, conn.Create(&e).Error, fmt.Sprint(i))
	}
	var event MatrixEvent
	require.NoError(t, conn.Where("event_id = ?", original.ID).Take(&event).Error)
	messages, err := webGroupMessagesDTO(hs, conn, room, "@alice:test.local", []MatrixEvent{event})
	require.NoError(t, err)
	require.Equal(t, "newest", messages[0].Body)
}

func TestWebGroupRichReplyFallbackDoesNotDuplicateQuote(t *testing.T) {
	body, formatted := webGroupDisplayBody(map[string]any{"body": "> <@alice:example.org> quoted original\n\nactual answer", "format": "org.matrix.custom.html", "formatted_body": "<mx-reply><blockquote>quoted original</blockquote></mx-reply><p>actual <b>answer</b></p>", "m.relates_to": map[string]any{"m.in_reply_to": map[string]any{"event_id": "$original"}}})
	require.Equal(t, "actual answer", body)
	require.NotContains(t, formatted, "quoted original")
	require.Contains(t, formatted, "<b>answer</b>")
	body, _ = webGroupDisplayBody(map[string]any{"body": "> my own quote\n\nkeep it", "m.relates_to": map[string]any{"m.in_reply_to": map[string]any{"event_id": "$original"}}})
	require.Equal(t, "> my own quote\n\nkeep it", body)
}

func TestWebGroupSenderUsesRoomProfileThenLocalDisplayName(t *testing.T) {
	hs, r, room := webMessageFixture(t)
	conn := db.GetDB()
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Extra").Create(&models.Account{Id: 1, UserId: 1, Username: "alice", DisplayName: "群聊测试昵称"}).Error)
	local := webMessageSend(t, r, room, "local-name", "hello")
	require.Equal(t, "群聊测试昵称", local.Sender.DisplayName)
	require.Contains(t, local.Sender.AvatarURL, "/members/")
	user := "@alice:test.local"
	_, err := hs.Event.SendRoomEvent(room, user, "m.room.member", nil, map[string]any{"membership": "join", "displayname": "房间专用昵称"}, &user)
	require.Nil(t, err)
	custom := webMessageSend(t, r, room, "room-name", "hello again")
	require.Equal(t, "房间专用昵称", custom.Sender.DisplayName)
}

func TestWebGroupSyncLimitHasActionableNonRetryableError(t *testing.T) {
	hs, router, _ := webMessageFixture(t)
	request := func(since string) {
		raw, err := json.Marshal(gin.H{"since": since, "timeout": 0})
		require.NoError(t, err)
		req := httptest.NewRequest("POST", "/api/v1/chat/group-sync", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-User", "@alice:test.local")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, 422, w.Code, w.Body.String())
		var result struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		require.Equal(t, "sync_limit_exceeded", result.Code)
		require.False(t, result.Retryable)
		require.Contains(t, w.Body.String(), "Matrix 客户端")
	}
	request(strings.Repeat("a", 256*1024+1))
	// The server must not silently truncate a real account's room set either.
	rooms, members := []MatrixRoom{}, []MatrixRoomMember{}
	for i := range 2001 {
		id := fmt.Sprintf("!limit-%d:test.local", i)
		rooms = append(rooms, MatrixRoom{RoomID: id, RoomVersion: "12", Creator: "@alice:test.local", JoinRule: "invite", HistoryVisibility: "joined"})
		members = append(members, MatrixRoomMember{RoomID: id, UserID: "@alice:test.local", Membership: MembershipJoin})
	}
	conn := db.GetDB()
	require.NoError(t, conn.CreateInBatches(rooms, 100).Error)
	require.NoError(t, conn.CreateInBatches(members, 100).Error)
	_, err := webGroupSyncSnapshot(hs, conn, "@alice:test.local", webGroupSyncCursor{}, true)
	require.ErrorIs(t, err, errWebGroupSyncLimit)
	request("")
}
