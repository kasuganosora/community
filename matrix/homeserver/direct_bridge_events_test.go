package homeserver

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBridgeReplyUsesOnlySameRoomTrustedMapping(t *testing.T) {
	hs, conn := directBridgeDB(t)
	seedDirectBridge(t, conn, 301, 2)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 301))
	var first models.MatrixDMDelivery
	require.NoError(t, conn.First(&first, "status_id = ?", 301).Error)
	parent := uint64(301)
	seedDirectBridge(t, conn, 302, 2)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Status{}).Where("id = ?", 302).UpdateColumn("in_reply_to_id", parent).Error)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 302))
	var second models.MatrixDMDelivery
	require.NoError(t, conn.First(&second, "status_id = ?", 302).Error)
	var event MatrixEvent
	require.NoError(t, conn.First(&event, "event_id = ?", second.EventID).Error)
	relation := event.Content["m.relates_to"].(map[string]any)
	require.Equal(t, first.EventID, relation["m.in_reply_to"].(map[string]any)["event_id"])
	content := map[string]any{}
	require.NoError(t, bridgeReplyRelation(conn, "!another:test.local", &models.Status{InReplyToId: &parent}, content))
	require.Empty(t, content)
}

func TestBridgeDeletionRedactsAllMappedEventsAndRetriesIdempotently(t *testing.T) {
	hs, conn := directBridgeDB(t)
	seedDirectBridge(t, conn, 311, 2)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 311))
	var delivery models.MatrixDMDelivery
	require.NoError(t, conn.First(&delivery, "status_id = ?", 311).Error)
	var original MatrixEvent
	require.NoError(t, conn.First(&original, "event_id = ?", delivery.EventID).Error)
	txn := "extra-file"
	attachment, me := hs.Event.sendBridgeRoomEvent(delivery.RoomID, original.Sender, "m.room.message", &txn, map[string]any{"msgtype": "m.file", "body": "private attachment", "url": "mxc://test.local/file"}, 311)
	require.Nil(t, me)
	// Hook queue and source removal must roll back together.
	require.Error(t, conn.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, models.QueueMatrixDirectDeletion(tx, &models.Status{Id: 311}))
		return fmt.Errorf("rollback")
	}))
	var count int64
	require.NoError(t, conn.Model(&models.MatrixDMEventJob{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error {
		if err := models.QueueMatrixDirectDeletion(tx, &models.Status{Id: 311}); err != nil {
			return err
		}
		return tx.Session(&gorm.Session{SkipHooks: true}).Delete(&models.Status{Id: 311}).Error
	}))
	require.NoError(t, hs.ProcessDirectBridgeEvent(context.Background(), "ap-delete:311"))
	require.NoError(t, hs.ProcessDirectBridgeEvent(context.Background(), "ap-delete:311"))
	for _, id := range []string{delivery.EventID, attachment.EventID} {
		var event MatrixEvent
		require.NoError(t, conn.First(&event, "event_id = ?", id).Error)
		require.Nil(t, event.Content["body"])
	}
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ? AND event_type = ?", delivery.RoomID, EventTypeRoomRedaction).Count(&count).Error)
	require.EqualValues(t, 2, count)
}

func TestBridgeRedactionCannotImpersonateAnotherAuthor(t *testing.T) {
	hs, conn := directBridgeDB(t)
	seedDirectBridge(t, conn, 321, 2)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 321))
	var delivery models.MatrixDMDelivery
	require.NoError(t, conn.First(&delivery, "status_id = ?", 321).Error)
	event := MatrixEvent{EventID: "$malicious", RoomID: delivery.RoomID, Sender: "@alice:test.local", Redacts: &delivery.EventID, OriginServerTS: time.Now().UnixMilli()}
	require.ErrorIs(t, hs.queueDirectBridgeRedaction(conn, &event), errDirectMessageSkipped)
	var count int64
	require.NoError(t, conn.Model(&models.MatrixDMEventJob{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestBridgeReactionUnicodeAndCustomAreDurableWithoutEcho(t *testing.T) {
	hs, conn, delivery := outboundBridgeFixture(t)
	require.NoError(t, conn.AutoMigrate(&models.StatusReaction{}, &models.RemoteReactionActivity{}))
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Status{}).Where("id = ?", 11).UpdateColumn("origin_uri", "https://remote.example/notes/11").Error)
	var alice models.Account
	require.NoError(t, conn.First(&alice, 2).Error)
	for n, key := range []string{"👍", ":bridge_wave:"} {
		if n == 1 {
			require.NoError(t, conn.Create(&models.Emoji{Id: 91, Shortcode: "bridge_wave", Domain: "", Url: "https://example.test/emoji.png", StaticUrl: "https://example.test/emoji.png"}).Error)
		}
		txn := fmt.Sprintf("reaction-%d", n)
		response, me := hs.Event.sendRoomEvent(delivery.RoomID, "@alice:test.local", "m.reaction", &txn, map[string]any{"m.relates_to": map[string]any{"rel_type": "m.annotation", "event_id": delivery.EventID, "key": key}}, nil, true, 0)
		require.Nil(t, me)
		var job models.MatrixDMEventJob
		require.NoError(t, conn.Where("event_id = ? AND kind = ?", response.EventID, "matrix_reaction").First(&job).Error)
		require.NoError(t, hs.ProcessDirectBridgeEvent(context.Background(), job.ID))
		var reaction models.StatusReaction
		require.NoError(t, conn.Where("status_id = ? AND account_id = ?", 11, 2).First(&reaction).Error)
		require.Equal(t, key, reaction.Name)
		var apJob models.MatrixDMEventJob
		require.NoError(t, conn.Where("reaction_id = ? AND kind = ?", reaction.ID, "Reaction").First(&apJob).Error)
		require.NoError(t, hs.ProcessDirectBridgeEvent(context.Background(), apJob.ID))
		var count int64
		require.NoError(t, conn.Model(&MatrixDMReactionReference{}).Where("reaction_id = ?", reaction.ID).Count(&count).Error)
		require.EqualValues(t, 1, count)
	}
}

func TestInboundBridgeReactionAndUndoUseTrustedReference(t *testing.T) {
	hs, conn, delivery := outboundBridgeFixture(t)
	require.NoError(t, conn.AutoMigrate(&models.StatusReaction{}, &models.RemoteReactionActivity{}))
	var remote models.Account
	require.NoError(t, conn.First(&remote, 1).Error)
	require.NoError(t, models.SetStatusReaction(conn, 11, &remote, "❤️", nil, "https://remote.example/likes/1", false))
	var reaction models.StatusReaction
	require.NoError(t, conn.Where("account_id = ? AND status_id = ?", 1, 11).First(&reaction).Error)
	id := fmt.Sprintf("ap-Reaction:%d", reaction.ID)
	require.NoError(t, hs.ProcessDirectBridgeEvent(context.Background(), id))
	var ref MatrixDMReactionReference
	require.NoError(t, conn.Where("reaction_id = ?", reaction.ID).First(&ref).Error)
	var event MatrixEvent
	require.NoError(t, conn.First(&event, "event_id = ?", ref.EventID).Error)
	relation := event.Content["m.relates_to"].(map[string]any)
	require.Equal(t, delivery.EventID, relation["event_id"])
	require.Equal(t, "❤️", relation["key"])
	require.NoError(t, models.SetStatusReaction(conn, 11, &remote, "❤️", nil, "https://remote.example/likes/1", true))
	require.NoError(t, hs.ProcessDirectBridgeEvent(context.Background(), fmt.Sprintf("ap-ReactionUndo:%d", reaction.ID)))
	event = MatrixEvent{}
	require.NoError(t, conn.First(&event, "event_id = ?", ref.EventID).Error)
	require.Nil(t, event.Content["m.relates_to"])
}

func TestMatrixReactionWithdrawalBeforeWorkerDoesNotFederate(t *testing.T) {
	hs, conn, delivery := outboundBridgeFixture(t)
	require.NoError(t, conn.AutoMigrate(&models.StatusReaction{}, &models.RemoteReactionActivity{}))
	txn := "pending-reaction"
	resp, me := hs.Event.sendRoomEvent(delivery.RoomID, "@alice:test.local", "m.reaction", &txn, map[string]any{"m.relates_to": map[string]any{"rel_type": "m.annotation", "event_id": delivery.EventID, "key": "👍"}}, nil, true, 0)
	require.Nil(t, me)
	redaction := MatrixEvent{EventID: "$remove-before-delivery", RoomID: delivery.RoomID, Sender: "@alice:test.local", Redacts: &resp.EventID}
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error { return hs.queueDirectBridgeRedaction(tx, &redaction) }))
	var job models.MatrixDMEventJob
	require.NoError(t, conn.Where("event_id = ? AND kind = ?", resp.EventID, "matrix_reaction").First(&job).Error)
	require.Equal(t, "skipped", job.State)
	require.NoError(t, hs.ProcessDirectBridgeEvent(context.Background(), job.ID))
	var count int64
	require.NoError(t, conn.Model(&models.StatusReaction{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestBridgeFederationRequiresPDUAcknowledgementAndRetries(t *testing.T) {
	hs, conn := directBridgeDB(t)
	seedDirectBridge(t, conn, 401, 2)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 401))
	var delivery models.MatrixDMDelivery
	require.NoError(t, conn.First(&delivery, "status_id = ?", 401).Error)
	var event MatrixEvent
	require.NoError(t, conn.First(&event, "event_id = ?", delivery.EventID).Error)
	accepted := false
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "PUT", r.Method)
		require.Contains(t, r.Header.Get("Authorization"), "X-Matrix")
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), "pdus")
		w.Header().Set("Content-Type", "application/json")
		pdu := map[string]any{}
		if !accepted {
			pdu["error"] = "temporary rejection"
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"pdus": map[string]any{event.EventID: pdu}}))
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "https://")
	old := newBridgeFederationClient
	defer func() { newBridgeFederationClient = old }()
	newBridgeFederationClient = func(hs *HomeServer) *FederationClient {
		c := NewFederationClient(hs)
		c.fedHTTPClient = server.Client()
		return c
	}
	require.NoError(t, conn.Create(&MatrixRoomMember{RoomID: event.RoomID, UserID: "@recipient:" + host, Membership: MembershipJoin}).Error)
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error { return hs.queueBridgeFederation(tx, &event) }))
	var row BridgeFederationDelivery
	require.NoError(t, conn.Where("event_id = ?", event.EventID).First(&row).Error)
	require.Error(t, hs.ProcessBridgeFederation(context.Background(), row.ID))
	require.NoError(t, conn.First(&row, "id = ?", row.ID).Error)
	require.Equal(t, "pending", row.State)
	accepted = true
	require.NoError(t, conn.Model(&row).Update("next_attempt_at", time.Now().Add(-time.Second)).Error)
	require.NoError(t, hs.ProcessBridgeFederation(context.Background(), row.ID))
	require.NoError(t, conn.First(&row, "id = ?", row.ID).Error)
	require.Equal(t, "delivered", row.State)
	require.Equal(t, 2, requests)
	require.NoError(t, hs.ProcessBridgeFederation(context.Background(), row.ID))
	require.Equal(t, 2, requests)
}

func TestBridgeReplaceWithdrawsAndRepostsThenDeletionClearsEditedRoot(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	original := sendOutboundTest(t, hs, source.RoomID, map[string]any{"msgtype": "m.text", "body": "original", "m.relates_to": map[string]any{"m.in_reply_to": map[string]any{"event_id": source.EventID}}})
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), original.EventID))
	var oldJob MatrixDMOutbound
	require.NoError(t, conn.First(&oldJob, "event_id = ?", original.EventID).Error)
	txn := "edit-once"
	edit, me := hs.Event.SendRoomEventFromDevice(source.RoomID, "@alice:test.local", "DEVICE", "m.room.message", &txn, map[string]any{"msgtype": "m.text", "body": "* changed", "m.new_content": map[string]any{"msgtype": "m.text", "body": "changed"}, "m.relates_to": map[string]any{"rel_type": "m.replace", "event_id": original.EventID}}, nil)
	require.Nil(t, me)
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), edit.EventID))
	var newJob MatrixDMOutbound
	require.NoError(t, conn.First(&newJob, "event_id = ?", edit.EventID).Error)
	require.NotEqual(t, oldJob.StatusID, newJob.StatusID)
	var count int64
	require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", oldJob.StatusID).Count(&count).Error)
	require.Zero(t, count)
	var replacement models.Status
	require.NoError(t, conn.Preload("Content").Preload("ToAccounts").First(&replacement, newJob.StatusID).Error)
	require.Equal(t, "changed", replacement.Content.Content)
	require.NotNil(t, replacement.InReplyToId)
	require.EqualValues(t, 11, *replacement.InReplyToId)
	var mapping models.MatrixDMDelivery
	require.NoError(t, resolveDirectEventStatus(conn, source.RoomID, original.EventID, &mapping))
	require.Equal(t, newJob.StatusID, mapping.StatusID)
	var notice MatrixEvent
	require.NoError(t, conn.Where("room_id = ? AND sender = ? AND event_type = ?", source.RoomID, "@_ap_bridge_"+strings.Split(strings.TrimPrefix(source.RoomID, "!apdm_"), ":")[0]+":test.local", EventTypeRoomMessage).Order("origin_server_ts DESC").First(&notice).Error)
	require.Contains(t, notice.Content["body"], "not an in-place")
	var oldDelete models.MatrixDMEventJob
	require.NoError(t, conn.First(&oldDelete, "id = ?", fmt.Sprintf("ap-delete:%d", oldJob.StatusID)).Error)
	require.Equal(t, "delivered", oldDelete.State)
	delTxn := "delete-edit-root"
	redaction, me := hs.Event.RedactEvent(source.RoomID, original.EventID, "@alice:test.local", "withdraw", &delTxn)
	require.Nil(t, me)
	var deleteJob models.MatrixDMEventJob
	require.NoError(t, conn.Where("event_id = ? AND kind = ?", redaction.EventID, "matrix_delete").First(&deleteJob).Error)
	require.NoError(t, hs.ProcessDirectBridgeEvent(context.Background(), deleteJob.ID))
	require.NoError(t, hs.ProcessDirectBridgeEvent(context.Background(), fmt.Sprintf("ap-delete:%d", newJob.StatusID)))
	for _, id := range []string{original.EventID, edit.EventID} {
		var event MatrixEvent
		require.NoError(t, conn.First(&event, "event_id = ?", id).Error)
		require.Nil(t, event.Content["body"])
	}
}

func TestInboundBridgeFavouriteStaysDistinctAndUndoRedacts(t *testing.T) {
	hs, conn, delivery := outboundBridgeFixture(t)
	require.NoError(t, conn.AutoMigrate(&models.FavoriteStatus{}, &models.StatusReaction{}))
	favorite := models.FavoriteStatus{Id: 831, AccountId: 1, StatusId: 11}
	require.NoError(t, conn.Create(&favorite).Error)
	require.NoError(t, hs.ProcessDirectBridgeEvent(context.Background(), "ap-Favorite:831"))
	require.NoError(t, hs.ProcessDirectBridgeEvent(context.Background(), "ap-Favorite:831"))
	var ref MatrixDMFavoriteReference
	require.NoError(t, conn.First(&ref, "favorite_id = ?", favorite.Id).Error)
	var event MatrixEvent
	require.NoError(t, conn.First(&event, "event_id = ?", ref.EventID).Error)
	relation := event.Content["m.relates_to"].(map[string]any)
	require.Equal(t, delivery.EventID, relation["event_id"])
	require.Equal(t, "Like", relation["key"])
	require.Equal(t, true, event.Content["org.community.activitypub"].(map[string]any)["favourite"])
	var count int64
	require.NoError(t, conn.Model(&models.StatusReaction{}).Count(&count).Error)
	require.Zero(t, count)
	var status models.Status
	require.NoError(t, conn.First(&status, 11).Error)
	require.EqualValues(t, 1, status.FavouritesCount)
	require.NoError(t, conn.Delete(&favorite).Error)
	require.NoError(t, hs.ProcessDirectBridgeEvent(context.Background(), "ap-FavoriteUndo:831"))
	event = MatrixEvent{}
	require.NoError(t, conn.First(&event, "event_id = ?", ref.EventID).Error)
	require.Nil(t, event.Content["m.relates_to"])
	require.NoError(t, conn.First(&status, 11).Error)
	require.Zero(t, status.FavouritesCount)
}

func TestBridgeDownloadFilenameIsMetadataNotPath(t *testing.T) {
	require.Equal(t, "media_sample.wav", bridgeDownloadFilename(`inline; filename=media_sample.wav`))
	require.Equal(t, "song name.wav", bridgeDownloadFilename(`attachment; filename*=UTF-8''song%20name.wav`))
	require.NotContains(t, bridgeDownloadFilename(`inline; filename="../../secret.wav"`), "/")
	require.Empty(t, bridgeDownloadFilename(`invalid; filename="`))
	require.Empty(t, bridgeDownloadFilename(""))
}
