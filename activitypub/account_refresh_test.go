package activitypub

import (
	"context"
	"testing"

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
