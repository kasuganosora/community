package activitypub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestQuoteObjectURIReadsEveryDialect(t *testing.T) {
	for name, obj := range map[string]map[string]any{
		"quote":        {"quote": "https://a.example/n/1"},
		"quoteUri":     {"quoteUri": "https://a.example/n/1"},
		"quoteUrl":     {"quoteUrl": "https://a.example/n/1"},
		"misskey":      {"_misskey_quote": "https://a.example/n/1"},
		"nested":       {"quote": map[string]any{"id": "https://a.example/n/1"}},
		"fep-e232 tag": {"tag": []any{map[string]any{"type": "Mention", "href": "https://x.example/u"}, map[string]any{"type": "Link", "rel": "https://misskey-hub.net/ns#_misskey_quote", "href": "https://a.example/n/1"}}},
		"fedibird tag": {"tag": map[string]any{"type": "Link", "rel": "http://fedibird.com/ns#quoteUri", "href": "https://a.example/n/1"}},
	} {
		require.Equal(t, "https://a.example/n/1", quoteObjectURI(obj), name)
	}
	require.Empty(t, quoteObjectURI(map[string]any{"tag": []any{map[string]any{"type": "Link", "rel": "alternate", "href": "https://a.example/x"}}}))
}

func TestReLinkHref(t *testing.T) {
	require.Equal(t, "https://a.example/@u/1?x=1&y=2", reLinkHref(`<p>hi</p><p>RE: <a href="https://a.example/@u/1?x=1&amp;y=2" rel="nofollow">https://a.example/@u/1</a></p>`))
	require.Empty(t, reLinkHref("<p>no quote</p>"))
}

func quoteFixture(t *testing.T, notes map[string]map[string]any) (*gorm.DB, *httptest.Server) {
	conn := persistenceDB(t, false)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := server.URL
		w.Header().Set("Content-Type", "application/activity+json")
		if r.URL.Path == "/users/alice" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": base + "/users/alice", "type": "Person", "preferredUsername": "alice", "inbox": base + "/inbox", "name": "Alice"})
			return
		}
		if n, ok := notes[r.URL.Path]; ok {
			n["id"] = base + r.URL.Path
			if _, ok := n["attributedTo"]; !ok {
				n["attributedTo"] = base + "/users/alice"
			}
			_ = json.NewEncoder(w).Encode(n)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	resolver := NewResolver(nil, "https://example.com/actor")
	resolver.client = &Client{httpClient: ssrfHTTPClientForTesting()}
	resolver.noQuoteFetch = true
	previous := quoteFetcher
	quoteFetcher = func() *Resolver { return resolver }
	t.Cleanup(func() { quoteFetcher = previous })
	return conn, server
}

func quotingStatus(t *testing.T, conn *gorm.DB) models.Status {
	account := models.Account{Username: "bob", Domain: ptr("other.example"), OriginId: "https://other.example/users/bob"}
	require.NoError(t, conn.Create(&account).Error)
	now := time.Now()
	status := models.Status{AccountId: account.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "<p>look</p>"}, CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Create(&status).Error)
	return status
}

func ptr[T any](v T) *T { return &v }

func TestFetchAndLinkQuoteImportsPublicPost(t *testing.T) {
	conn, server := quoteFixture(t, map[string]map[string]any{
		"/notes/1": {"type": "Note", "content": "<p>old original</p>", "published": "2026-09-20T10:00:00Z", "to": []any{"https://www.w3.org/ns/activitystreams#Public"}},
	})
	status := quotingStatus(t, conn)

	require.NoError(t, fetchAndLinkQuote(context.Background(), status.Id, server.URL+"/notes/1"))

	var got models.Status
	require.NoError(t, conn.First(&got, status.Id).Error)
	require.NotNil(t, got.QuoteId)
	var quoted models.Status
	require.NoError(t, conn.Preload("Content").First(&quoted, *got.QuoteId).Error)
	require.Equal(t, models.StatusVisibilityPublic, int(quoted.Visibility))
	require.Contains(t, quoted.Content.Content, "old original")
	require.Equal(t, server.URL+"/notes/1", *quoted.OriginUri)
	require.True(t, quoted.IsBackfilled(), "an old quoted post must not surface in timelines")

	// A second call reuses the stored post.
	require.NoError(t, fetchAndLinkQuote(context.Background(), status.Id, server.URL+"/notes/1"))
	var count int64
	require.NoError(t, conn.Model(&models.Status{}).Where("origin_uri = ?", server.URL+"/notes/1").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestFetchAndLinkQuoteRefusesNonPublicAndForeignPosts(t *testing.T) {
	conn, server := quoteFixture(t, map[string]map[string]any{
		"/notes/private": {"type": "Note", "content": "<p>x</p>", "published": "2026-09-20T10:00:00Z", "to": []any{"https://a.example/users/alice/followers"}},
		"/notes/foreign": {"type": "Note", "content": "<p>x</p>", "published": "2026-09-20T10:00:00Z", "to": []any{"https://www.w3.org/ns/activitystreams#Public"}, "attributedTo": "https://elsewhere.example/users/mallory"},
	})
	status := quotingStatus(t, conn)

	for _, path := range []string{"/notes/private", "/notes/foreign", "/notes/missing"} {
		require.Error(t, fetchAndLinkQuote(context.Background(), status.Id, server.URL+path), path)
	}
	var got models.Status
	require.NoError(t, conn.First(&got, status.Id).Error)
	require.Nil(t, got.QuoteId)
	var count int64
	require.NoError(t, conn.Model(&models.Status{}).Where("origin_uri LIKE ?", "%/notes/%").Count(&count).Error)
	require.Zero(t, count, "rejected posts are never stored")

	require.Error(t, fetchAndLinkQuote(context.Background(), status.Id, "file:///etc/passwd"))
}

func TestScheduleQuoteFetchSkipsNestedAndKnownQuotes(t *testing.T) {
	var ran int
	previous := startQuoteFetch
	startQuoteFetch = func(func()) { ran++ }
	t.Cleanup(func() { startQuoteFetch = previous })
	obj := map[string]any{"quote": "https://a.example/n/1"}

	scheduleQuoteFetch(nil, &models.Status{Id: 1}, obj)
	require.Equal(t, 1, ran)
	scheduleQuoteFetch(&Resolver{noQuoteFetch: true}, &models.Status{Id: 1}, obj)
	known := uint64(5)
	scheduleQuoteFetch(nil, &models.Status{Id: 1, QuoteId: &known}, obj)
	scheduleQuoteFetch(nil, &models.Status{Id: 1}, map[string]any{})
	require.Equal(t, 1, ran)
}
