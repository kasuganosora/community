package activitypub

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
)

// importMisskeyActor creates a local system account as the importer does
// for Misskey's instance.actor / relay.actor: actor URI <base>/users/<id>,
// keyId <uri>#main-key, and the designating setting.
func importMisskeyActor(t *testing.T, w *relayWorld, id, username, setting string) (*models.Account, *models.AccountKey) {
	t.Helper()
	uri := e2eBase + "/users/" + id
	account := &models.Account{Username: username, LocalActorURI: uri, Bot: true, Locked: true}
	require.NoError(t, w.conn.Create(account).Error)
	key, err := newAccountKey(account.Id)
	require.NoError(t, err)
	key.KeyID = uri + "#main-key"
	require.NoError(t, w.conn.Create(key).Error)
	systemsettings.Set(setting, uri)
	t.Cleanup(func() { systemsettings.Set(setting, "") })
	return account, key
}

func TestImportedMisskeyInstanceAndRelayActorsKeepIdentity(t *testing.T) {
	w := newRelayWorld(t)
	resetInstanceActorCache()
	t.Cleanup(resetInstanceActorCache)
	ia, iaKey := importMisskeyActor(t, w, "9ia0000000", "instance.actor", models.SettingInstanceActorURI)
	ra, raKey := importMisskeyActor(t, w, "9ra0000000", "relay.actor", models.SettingRelayActorURI)

	require.Equal(t, ia.LocalActorURI, InstanceActorURI())
	require.Equal(t, ra.LocalActorURI, RelayActorURI())
	got, key, err := EnsureInstanceActor(context.Background())
	require.NoError(t, err)
	require.Equal(t, ia.Id, got.Id, "the imported actor is reused, no new identity is minted")
	require.Equal(t, iaKey.PublicKey, key.PublicKey, "and its key is not regenerated")
	var accounts int64
	require.NoError(t, w.conn.Model(&models.Account{}).Where("local_actor_uri = ?", e2eBase+"/actor").Count(&accounts).Error)
	require.Zero(t, accounts)

	// Both the old Misskey URI and /actor resolve to the same document.
	for _, path := range []string{"/users/9ia0000000", "/actor"} {
		rec := httptest.NewRecorder()
		w.engine.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		require.Equal(t, 200, rec.Code, path)
		var actor map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &actor))
		require.Equal(t, ia.LocalActorURI, actor["id"], path)
		pk := actor["publicKey"].(map[string]any)
		require.Equal(t, ia.LocalActorURI+"#main-key", pk["id"], path)
		require.Equal(t, iaKey.PublicKey, pk["publicKeyPem"], path)
	}
	// WebFinger keeps resolving the Misskey accts and the old actor URI.
	for resource, want := range map[string]string{"acct:instance.actor@c.hime.test": ia.LocalActorURI, ia.LocalActorURI: ia.LocalActorURI, "acct:relay.actor@c.hime.test": ra.LocalActorURI} {
		rec := httptest.NewRecorder()
		w.engine.ServeHTTP(rec, httptest.NewRequest("GET", "/.well-known/webfinger?resource="+resource, nil))
		require.Equal(t, 200, rec.Code, resource+": "+rec.Body.String())
		var jrd WebFingerResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &jrd))
		found := false
		for _, l := range jrd.Links {
			found = found || l.Href == want
		}
		require.True(t, found, "%s -> %+v", resource, jrd)
	}
	// Signed fetches use the old keyId.
	client, err := instanceActorSigningClient(context.Background())
	require.NoError(t, err)
	require.Equal(t, ia.LocalActorURI+"#main-key", client.signingKeyID())

	// Relays are followed, signed and accepted as relay.actor.
	relay := w.subscribe(t)
	require.Equal(t, models.RelayStateAccepted, relay.State)
	require.Equal(t, ra.LocalActorURI, relay.ActorURI)
	var d Delivery
	require.NoError(t, w.conn.Where("target_inbox = ?", e2eRelay+"/inbox").First(&d).Error)
	var follow map[string]any
	require.NoError(t, json.Unmarshal([]byte(d.Payload), &follow))
	require.Equal(t, ra.LocalActorURI, follow["actor"])
	signer, err := (&DeliveryQueue{}).signingClient(follow)
	require.NoError(t, err)
	require.Equal(t, ra.LocalActorURI+"#main-key", signer.signingKeyID())
	_ = raKey

	// A subscription migrated from Misskey (Follow id follow-relay/<id>,
	// actor relay.actor) is undone with that same actor and id.
	migrated := models.Relay{InboxURL: "https://relay.test/inbox2", FollowActivityID: e2eBase + "/activities/follow-relay/9rl", State: models.RelayStateAccepted, ActorURI: ra.LocalActorURI}
	require.NoError(t, w.conn.Create(&migrated).Error)
	require.NoError(t, NewRelayService().DisableRelay(migrated.ID))
	var undo Delivery
	require.NoError(t, w.conn.Where("target_inbox = ?", migrated.InboxURL).First(&undo).Error)
	var activity map[string]any
	require.NoError(t, json.Unmarshal([]byte(undo.Payload), &activity))
	require.Equal(t, ra.LocalActorURI, activity["actor"])
	require.Equal(t, e2eBase+"/activities/follow-relay/9rl", activity["object"].(map[string]any)["id"])
}
