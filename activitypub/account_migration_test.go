package activitypub

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type migrationFetcher struct{ actor *Actor }

func (f migrationFetcher) FetchActor(context.Context, string) (*Actor, error) { return f.actor, nil }
func (f migrationFetcher) FetchObject(context.Context, string) (map[string]any, error) {
	return nil, nil
}

func TestMigrationRequiresFreshAliasAndPreservesAllAliases(t *testing.T) {
	conn := outboundDB(t)
	old := outboundAccount(t, conn, "old", false)
	target := outboundAccount(t, conn, "new", true)
	uri := old.ActorURI(systemsettings.GetBaseURL())
	var actor Actor
	require.NoError(t, json.Unmarshal([]byte(`{"id":"`+target.OriginId+`","type":"Person","alsoKnownAs":["https://another.example/users/a","`+uri+`"]}`), &actor))
	require.Len(t, actor.Aliases, 2)
	require.NoError(t, VerifyMigrationTarget(context.Background(), conn, old, target, migrationFetcher{&actor}))
	encoded, err := json.Marshal(actor)
	require.NoError(t, err)
	require.Contains(t, string(encoded), uri)
	actor.Aliases = []string{"https://another.example/users/a"}
	actor.AlsoKnownAs = actor.Aliases[0]
	require.ErrorIs(t, VerifyMigrationTarget(context.Background(), conn, old, target, migrationFetcher{&actor}), ErrMigrationAlias)
	actor.Aliases = []string{uri}
	actor.ID = "https://wrong.example/users/a"
	require.ErrorIs(t, VerifyMigrationTarget(context.Background(), conn, old, target, migrationFetcher{&actor}), ErrMigrationState)
}

func TestAccountMoveTransfersAcceptedFollowersAndQueuesDurableMove(t *testing.T) {
	conn := outboundDB(t)
	old := outboundAccount(t, conn, "old", false)
	target := outboundAccount(t, conn, "new", false)
	local := outboundAccount(t, conn, "reader", false)
	remote := outboundAccount(t, conn, "remote", true)
	pending := outboundAccount(t, conn, "pending", false)
	aliases, _ := json.Marshal([]string{old.ActorURI(systemsettings.GetBaseURL())})
	require.NoError(t, conn.Model(target).UpdateColumn("also_known_as", string(aliases)).Error)
	for _, f := range []models.Follow{{Id: 51, AccountId: local.Id, FollowId: old.Id}, {Id: 52, AccountId: remote.Id, FollowId: old.Id}, {Id: 53, AccountId: pending.Id, FollowId: old.Id, Status: models.FollowStatusRequested}} {
		require.NoError(t, conn.Create(&f).Error)
	}
	require.NoError(t, ApplyAccountMove(conn, old.Id, target.Id, true))
	var f models.Follow
	require.NoError(t, conn.Where("account_id = ? AND follow_id = ?", local.Id, target.Id).First(&f).Error)
	require.Equal(t, models.FollowStatusFollowed, f.Status)
	var count int64
	require.NoError(t, conn.Model(&models.Follow{}).Where("account_id = ? AND follow_id = ?", pending.Id, target.Id).Count(&count).Error)
	require.Zero(t, count)
	var saved models.Account
	require.NoError(t, conn.First(&saved, old.Id).Error)
	require.Equal(t, target.ActorURI(systemsettings.GetBaseURL()), saved.MovedTo)
	events := outboxEvents(t, conn)
	require.Len(t, events, 1)
	require.Equal(t, "Move", events[0].Kind)
	require.Equal(t, []uint64{remote.Id}, events[0].RecipientIDs)
	require.NoError(t, ApplyAccountMove(conn, old.Id, target.Id, true))
	require.Len(t, outboxEvents(t, conn), 1)
	var extra models.AccountExtra
	require.NoError(t, conn.First(&extra, "account_id = ?", target.Id).Error)
	require.EqualValues(t, 1, extra.FollowersCount)
}

func TestAccountMoveRollbackAndLocalAliasRecheck(t *testing.T) {
	conn := outboundDB(t)
	old := outboundAccount(t, conn, "old", false)
	target := outboundAccount(t, conn, "new", false)
	require.ErrorIs(t, ApplyAccountMove(conn, old.Id, target.Id, true), ErrMigrationAlias)
	require.Empty(t, outboxEvents(t, conn))
	var saved models.Account
	require.NoError(t, conn.First(&saved, old.Id).Error)
	require.Empty(t, saved.MovedTo)
}

func TestAccountDeleteStillExpandsAfterErasureAndMoveAuthorization(t *testing.T) {
	conn := outboundDB(t)
	author := outboundAccount(t, conn, "erased", false)
	now := time.Now()
	require.NoError(t, conn.Model(author).Updates(map[string]any{"is_ban": true, "deletion_requested_at": now}).Error)
	worker := &OutboundWorker{}
	_, err := worker.expand(context.Background(), &models.FederationOutbox{ID: 1, AccountID: author.Id, Kind: "AccountDelete", Payload: "{}"})
	require.NoError(t, err)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(author).Updates(map[string]any{"is_ban": false, "moved_to": "https://new.example/users/a"}).Error)
	auth, _ := json.Marshal(DeliveryAuthorization{AccountID: author.Id, Kind: "Move"})
	_, allowed, err := authorizeQueuedDelivery(&Delivery{Authorization: string(auth)}, map[string]any{"target": "https://other.example/users/a"})
	require.NoError(t, err)
	require.False(t, allowed)
	_, allowed, err = authorizeQueuedDelivery(&Delivery{Authorization: string(auth)}, map[string]any{"target": "https://new.example/users/a"})
	require.NoError(t, err)
	require.True(t, allowed)
}
