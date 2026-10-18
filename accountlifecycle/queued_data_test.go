package accountlifecycle

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestDeletionQueueScrubMakesBoundedProgressAndPreservesRevocations(t *testing.T) {
	conn, a := deletionDB(t)
	require.NoError(t, conn.AutoMigrate(&activitypub.Delivery{}))
	require.NoError(t, RequestDeletion(context.Background(), conn, a.Id, a.UserId))
	for i := 0; i < 105; i++ {
		require.NoError(t, conn.Create(&models.FederationOutbox{AccountID: a.Id, Kind: "Create", Payload: `{"private":"post body"}`, State: "done"}).Error)
	}
	auth, err := json.Marshal(activitypub.DeliveryAuthorization{AccountID: a.Id, Kind: "Create"})
	require.NoError(t, err)
	future := time.Now().Add(time.Minute)
	delivery := activitypub.Delivery{ActivityID: "private", TargetInbox: "https://remote.test/inbox", Authorization: string(auth), Payload: `{"private":"body"}`, Status: activitypub.DeliveryStatusProcessing, NextRetryAt: &future}
	require.NoError(t, conn.Create(&delivery).Error)
	for i := 0; i < 2; i++ {
		done, err := eraseQueuedContent(conn, a.Id)
		require.NoError(t, err)
		require.False(t, done)
	}
	var job models.AccountDeletion
	require.NoError(t, conn.First(&job).Error)
	require.EqualValues(t, 106, job.OutboxCursor)
	done, err := eraseQueuedContent(conn, a.Id)
	require.ErrorContains(t, err, "active federation delivery")
	require.False(t, done)
	require.NoError(t, conn.Model(&delivery).Update("status", activitypub.DeliveryStatusDelivered).Error)
	done, err = eraseQueuedContent(conn, a.Id)
	require.NoError(t, err)
	require.False(t, done)
	done, err = eraseQueuedContent(conn, a.Id)
	require.NoError(t, err)
	require.True(t, done)
	require.NoError(t, conn.First(&delivery).Error)
	require.Equal(t, "{}", delivery.Payload)
	require.EqualValues(t, "cancelled", delivery.Status)
	var count int64
	require.NoError(t, conn.Model(&models.FederationOutbox{}).Where("kind = ? AND state = ? AND payload = ?", "Create", "cancelled", "{}").Count(&count).Error)
	require.EqualValues(t, 105, count)
	require.NoError(t, conn.Model(&models.FederationOutbox{}).Where("kind = ? AND state = ?", "AccountDelete", "pending").Count(&count).Error)
	require.EqualValues(t, 1, count)
}
