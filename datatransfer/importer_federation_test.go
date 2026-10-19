package datatransfer

import (
	"context"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

// Misskey instance.actor / relay.actor keep <base>/users/<id> and their keys;
// the import designates them and restores relay subscriptions with the
// Follow id Misskey sent, so relays keep recognising the subscription.
func TestImportSystemActorsAndRelays(t *testing.T) {
	b := streamFixture(t, 3, 4)
	base := "https://site.example"
	for i, role := range []string{"instance_actor", "relay_actor"} {
		id := []string{"ia", "ra"}[i]
		name := []string{"instance.actor", "relay.actor"}[i]
		b.Accounts = append(b.Accounts, Account{ID: id, Username: name, Local: true, System: true, SystemRole: role, ActorURI: base + "/users/" + id})
		k := b.Keys[0]
		k.AccountID, k.KeyID = id, base+"/users/"+id+"#main-key"
		b.Keys = append(b.Keys, k)
	}
	b.Relays = []SourceRelay{
		{ID: "r1", Inbox: "https://relay.example/inbox", State: "accepted", ActorID: "ra", ActorURI: base + "/users/ra", FollowID: base + "/activities/follow-relay/r1"},
		{ID: "r2", Inbox: "https://relay2.example/inbox", State: "requesting", ActorID: "ra", ActorURI: base + "/users/ra", FollowID: base + "/activities/follow-relay/r2"},
	}
	o := Options{Scope: "site", TargetBaseURL: base, StorageDir: t.TempDir()}
	conn := importerDB(t)
	require.NoError(t, conn.AutoMigrate(&models.SystemSetting{}, &models.Relay{}))
	sp := spoolBundle(t, b)
	preview, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: 2})
	require.NoError(t, err)
	require.True(t, preview.CanExecute, "%+v", preview.Issues)
	for range 2 { // idempotent rerun
		report, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: 2, Apply: true})
		require.NoError(t, err, "%+v", report)
	}
	var settings []models.SystemSetting
	require.NoError(t, conn.Order("key").Find(&settings).Error)
	got := map[string]any{}
	for _, s := range settings {
		got[s.Key] = s.Val
	}
	require.Equal(t, map[string]any{models.SettingInstanceActorURI: base + "/users/ia", models.SettingRelayActorURI: base + "/users/ra"}, got)
	var ia models.Account
	require.NoError(t, conn.Where("local_actor_uri = ?", base+"/users/ia").First(&ia).Error)
	var key models.AccountKey
	require.NoError(t, conn.Where("account_id = ?", ia.Id).First(&key).Error)
	require.Equal(t, base+"/users/ia#main-key", key.SigningKeyID(ia.LocalActorURI), "keyId unchanged")
	var relays []models.Relay
	require.NoError(t, conn.Order("inbox_url").Find(&relays).Error)
	require.Len(t, relays, 2)
	require.Equal(t, models.RelayStateAccepted, relays[0].State)
	require.Equal(t, base+"/activities/follow-relay/r1", relays[0].FollowActivityID)
	require.Equal(t, base+"/users/ra", relays[0].ActorURI)
	require.Equal(t, models.RelayStatePending, relays[1].State)
}
