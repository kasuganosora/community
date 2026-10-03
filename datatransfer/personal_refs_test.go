package datatransfer

import (
	"context"
	"errors"
	"testing"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPersonalReferencesDiscoveryPreservesOpaqueIdentityAndNoWrites(t *testing.T) {
	conn := importerDB(t)
	b := &Bundle{Scope: "personal", OwnerID: "owner", Accounts: []Account{{ID: "owner"}, {ID: "remote", Username: "alice", Domain: "remote.example"}}, Relations: []Relation{{Kind: "follow", AccountID: "owner", TargetID: "remote"}}}
	calls := 0
	require.NoError(t, resolvePersonalReferences(context.Background(), conn, b, func(ctx context.Context, a Account) (*activitypub.Actor, error) {
		calls++
		require.Equal(t, "alice", a.Username)
		return &activitypub.Actor{ID: "https://remote.example/users/opaque123", PreferredUsername: "alice", Inbox: "https://remote.example/inbox", Name: "Alice"}, nil
	}))
	require.Equal(t, 1, calls)
	require.Equal(t, "remote", b.Accounts[1].ID)
	require.Equal(t, "https://remote.example/users/opaque123", b.Accounts[1].ActorURI)
	require.Equal(t, "remote", b.Relations[0].TargetID)
	var count int64
	require.NoError(t, conn.Model(&models.Account{}).Count(&count).Error)
	require.Zero(t, count)
}
func TestPersonalReferencesCacheAndUnresolvedRecipientFailClosed(t *testing.T) {
	conn := importerDB(t)
	domain := "remote.example"
	cached := models.Account{Username: "alice", Acct: "alice@remote.example", Domain: &domain, OriginId: "https://remote.example/users/opaque"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&cached).Error)
	b := &Bundle{Scope: "personal", OwnerID: "owner", Accounts: []Account{{ID: "owner"}, {ID: "known", Username: "alice", Domain: domain}, {ID: "missing", ActorURI: "https://missing.example/user", Settings: map[string]any{"personal_reference_verified": true}}}, Posts: []Post{{ID: "dm", AuthorID: "owner", Visibility: "direct", RecipientIDs: []string{"known", "missing"}}}}
	calls := 0
	require.NoError(t, resolvePersonalReferences(context.Background(), conn, b, func(context.Context, Account) (*activitypub.Actor, error) {
		calls++
		return nil, errors.New("unreachable")
	}))
	require.Equal(t, 1, calls)
	require.Equal(t, cached.OriginId, b.Accounts[1].ActorURI)
	require.Empty(t, b.Accounts[2].ActorURI)
	require.Nil(t, b.Accounts[2].Settings["personal_reference_verified"])
	require.True(t, b.Posts[0].ArchiveOnly)
	require.Len(t, b.Issues, 1)
}
func TestPersonalReferenceRejectsSSRFAndCancellation(t *testing.T) {
	conn := importerDB(t)
	b := &Bundle{Scope: "personal", OwnerID: "owner", Accounts: []Account{{ID: "bad", ActorURI: "http://127.0.0.1:9/actor"}}}
	require.NoError(t, ResolvePersonalReferences(context.Background(), conn, b))
	require.Empty(t, b.Accounts[0].ActorURI)
	require.Len(t, b.Issues, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, ResolvePersonalReferences(ctx, conn, b), context.Canceled)
}
