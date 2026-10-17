package activitypub

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

type staticActorFetcher struct {
	actor *Actor
	err   error
}

func (f staticActorFetcher) FetchActor(context.Context, string) (*Actor, error) {
	return f.actor, f.err
}
func (f staticActorFetcher) FetchObject(context.Context, string) (map[string]any, error) {
	return nil, &FetchError{StatusCode: 404}
}

func TestRefreshRemoteAccountRejectsBeforeWriting(t *testing.T) {
	domain := "remote.example"
	account := &models.Account{Id: 1, Username: "alice", Domain: &domain, OriginId: "https://remote.example/users/alice"}
	ctx := context.Background()

	// No database is touched on any of these paths (conn is nil).
	assert.ErrorIs(t, RefreshRemoteAccount(ctx, nil, staticActorFetcher{}, &models.Account{Username: "local"}, false), ErrNotRemoteAccount)
	assert.ErrorIs(t, RefreshRemoteAccount(ctx, nil, staticActorFetcher{err: &FetchError{StatusCode: 410}}, account, false), ErrActorGone)
	assert.ErrorIs(t, RefreshRemoteAccount(ctx, nil, staticActorFetcher{err: &FetchError{StatusCode: 404}}, account, false), ErrActorGone)
	assert.ErrorIs(t, RefreshRemoteAccount(ctx, nil, staticActorFetcher{actor: &Actor{ID: account.OriginId, Type: "Tombstone"}}, account, false), ErrActorGone)

	err := RefreshRemoteAccount(ctx, nil, staticActorFetcher{err: &FetchError{StatusCode: 503}}, account, false)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrActorGone, "a transient failure is not gone")

	err = RefreshRemoteAccount(ctx, nil, staticActorFetcher{actor: &Actor{ID: "https://evil.example/users/alice", Type: "Person"}}, account, false)
	assert.ErrorContains(t, err, "does not match", "a stored identity is never re-pointed")
	err = RefreshRemoteAccount(ctx, nil, staticActorFetcher{actor: &Actor{ID: account.OriginId, Type: "Note"}}, account, false)
	assert.ErrorContains(t, err, "not an actor")
	assert.NoError(t, RefreshRemoteAccount(ctx, nil, staticActorFetcher{actor: &Actor{ID: account.OriginId, Type: "Person"}}, account, true),
		"dry run validates only")
}

func TestRefreshRemoteAccountSkipsUnchangedRowWrites(t *testing.T) {
	conn := emojiDB(t)
	actor := decodeDiscoveryActor(t, `,"discoverable":true,"indexable":true,"hideCollections":false`)
	actor.Name = "Alice"
	account, err := createRemoteAccount(conn, actor)
	require.NoError(t, err)
	ctx := context.Background()
	fetch := staticActorFetcher{actor: actor}
	require.NoError(t, RefreshRemoteAccount(ctx, conn, fetch, &account, false))

	var writes []string
	require.NoError(t, conn.Callback().Update().After("gorm:update").Register("test:count_account_writes", func(db *gorm.DB) {
		if db.Error == nil && db.RowsAffected > 0 && strings.HasPrefix(db.Statement.Table, "accounts") {
			writes = append(writes, db.Statement.Table)
		}
	}))
	t.Cleanup(func() { _ = conn.Callback().Update().Remove("test:count_account_writes") })

	require.NoError(t, RefreshRemoteAccount(ctx, conn, fetch, &account, false))
	assert.Empty(t, writes, "an unchanged profile rewrites no account rows")

	// Stale stamps are renewed even when nothing else changed.
	old := time.Now().Add(-7 * time.Hour)
	require.NoError(t, conn.Exec("UPDATE accounts SET last_activity_at = ? WHERE id = ?", old, account.Id).Error)
	require.NoError(t, RefreshRemoteAccount(ctx, conn, fetch, &account, false))
	assert.Equal(t, []string{"accounts"}, writes)
	require.NotNil(t, account.LastActivityAt)
	assert.True(t, account.LastActivityAt.After(old.Add(time.Hour)))

	writes = nil
	actor.Name = "Alice 2"
	require.NoError(t, RefreshRemoteAccount(ctx, conn, fetch, &account, false))
	assert.Equal(t, []string{"accounts"}, writes)
	assert.Equal(t, "Alice 2", account.DisplayName)
}
