package homeserver

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestDirectDeliveryTracksTransportNotBridgeAcceptance(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	require.NoError(t, conn.AutoMigrate(&BridgeFederationDelivery{}, &activitypub.Delivery{}))
	sent := sendOutboundTest(t, hs, source.RoomID, map[string]any{"msgtype": "m.text", "body": "private"})
	ctx := context.Background()
	state, err := hs.GetDirectDeliveryStatus(ctx, 2, source.RoomID, sent.EventID)
	require.NoError(t, err)
	require.Equal(t, "queued", state.State)
	require.NoError(t, hs.ProcessOutboundDirect(ctx, sent.EventID))
	state, err = hs.GetDirectDeliveryStatus(ctx, 2, source.RoomID, sent.EventID)
	require.NoError(t, err)
	require.Equal(t, "queued", state.State)
	require.False(t, state.RemoteReceived)
	var job MatrixDMOutbound
	require.NoError(t, conn.First(&job, "event_id = ?", sent.EventID).Error)
	var outbox models.FederationOutbox
	require.NoError(t, conn.First(&outbox, "object_id = ? AND kind = ?", job.StatusID, "Create").Error)
	var author models.Account
	require.NoError(t, conn.First(&author, 2).Error)
	activityID := fmt.Sprintf("%s/activities/%d", author.ActorURI(systemsettings.GetBaseURL()), outbox.ID)
	require.NoError(t, conn.Model(&outbox).Update("state", "processed").Error)
	delivery := activitypub.Delivery{ActivityID: activityID, TargetInbox: "https://remote.example/inbox", Payload: "{}", Status: activitypub.DeliveryStatusProcessing}
	require.NoError(t, conn.Create(&delivery).Error)
	state, err = hs.GetDirectDeliveryStatus(ctx, 2, source.RoomID, sent.EventID)
	require.NoError(t, err)
	require.Equal(t, "sending", state.State)
	require.NoError(t, conn.Model(&delivery).Updates(map[string]any{"status": "failed", "last_error": "secret upstream body and private host", "retries": 5}).Error)
	state, err = hs.GetDirectDeliveryStatus(ctx, 2, source.RoomID, sent.EventID)
	require.NoError(t, err)
	require.Equal(t, "failed", state.State)
	require.Equal(t, "federation_delivery_failed", state.ErrorCode)
	require.True(t, state.CanRetry)
	for i := 0; i < 2; i++ {
		state, err = hs.RetryDirectDelivery(ctx, 2, source.RoomID, sent.EventID)
		require.NoError(t, err)
		require.Equal(t, "queued", state.State)
	}
	var count int64
	require.NoError(t, conn.Model(&models.FederationOutbox{}).Where("object_id = ?", job.StatusID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, conn.Model(&activitypub.Delivery{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, conn.Model(&delivery).Updates(map[string]any{"status": "delivered", "last_error": ""}).Error)
	state, err = hs.GetDirectDeliveryStatus(ctx, 2, source.RoomID, sent.EventID)
	require.NoError(t, err)
	require.Equal(t, "delivered", state.State)
	require.True(t, state.RemoteReceived)
	require.False(t, state.CanRetry)

	matrix := BridgeFederationDelivery{ID: "matrix-peer", EventID: sent.EventID, ServerName: "matrix.example", State: "pending", NextAttemptAt: time.Now()}
	require.NoError(t, conn.Create(&matrix).Error)
	state, err = hs.GetDirectDeliveryStatus(ctx, 2, source.RoomID, sent.EventID)
	require.NoError(t, err)
	require.Equal(t, "queued", state.State)
	require.False(t, state.RemoteReceived, "AP acceptance does not acknowledge pending Matrix transport")
	require.NoError(t, conn.Model(&matrix).Update("state", "failed").Error)
	state, err = hs.GetDirectDeliveryStatus(ctx, 2, source.RoomID, sent.EventID)
	require.NoError(t, err)
	require.Equal(t, "matrix_delivery_failed", state.ErrorCode)
	state, err = hs.RetryDirectDelivery(ctx, 2, source.RoomID, sent.EventID)
	require.NoError(t, err)
	require.Equal(t, "queued", state.State)
	require.NoError(t, conn.Model(&matrix).Update("state", "delivered").Error)
	require.NoError(t, conn.Model(&delivery).Update("last_error", "recipient authorization revoked").Error)
	state, err = hs.GetDirectDeliveryStatus(ctx, 2, source.RoomID, sent.EventID)
	require.NoError(t, err)
	require.Equal(t, "skipped", state.State)
	require.False(t, state.RemoteReceived)
	_, err = hs.GetDirectDeliveryStatus(ctx, 3, source.RoomID, sent.EventID)
	require.Error(t, err)
	require.NoError(t, conn.Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ?", source.RoomID, "@alice:test.local").Update("membership", MembershipLeave).Error)
	_, err = hs.RetryDirectDelivery(ctx, 2, source.RoomID, sent.EventID)
	require.Error(t, err)
}
func TestDirectDeliveryRetryLeavesActiveLeaseUntouched(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	require.NoError(t, conn.AutoMigrate(&BridgeFederationDelivery{}, &activitypub.Delivery{}))
	sent := sendOutboundTest(t, hs, source.RoomID, map[string]any{"msgtype": "m.text", "body": "lease"})
	require.NoError(t, hs.ProcessOutboundDirect(context.Background(), sent.EventID))
	var job MatrixDMOutbound
	require.NoError(t, conn.First(&job, "event_id = ?", sent.EventID).Error)
	var outbox models.FederationOutbox
	require.NoError(t, conn.First(&outbox, "object_id = ?", job.StatusID).Error)
	deadline := time.Now().Add(time.Minute).UTC().Truncate(time.Millisecond)
	require.NoError(t, conn.Model(&outbox).Updates(map[string]any{"state": "processing", "next_attempt_at": deadline}).Error)
	// PostgreSQL rejects row locks on aggregate queries. Inspect the GORM
	// statement before dialect rendering (SQLite intentionally drops FOR).
	require.NoError(t, conn.Callback().Query().Before("gorm:query").Register("reject-aggregate-row-lock", func(query *gorm.DB) {
		if _, aggregate := query.Statement.Dest.(*int64); aggregate {
			if _, locked := query.Statement.Clauses["FOR"]; locked {
				query.AddError(errors.New("aggregate query inherited row lock"))
			}
		}
	}))
	defer conn.Callback().Query().Remove("reject-aggregate-row-lock")
	// Positive control: GORM retains the broad clause across Session clones;
	// this reproduces the former retry call without relying on SQLite SQL.
	_, inheritedErr := hs.directDeliveryJob(conn.Clauses(clause.Locking{Strength: "UPDATE"}).Session(&gorm.Session{}), 2, source.RoomID, sent.EventID)
	require.ErrorContains(t, inheritedErr, "aggregate query inherited row lock")
	result, err := hs.RetryDirectDelivery(context.Background(), 2, source.RoomID, sent.EventID)
	require.NoError(t, err)
	require.Equal(t, "sending", result.State)
	require.NoError(t, conn.First(&outbox, outbox.ID).Error)
	require.WithinDuration(t, deadline, *outbox.NextAttemptAt, time.Millisecond)
}

func TestDirectDeliveryFeedbackIsPrivateBoundedAndIdempotent(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	require.NoError(t, conn.AutoMigrate(&BridgeFederationDelivery{}, &activitypub.Delivery{}, &MatrixAccountData{}))
	sent := sendOutboundTest(t, hs, source.RoomID, map[string]any{"msgtype": "m.text", "body": "NEVER_PUBLISH_THIS"})
	require.NoError(t, conn.Model(&MatrixDMOutbound{}).Where("event_id = ?", sent.EventID).Updates(map[string]any{"state": "failed", "last_error": "PRIVATE_UPSTREAM_ERROR"}).Error)
	hs.publishDirectDeliveryFeedback(context.Background())
	var data []MatrixAccountData
	require.NoError(t, conn.Find(&data).Error)
	require.Len(t, data, 1)
	require.Equal(t, "@alice:test.local", data[0].UserID)
	require.Equal(t, "org.community.direct_delivery", data[0].DataType)
	require.NotContains(t, fmt.Sprint(data[0].Content), "NEVER_PUBLISH_THIS")
	require.NotContains(t, fmt.Sprint(data[0].Content), "PRIVATE_UPSTREAM_ERROR")
	require.Contains(t, fmt.Sprint(data[0].Content), "bridge_failed")
	old := *data[0].UpdatedAt
	hs.publishDirectDeliveryFeedback(context.Background())
	require.NoError(t, conn.Find(&data).Error)
	require.Len(t, data, 1)
	require.Equal(t, old, *data[0].UpdatedAt)
	state, err := hs.RetryDirectDelivery(context.Background(), 2, source.RoomID, sent.EventID)
	require.NoError(t, err)
	require.Equal(t, "queued", state.State)
	hs.publishDirectDeliveryFeedback(context.Background())
	require.NoError(t, conn.Find(&data).Error)
	require.Len(t, data, 1)
	entries, ok := data[0].Content["events"].([]any)
	require.True(t, ok)
	require.Len(t, entries, 1)
	require.Equal(t, "queued", entries[0].(map[string]any)["state"])
	statuses, err := hs.BatchDirectDeliveryStatuses(context.Background(), 2, source.RoomID, []string{sent.EventID, "$missing"})
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	require.Equal(t, "queued", statuses[sent.EventID].State)
	statuses, err = hs.BatchDirectDeliveryStatuses(context.Background(), 3, source.RoomID, []string{sent.EventID})
	require.NoError(t, err)
	require.Empty(t, statuses)
}

func TestDirectDeliveryFeedbackRollbackAndBound(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	require.NoError(t, conn.AutoMigrate(&BridgeFederationDelivery{}, &activitypub.Delivery{}, &MatrixAccountData{}))
	sent := sendOutboundTest(t, hs, source.RoomID, map[string]any{"msgtype": "m.text", "body": "delivery"})
	require.NoError(t, conn.Model(&MatrixDMOutbound{}).Where("event_id = ?", sent.EventID).Update("state", "failed").Error)
	entries := []any{}
	for i := 0; i < 40; i++ {
		entries = append(entries, map[string]any{"event_id": fmt.Sprintf("$old-%d", i), "state": "delivered"})
	}
	data := MatrixAccountData{UserID: "@alice:test.local", RoomID: &source.RoomID, DataType: "org.community.direct_delivery", Content: map[string]any{"events": entries}}
	require.NoError(t, conn.Create(&data).Error)
	require.NoError(t, conn.Callback().Update().Before("gorm:update").Register("fail-feedback", func(tx *gorm.DB) {
		if tx.Statement.Table == "matrix_account_data" {
			tx.AddError(errors.New("injected feedback write failure"))
		}
	}))
	hs.publishDirectDeliveryFeedback(context.Background())
	var job MatrixDMOutbound
	require.NoError(t, conn.First(&job, "event_id = ?", sent.EventID).Error)
	require.Empty(t, job.DeliveryFeedbackState, "failed feedback must remain resumable")
	require.NoError(t, conn.Callback().Update().Remove("fail-feedback"))
	hs.publishDirectDeliveryFeedback(context.Background())
	require.NoError(t, conn.First(&data, data.ID).Error)
	actual := data.Content["events"].([]any)
	require.Len(t, actual, 32)
	require.Equal(t, sent.EventID, actual[31].(map[string]any)["event_id"])
	require.NoError(t, conn.First(&job, "event_id = ?", sent.EventID).Error)
	require.Equal(t, "failed", job.DeliveryFeedbackState)
}

func TestDirectDeliveryHistorySurvivesRecipientDepartureWithoutRetry(t *testing.T) {
	hs, conn, source := outboundBridgeFixture(t)
	require.NoError(t, conn.AutoMigrate(&BridgeFederationDelivery{}, &activitypub.Delivery{}))
	sent := sendOutboundTest(t, hs, source.RoomID, map[string]any{"msgtype": "m.text", "body": "old audience"})
	require.NoError(t, conn.Model(&MatrixDMOutbound{}).Where("event_id = ?", sent.EventID).Update("state", "failed").Error)
	require.NoError(t, conn.Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id <> ? AND user_id NOT LIKE ?", source.RoomID, "@alice:test.local", "@_ap_bridge_%").Update("membership", MembershipLeave).Error)
	statuses, err := hs.BatchDirectDeliveryStatuses(context.Background(), 2, source.RoomID, []string{sent.EventID})
	require.NoError(t, err)
	require.Equal(t, "failed", statuses[sent.EventID].State)
	require.False(t, statuses[sent.EventID].CanRetry)
	_, err = hs.RetryDirectDelivery(context.Background(), 2, source.RoomID, sent.EventID)
	require.Error(t, err)
	require.NoError(t, conn.Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ?", source.RoomID, "@alice:test.local").Update("membership", MembershipLeave).Error)
	_, err = hs.BatchDirectDeliveryStatuses(context.Background(), 2, source.RoomID, []string{sent.EventID})
	require.Error(t, err)
}
