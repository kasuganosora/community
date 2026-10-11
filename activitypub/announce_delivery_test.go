package activitypub

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestOutboundBoostTransactionDeliversSignedAnnounceAndUndo(t *testing.T) {
	conn := relayDB(t)
	booster := outboundAccount(t, conn, "chain_booster", false)
	originalAuthor := outboundAccount(t, conn, "original_author", true)
	follower := outboundAccount(t, conn, "accepted_follower", true)
	pending := outboundAccount(t, conn, "pending_follower", true)
	require.NoError(t, conn.Create(&models.Follow{AccountId: follower.Id, FollowId: booster.Id, Status: models.FollowStatusFollowed}).Error)
	require.NoError(t, conn.Create(&models.Follow{AccountId: pending.Id, FollowId: booster.Id, Status: models.FollowStatusRequested}).Error)
	originalURI := "https://remote.example/notes/opaque-original-id"
	original := &models.Status{AccountId: originalAuthor.Id, Domain: originalAuthor.Domain, OriginUri: &originalURI, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "remote original"}}
	require.NoError(t, conn.Create(original).Error)
	require.Empty(t, outboxEvents(t, conn), "remote ingestion must not generate a local Create")
	boost := &models.Status{AccountId: booster.Id, ReblogId: &original.Id, Visibility: models.StatusVisibilityPublic}
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error { return tx.Create(boost).Error }))
	events := outboxEvents(t, conn)
	require.Len(t, events, 1)
	require.Equal(t, "Announce", events[0].Kind)
	require.ElementsMatch(t, []uint64{originalAuthor.Id, follower.Id}, events[0].RecipientIDs)
	key, err := localAccountKey(booster)
	require.NoError(t, err)
	block, _ := pem.Decode([]byte(key.PublicKey))
	require.NotNil(t, block)
	publicKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	require.NoError(t, err)
	old := sharedHTTPClient
	t.Cleanup(func() { sharedHTTPClient = old })
	inboxes := map[string]string{originalAuthor.OriginId: "https://remote.example/original-inbox", follower.OriginId: "https://remote.example/follower-inbox"}
	sent := map[string][]map[string]any{}
	sharedHTTPClient = &http.Client{Transport: outboundTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			inbox, known := inboxes[r.URL.String()]
			require.True(t, known, "only intended actors may be discovered")
			actor, _ := json.Marshal(map[string]any{"id": r.URL.String(), "inbox": inbox})
			return outboundResponse(200, string(actor)), nil
		}
		require.Equal(t, http.MethodPost, r.Method)
		require.Contains(t, []string{"/original-inbox", "/follower-inbox"}, r.URL.Path)
		payload, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		r.Body = io.NopCloser(bytes.NewReader(payload))
		require.NoError(t, VerifyRequest(r, publicKey.(*rsa.PublicKey)))
		require.Contains(t, r.Header.Get("Signature"), accountActorURI(booster, systemsettings.GetBaseURL())+"#main-key")
		var activity map[string]any
		require.NoError(t, json.Unmarshal(payload, &activity))
		require.NotEqual(t, "Create", activity["type"], "boost is an Announce, not a new Note wrapper")
		sent[r.URL.Path] = append(sent[r.URL.Path], activity)
		return outboundResponse(202, ""), nil
	})}
	worker := &OutboundWorker{}
	queue := NewDeliveryQueue(nil, "", 0)
	worker.processPending()
	queue.processPending()
	for _, path := range []string{"/original-inbox", "/follower-inbox"} {
		require.Len(t, sent[path], 1)
		activity := sent[path][0]
		require.Equal(t, "Announce", activity["type"])
		require.Equal(t, originalURI, activity["object"], "remote URI must remain the original opaque identifier")
		require.Equal(t, accountActorURI(booster, systemsettings.GetBaseURL()), activity["actor"])
	}
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error { return tx.Delete(boost).Error }))
	events = outboxEvents(t, conn)
	require.Len(t, events, 2)
	require.Equal(t, "AnnounceUndo", events[1].Kind)
	worker.processPending()
	queue.processPending()
	for _, path := range []string{"/original-inbox", "/follower-inbox"} {
		require.Len(t, sent[path], 2)
		undo := sent[path][1]
		require.Equal(t, "Undo", undo["type"])
		announce := undo["object"].(map[string]any)
		require.Equal(t, sent[path][0]["id"], announce["id"])
		require.Equal(t, originalURI, announce["object"])
	}
	var deliveries []Delivery
	require.NoError(t, conn.Find(&deliveries).Error)
	require.Len(t, deliveries, 4)
	for _, delivery := range deliveries {
		require.Equal(t, DeliveryStatusDelivered, delivery.Status)
		require.Empty(t, delivery.LastError)
	}
	for _, event := range outboxEvents(t, conn) {
		require.Equal(t, "processed", event.State)
	}
}
