package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func feedJobsDB(t *testing.T) (*gorm.DB, *models.Account) {
	conn, account := operationsDB(t)
	require.NoError(t, conn.AutoMigrate(&models.FeedEvent{}, &models.FeedBatch{}))
	return conn, account
}
func feedJobRequest(t *testing.T, a *models.Account, kind string, id uint64, action string) int {
	c, w := newJSONContext(t, "POST", gin.Params{{Key: "kind", Value: kind}, {Key: "id", Value: fmt.Sprint(id)}}, gin.H{"action": action, "confirm": true})
	c.Set("account", a)
	FederationJobAction(c)
	return w.Code
}
func queueAdminFeed(t *testing.T, conn *gorm.DB, created time.Time) (*models.FeedEvent, *models.FeedBatch) {
	event := &models.FeedEvent{EventKey: fmt.Sprintf("admin-%d", created.UnixNano()), Kind: models.FeedStatusCreate, ObjectType: "status", ObjectID: 700, ActorID: 20, Metadata: models.FeedEventMetadata{Reason: "PRIVATE_METADATA"}, FeedLease: models.FeedLease{State: "failed", CreatedAt: created, LeaseToken: "PRIVATE_LEASE"}}
	require.NoError(t, conn.Create(event).Error)
	batch := &models.FeedBatch{EventID: event.ID, TargetKind: "home", Recipients: []models.FeedRecipient{{TimelineID: "PRIVATE_RECIPIENT"}}, FeedLease: models.FeedLease{State: "failed"}}
	require.NoError(t, conn.Create(batch).Error)
	return event, batch
}
func TestFeedJobsMetadataAndFreshPermission(t *testing.T) {
	conn, a := feedJobsDB(t)
	event, batch := queueAdminFeed(t, conn, time.Now())
	for _, kind := range []string{"feed-event", "feed-batch"} {
		c, w := newGetContext(t, nil, "kind="+kind+"&limit=1&page=1")
		c.Set("account", a)
		FederationJobs(c)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.NotContains(t, w.Body.String(), "PRIVATE_")
		var response struct {
			Items []map[string]any
			Total int64
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Len(t, response.Items, 1)
		require.EqualValues(t, 1, response.Total)
		require.Equal(t, true, response.Items[0]["can_retry"])
		if kind == "feed-batch" {
			require.Equal(t, fmt.Sprint(event.ID), response.Items[0]["parent_id"])
		}
	}
	require.NoError(t, conn.Model(&models.User{}).Where("id = ?", a.UserId).UpdateColumn("is_admin", false).Error)
	require.Equal(t, 403, feedJobRequest(t, a, "feed-batch", batch.ID, "retry"))
	c, w := newGetContext(t, nil, "kind=feed-event")
	c.Set("account", a)
	FederationJobs(c)
	require.Equal(t, 403, w.Code)
	c, w = newGetContext(t, nil, "kind=feed-event")
	FederationJobs(c)
	require.Equal(t, 401, w.Code)
}
func TestFeedJobsRetryAndCancellationKeepDependenciesConsistent(t *testing.T) {
	conn, a := feedJobsDB(t)
	event, batch := queueAdminFeed(t, conn, time.Now())
	done := models.FeedBatch{EventID: event.ID, TargetKind: "home", BatchNo: 1, Recipients: []models.FeedRecipient{}, FeedLease: models.FeedLease{State: "complete", LeaseGeneration: 6}}
	require.NoError(t, conn.Create(&done).Error)
	require.Equal(t, 200, feedJobRequest(t, a, "feed-batch", batch.ID, "retry"))
	require.NoError(t, conn.First(event, event.ID).Error)
	require.Equal(t, "pending", event.State)
	require.EqualValues(t, 1, event.LeaseGeneration)
	require.NoError(t, conn.First(batch, batch.ID).Error)
	require.Equal(t, "pending", batch.State)
	require.Empty(t, batch.LeaseToken)
	require.Equal(t, 200, feedJobRequest(t, a, "feed-batch", batch.ID, "cancel"))
	require.NoError(t, conn.First(event, event.ID).Error)
	require.Equal(t, "cancelled", event.State)
	require.NoError(t, conn.First(batch, batch.ID).Error)
	require.Equal(t, "cancelled", batch.State)
	require.NoError(t, conn.First(&done, done.ID).Error)
	require.Equal(t, "complete", done.State)
	require.EqualValues(t, 6, done.LeaseGeneration)
	require.Equal(t, 409, feedJobRequest(t, a, "feed-event", event.ID, "retry"))
	claimed, err := models.ClaimFeedBatches(conn, 10, time.Minute)
	require.NoError(t, err)
	require.Empty(t, claimed)
}
func TestFeedJobsRefuseRunningOrLeasedDependenciesWithoutPartialMutation(t *testing.T) {
	for _, tc := range []struct {
		state  string
		future bool
	}{{"processing", true}, {"processing", false}, {"pending", true}} {
		t.Run(fmt.Sprintf("%s-%v", tc.state, tc.future), func(t *testing.T) {
			conn, a := feedJobsDB(t)
			event, batch := queueAdminFeed(t, conn, time.Now())
			expires := time.Now().Add(-time.Minute)
			if tc.future {
				expires = time.Now().Add(time.Minute)
			}
			require.NoError(t, conn.Model(batch).Updates(map[string]any{"state": tc.state, "lease_expires_at": expires}).Error)
			for _, action := range []string{"cancel", "retry"} {
				require.Equal(t, 409, feedJobRequest(t, a, "feed-event", event.ID, action))
			}
			require.NoError(t, conn.First(event, event.ID).Error)
			require.Equal(t, "failed", event.State)
			require.Zero(t, event.LeaseGeneration)
		})
	}
}
func TestFeedJobsExpiredRetryQueuesCurrentStateReconciliationAtomically(t *testing.T) {
	conn, a := feedJobsDB(t)
	event, batch := queueAdminFeed(t, conn, time.Now().Add(-8*24*time.Hour))
	require.Equal(t, 200, feedJobRequest(t, a, "feed-batch", batch.ID, "retry"))
	require.NoError(t, conn.First(event, event.ID).Error)
	require.Equal(t, "cancelled", event.State)
	require.NoError(t, conn.First(batch, batch.ID).Error)
	require.Equal(t, "cancelled", batch.State)
	var fresh models.FeedEvent
	require.NoError(t, conn.Where("id <> ?", event.ID).First(&fresh).Error)
	require.Equal(t, models.FeedInvalidate, fresh.Kind)
	require.Equal(t, "admin_reconcile", fresh.Metadata.Reason)
	require.Zero(t, fresh.ActorID)
	require.Zero(t, fresh.ObjectID)
	require.Empty(t, fresh.Metadata.RecipientIDs)
	require.Equal(t, "pending", fresh.State)
	require.True(t, fresh.CreatedAt.After(time.Now().Add(-time.Minute)))
	require.Equal(t, 409, feedJobRequest(t, a, "feed-event", event.ID, "retry"))
	var count int64
	require.NoError(t, conn.Model(&models.FeedEvent{}).Count(&count).Error)
	require.EqualValues(t, 2, count)
}
func TestFeedJobsReconciliationFailureRollsBackCancellation(t *testing.T) {
	conn, a := feedJobsDB(t)
	event, batch := queueAdminFeed(t, conn, time.Now().Add(-8*24*time.Hour))
	require.NoError(t, conn.Callback().Create().Before("gorm:create").Register("fail_reconcile", func(tx *gorm.DB) {
		if tx.Statement.Table == "feed_events" {
			tx.AddError(errors.New("injected reconciliation failure"))
		}
	}))
	t.Cleanup(func() { conn.Callback().Create().Remove("fail_reconcile") })
	require.Equal(t, 500, feedJobRequest(t, a, "feed-event", event.ID, "retry"))
	require.NoError(t, conn.First(event, event.ID).Error)
	require.Equal(t, "failed", event.State)
	require.NoError(t, conn.First(batch, batch.ID).Error)
	require.Equal(t, "failed", batch.State)
}
