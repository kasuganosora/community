package activitypub

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
)

func TestDeliveryBackoffDoesNotStarveOtherInbox(t *testing.T) {
	conn := outboundDB(t)
	author := outboundAccount(t, conn, "fair_sender", false)
	actor := systemsettings.GetBaseURL() + "/users/" + author.Username
	payload, err := json.Marshal(map[string]any{"id": actor + "/activity/1", "actor": actor, "type": "Like", "object": "https://remote.example/note/1"})
	require.NoError(t, err)
	future := time.Now().Add(time.Hour)
	head := Delivery{ActivityID: actor + "/activity/1", TargetInbox: "https://offline.example/inbox", Payload: string(payload), NextRetryAt: &future}
	require.NoError(t, conn.Create(&head).Error)
	for range 12 {
		require.NoError(t, conn.Create(&Delivery{TargetInbox: head.TargetInbox, Payload: string(payload)}).Error)
	}
	healthy := Delivery{ActivityID: actor + "/activity/2", TargetInbox: "https://healthy.example/inbox", Payload: string(payload)}
	require.NoError(t, conn.Create(&healthy).Error)
	old := sharedHTTPClient
	t.Cleanup(func() { sharedHTTPClient = old })
	posts := 0
	sharedHTTPClient = &http.Client{Transport: outboundTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, healthy.TargetInbox, r.URL.String(), "offline queue must retain its order and backoff")
		posts++
		return outboundResponse(202, ""), nil
	})}
	NewDeliveryQueue(nil, "", 0).processPending()
	require.Equal(t, 1, posts, "a deferred inbox must not occupy every batch slot")
	require.NoError(t, conn.First(&healthy, healthy.ID).Error)
	require.Equal(t, DeliveryStatusDelivered, healthy.Status)
}

func TestDeliveryRestartsAfterLostResponseWithStableIdentity(t *testing.T) {
	conn := outboundDB(t)
	author := outboundAccount(t, conn, "retry_sender", false)
	actor := systemsettings.GetBaseURL() + "/users/" + author.Username
	activityID := actor + "/reactions/100"
	payload, err := json.Marshal(map[string]any{"id": activityID, "actor": actor, "type": "Like", "content": "👍", "object": "https://remote.example/notes/100"})
	require.NoError(t, err)
	delivery := Delivery{ActivityID: activityID, TargetInbox: "https://remote.example/inbox", Payload: string(payload)}
	require.NoError(t, conn.Create(&delivery).Error)
	old := sharedHTTPClient
	t.Cleanup(func() { sharedHTTPClient = old })
	var sent []string
	sharedHTTPClient = &http.Client{Transport: outboundTransport(func(r *http.Request) (*http.Response, error) {
		var activity map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&activity))
		sent = append(sent, activity["id"].(string))
		if len(sent) == 1 {
			// The peer accepted the request, but its response was lost.
			return nil, io.ErrUnexpectedEOF
		}
		return outboundResponse(202, ""), nil
	})}
	NewDeliveryQueue(nil, "", 0).processPending()
	require.NoError(t, conn.First(&delivery, delivery.ID).Error)
	require.Equal(t, DeliveryStatusPending, delivery.Status)
	require.Equal(t, 1, delivery.Retries)
	NewDeliveryQueue(nil, "", 0).processPending()
	require.Len(t, sent, 1, "a fresh worker must respect persisted backoff")
	// Simulate a worker crash with a persisted lease; no process memory is reused.
	future := time.Now().Add(time.Minute)
	require.NoError(t, conn.Model(&delivery).Updates(map[string]any{"status": DeliveryStatusProcessing, "next_retry_at": future}).Error)
	NewDeliveryQueue(nil, "", 0).processPending()
	require.Len(t, sent, 1, "an active lease must not be stolen")
	past := time.Now().Add(-time.Second)
	require.NoError(t, conn.Model(&delivery).Update("next_retry_at", past).Error)
	NewDeliveryQueue(nil, "", 0).processPending()
	require.Equal(t, []string{activityID, activityID}, sent, "a retry must retain its deduplication identity")
	var saved Delivery
	require.NoError(t, conn.First(&saved, delivery.ID).Error)
	require.Equal(t, DeliveryStatusDelivered, saved.Status)
	require.Nil(t, saved.NextRetryAt)
}

func TestOutboxBackoffDoesNotStarveOtherAuthor(t *testing.T) {
	conn := outboundDB(t)
	blocked := outboundAccount(t, conn, "blocked_writer", false)
	healthy := outboundAccount(t, conn, "healthy_writer", false)
	future := time.Now().Add(time.Hour)
	require.NoError(t, conn.Create(&models.FederationOutbox{AccountID: blocked.Id, Kind: "ProfileUpdate", Payload: "{}", NextAttemptAt: &future}).Error)
	for range 22 {
		require.NoError(t, conn.Create(&models.FederationOutbox{AccountID: blocked.Id, Kind: "ProfileUpdate", Payload: "{}"}).Error)
	}
	snapshot, err := json.Marshal(models.FederationProfileSnapshot{Account: *healthy})
	require.NoError(t, err)
	event := models.FederationOutbox{AccountID: healthy.Id, ObjectID: healthy.Id, Kind: "ProfileUpdate", Payload: string(snapshot)}
	require.NoError(t, conn.Create(&event).Error)
	(&OutboundWorker{}).processPending()
	require.NoError(t, conn.First(&event, event.ID).Error)
	require.Equal(t, "processed", event.State, "a deferred author must not block unrelated authors")
}
