package activitypub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverActorInvalidAcctFormat(t *testing.T) {
	// Too many @ signs
	_, err := DiscoverActor("user@a@b")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid acct format")

	// No @ at all
	_, err = DiscoverActor("justusername")
	require.Error(t, err)
}

func TestDiscoverActorSSRFBlocked(t *testing.T) {
	// A localhost domain should be blocked by SSRF protection
	_, err := DiscoverActor("user@127.0.0.1")
	require.Error(t, err)
}

func TestResolverResolveAccountInvalidIdentifier(t *testing.T) {
	resolver := NewResolver(nil, "https://example.com/actor")

	// Invalid identifier (no @, no http)
	_, err := resolver.ResolveAccount(context.Background(), "invalid-no-at-no-http")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid account identifier")
}

func TestResolverResolveStatusDBMiss(t *testing.T) {
	// Set up a DB so db.GetDB() is not nil during the resolve
	setupStatusReachDB(t)

	resolver := NewResolver(nil, "https://example.com/actor")

	// Mock server returning a Note object
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":         "Note",
			"id":           "https://mock.example.com/statuses/1",
			"content":      "<p>Hello</p>",
			"attributedTo": "https://mock.example.com/users/alice",
		})
	}))
	defer server.Close()

	// This will fail because createStatusFromObject tries to resolve the author
	// account and write to DB. Since the author doesn't exist, it will attempt
	// to create it from the actor (which won't be found), producing an error.
	_, err := resolver.ResolveStatus(context.Background(), server.URL+"/statuses/1")
	// It should error because the author account can't be resolved
	require.Error(t, err)
}

func TestResolverFetchRemoteFollowersNoOriginId(t *testing.T) {
	resolver := NewResolver(nil, "https://example.com/actor")

	// Account with empty OriginId
	account := &models.Account{OriginId: ""}
	_, err := resolver.FetchRemoteFollowers(context.Background(), account, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a remote account")
}

func TestResolverFetchRemoteFollowersWithMockServer(t *testing.T) {
	resolver := newTestResolver()

	// Mock server returning actor with followers URL
	followersServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":         "OrderedCollection",
			"totalItems":   2,
			"orderedItems": []string{"https://f1.example.com/users/a", "https://f2.example.com/users/b"},
		})
	}))
	defer followersServer.Close()

	actorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":      "Person",
			"id":        "https://mock.example.com/users/alice",
			"followers": followersServer.URL,
		})
	}))
	defer actorServer.Close()

	account := &models.Account{OriginId: actorServer.URL}
	followers, err := resolver.FetchRemoteFollowers(context.Background(), account, 10)
	require.NoError(t, err)
	assert.Len(t, followers, 2)
}
func newTestResolver() *Resolver {
	r := &Resolver{
		client:   &Client{httpClient: ssrfHTTPClientForTesting(), privateKey: nil, actorID: ""},
		cacheTTL: defaultCacheTTL,
	}
	return r
}
