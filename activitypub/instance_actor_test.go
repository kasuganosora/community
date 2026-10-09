package activitypub

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/require"
)

func TestInstanceActorServedAsApplication(t *testing.T) {
	w := newRelayWorld(t)
	_, _, err := EnsureInstanceActor(context.Background())
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	w.engine.ServeHTTP(rec, httptest.NewRequest("GET", "/actor", nil))
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/activity+json")
	var actor map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &actor))
	require.Equal(t, e2eBase+"/actor", actor["id"])
	require.Equal(t, "Application", actor["type"])
	require.Equal(t, "c.hime.test", actor["preferredUsername"])
	require.Equal(t, e2eBase+"/actor/inbox", actor["inbox"])
	require.Equal(t, e2eBase+"/actor/outbox", actor["outbox"])
	require.Equal(t, e2eBase+"/actor/followers", actor["followers"])
	require.Equal(t, e2eBase+"/inbox", actor["endpoints"].(map[string]any)["sharedInbox"])
	key := actor["publicKey"].(map[string]any)
	require.Equal(t, e2eBase+"/actor#main-key", key["id"])
	require.Equal(t, e2eBase+"/actor", key["owner"])
	require.Contains(t, key["publicKeyPem"], "PUBLIC KEY")

	// /users/<username> must not expose a second identity for it.
	rec = httptest.NewRecorder()
	w.engine.ServeHTTP(rec, httptest.NewRequest("GET", "/users/c.hime.test", nil))
	require.Equal(t, 404, rec.Code)

	// WebFinger by acct and by actor URL.
	for _, resource := range []string{"acct:c.hime.test@c.hime.test", e2eBase + "/actor"} {
		rec = httptest.NewRecorder()
		w.engine.ServeHTTP(rec, httptest.NewRequest("GET", "/.well-known/webfinger?resource="+resource, nil))
		require.Equal(t, 200, rec.Code, resource)
		var jrd WebFingerResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &jrd))
		require.Equal(t, "acct:c.hime.test@c.hime.test", jrd.Subject)
		require.Equal(t, e2eBase+"/actor", jrd.Links[0].Href)
	}

	// The delivery queue can sign as <base>/actor.
	client, err := (&DeliveryQueue{}).signingClient(map[string]any{"actor": e2eBase + "/actor"})
	require.NoError(t, err)
	require.Equal(t, e2eBase+"/actor#main-key", client.signingKeyID())

	// system.proxy keeps working next to it.
	rec = httptest.NewRecorder()
	_, _, err = EnsureProxyAccount(context.Background())
	require.NoError(t, err)
	w.engine.ServeHTTP(rec, httptest.NewRequest("GET", "/users/system.proxy", nil))
	require.Equal(t, 200, rec.Code)
}

func TestEnsureInstanceActorIdempotentAndConcurrent(t *testing.T) {
	w := newRelayWorld(t)
	ensure := func(ctx context.Context) (*models.Account, *models.AccountKey, error) {
		resetInstanceActorCache()
		return EnsureInstanceActor(ctx)
	}
	if testutil.UsePostgres() {
		// Replicas: bypass the in-process mutex; only the advisory lock serialises.
		ensure = func(ctx context.Context) (*models.Account, *models.AccountKey, error) {
			return ensureInstanceActorWithRetry(ctx, w.conn)
		}
	}
	const workers = 8
	var wg sync.WaitGroup
	ids := make([]uint64, workers)
	pubs := make([]string, workers)
	errs := make([]error, workers)
	start := make(chan struct{})
	for i := range workers {
		wg.Go(func() {
			<-start
			a, k, err := ensure(context.Background())
			errs[i] = err
			if err == nil {
				ids[i], pubs[i] = a.Id, k.PublicKey
			}
		})
	}
	close(start)
	wg.Wait()
	for i := range workers {
		require.NoError(t, errs[i])
		require.Equal(t, ids[0], ids[i])
		require.Equal(t, pubs[0], pubs[i])
	}
	again, _, err := ensure(context.Background())
	require.NoError(t, err)
	require.Equal(t, ids[0], again.Id)
	var accounts, keys int64
	require.NoError(t, w.conn.Model(&models.Account{}).Where("local_actor_uri = ?", e2eBase+"/actor").Count(&accounts).Error)
	require.NoError(t, w.conn.Model(&models.AccountKey{}).Where("account_id = ?", ids[0]).Count(&keys).Error)
	require.EqualValues(t, 1, accounts)
	require.EqualValues(t, 1, keys)
}

// A system actor imported by a migration (local_actor_uri <base>/actor) is
// reused with its preserved key rather than replaced.
func TestEnsureInstanceActorReusesImportedActor(t *testing.T) {
	w := newRelayWorld(t)
	imported := &models.Account{Username: "c.hime.test", LocalActorURI: e2eBase + "/actor", Bot: true}
	require.NoError(t, w.conn.Create(imported).Error)
	key, err := newAccountKey(imported.Id)
	require.NoError(t, err)
	require.NoError(t, w.conn.Create(key).Error)
	got, gotKey, err := EnsureInstanceActor(context.Background())
	require.NoError(t, err)
	require.Equal(t, imported.Id, got.Id)
	require.Equal(t, key.PublicKey, gotKey.PublicKey)
}

func TestRelayDeliveriesOnlyForAcceptedRelaysAndPublicPosts(t *testing.T) {
	w := newRelayWorld(t)
	relay := w.subscribe(t)
	require.Equal(t, models.RelayStateAccepted, relay.State)
	author := &models.Account{Username: "booster"}
	require.NoError(t, w.conn.Create(author).Error)
	// A remote follower whose inbox is the relay's: deduplicated.
	domain := "relay.test"
	follower := &models.Account{Username: "relayfollower", Domain: &domain, OriginId: e2eRelay + "/actor"}
	require.NoError(t, w.conn.Create(follower).Error)
	require.NoError(t, w.conn.Create(&models.Follow{AccountId: follower.Id, FollowId: author.Id, Status: models.FollowStatusFollowed}).Error)

	original := &models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "original"}}
	require.NoError(t, w.conn.Create(original).Error)
	(&OutboundWorker{}).processPending()
	var deliveries []Delivery
	require.NoError(t, w.conn.Where("target_inbox = ? AND CAST(payload AS TEXT) LIKE ?", e2eRelay+"/inbox", "%\"Create\"%").Find(&deliveries).Error)
	require.Len(t, deliveries, 1, "follower inbox and relay inbox are the same: one delivery")

	// A public local boost is announced; unboosting sends Undo.
	boost := &models.Status{AccountId: author.Id, ReblogId: &original.Id, Visibility: models.StatusVisibilityPublic}
	require.NoError(t, w.conn.Create(boost).Error)
	require.NoError(t, w.conn.Delete(boost).Error)
	// An unlisted boost goes to followers but never to relays.
	unlisted := &models.Status{AccountId: author.Id, ReblogId: &original.Id, Visibility: models.StatusVisibilityUnlisted}
	require.NoError(t, w.conn.Create(unlisted).Error)
	(&OutboundWorker{}).processPending()
	var events []models.FederationOutbox
	require.NoError(t, w.conn.Order("id").Find(&events).Error)
	kinds := []string{}
	for _, e := range events {
		kinds = append(kinds, e.Kind)
		require.Equal(t, "processed", e.State, e.LastError)
	}
	require.Equal(t, []string{"Create", "Announce", "AnnounceUndo", "Announce"}, kinds)
	var announces []Delivery
	require.NoError(t, w.conn.Where("CAST(payload AS TEXT) LIKE ?", "%Announce%").Order("id").Find(&announces).Error)
	require.Len(t, announces, 3)
	var first, undo, third map[string]any
	require.NoError(t, json.Unmarshal([]byte(announces[0].Payload), &first))
	require.NoError(t, json.Unmarshal([]byte(announces[1].Payload), &undo))
	require.NoError(t, json.Unmarshal([]byte(announces[2].Payload), &third))
	require.Equal(t, "Announce", first["type"])
	require.Equal(t, e2eBase+"/users/booster/statuses/"+itoa(int(original.Id)), first["object"])
	require.Equal(t, "Undo", undo["type"])
	require.Equal(t, first["id"], undo["object"].(map[string]any)["id"])
	require.Equal(t, []any{e2eBase + "/users/booster/followers"}, third["to"])
	var auth DeliveryAuthorization
	require.NoError(t, json.Unmarshal([]byte(announces[0].Authorization), &auth))
	require.Zero(t, auth.RelayID, "follower delivery wins the shared inbox")

	// Disable the relay: queued relay deliveries are revoked at send time.
	require.NoError(t, w.conn.Delete(&models.Follow{}, "account_id = ?", follower.Id).Error)
	require.NoError(t, w.conn.Create(&models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "late"}}).Error)
	(&OutboundWorker{}).processPending()
	var late Delivery
	require.NoError(t, w.conn.Where("CAST(payload AS TEXT) LIKE ?", "%late%").First(&late).Error)
	require.NoError(t, json.Unmarshal([]byte(late.Authorization), &auth))
	require.Equal(t, relay.ID, auth.RelayID)
	require.NoError(t, w.conn.Model(&models.Relay{}).Where("id = ?", relay.ID).Update("state", models.RelayStateIdle).Error)
	var activity map[string]any
	require.NoError(t, json.Unmarshal([]byte(late.Payload), &activity))
	_, allowed, err := authorizeQueuedDelivery(&late, activity)
	require.NoError(t, err)
	require.False(t, allowed)
}
