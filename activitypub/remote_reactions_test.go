package activitypub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/stretchr/testify/require"
)

func remoteOrigin(t *testing.T, software string, mux func(w http.ResponseWriter, r *http.Request) bool) (*int32, func()) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		switch r.URL.Path {
		case "/.well-known/nodeinfo":
			_ = json.NewEncoder(w).Encode(map[string]any{"links": []any{map[string]any{"rel": "http://nodeinfo.diaspora.software/ns/schema/2.1", "href": "http://" + r.Host + "/nodeinfo/2.1"}}})
			return
		case "/nodeinfo/2.1":
			_ = json.NewEncoder(w).Encode(map[string]any{"software": map[string]any{"name": software}})
			return
		}
		if !mux(w, r) {
			http.NotFound(w, r)
		}
	}))
	oldClient, oldBase := remoteReactionClient, remoteReactionBase
	remoteReactionClient = ssrfHTTPClientForTesting()
	remoteReactionBase = func(string) string { return srv.URL }
	resetRemoteReactionCaches()
	return &calls, func() {
		srv.Close()
		remoteReactionClient, remoteReactionBase = oldClient, oldBase
		resetRemoteReactionCaches()
	}
}

func strp(s string) *string { return &s }

func byName(rs []*serializer.Reaction) map[string]*serializer.Reaction {
	m := map[string]*serializer.Reaction{}
	for _, r := range rs {
		m[r.Name] = r
	}
	return m
}

func TestRemoteReactionsMisskey(t *testing.T) {
	calls, done := remoteOrigin(t, "sharkey", func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/notes/show" || r.Method != http.MethodPost {
			return false
		}
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "asd5o9mww71800pw", body["noteId"])
		_, _ = w.Write([]byte(`{"id":"asd5o9mww71800pw","reactions":{"🆒":2,"👍":1,":anenw18@.:":1,":blob@other.example:":3},"reactionEmojis":{"blob@other.example":"https://other.example/blob.png"}}`))
		return true
	})
	defer done()
	st := &models.Status{OriginUri: strp("https://maid.lat/notes/asd5o9mww71800pw")}
	local := []*serializer.Reaction{{Name: "👍", Count: 1, Me: true}}
	merged := byName(WithRemoteReactions(context.Background(), st, local))
	require.Len(t, merged, 4)
	require.EqualValues(t, 2, merged["🆒"].Count)
	require.EqualValues(t, 1, merged["👍"].Count) // local reactor is already in the remote count
	require.True(t, merged["👍"].Me)
	require.EqualValues(t, 1, merged[":anenw18@maid.lat:"].Count)
	require.Contains(t, merged[":anenw18@maid.lat:"].URL, "/")
	require.NotEmpty(t, merged[":blob@other.example:"].URL)
	require.Empty(t, merged["🆒"].URL)
	// Cached: a second call makes no requests.
	n := atomic.LoadInt32(calls)
	WithRemoteReactions(context.Background(), st, nil)
	require.Equal(t, n, atomic.LoadInt32(calls))
	require.Len(t, local, 1)
	require.EqualValues(t, 1, local[0].Count)
}

func TestRemoteReactionsPleroma(t *testing.T) {
	_, done := remoteOrigin(t, "akkoma", func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v1/pleroma/statuses/AbC123/reactions" {
			return false
		}
		_, _ = w.Write([]byte(`[{"name":"😀","count":2,"me":false},{"name":"blobcat","count":1,"url":"https://pl.example/emoji/blobcat.png"}]`))
		return true
	})
	defer done()
	st := &models.Status{OriginUri: strp("https://pl.example/objects/uuid-1"), OriginUrl: strp("https://pl.example/notice/AbC123")}
	m := byName(RemoteStatusReactions(context.Background(), st))
	require.EqualValues(t, 2, m["😀"].Count)
	require.EqualValues(t, 1, m[":blobcat@pl.example:"].Count)
	require.NotEmpty(t, m[":blobcat@pl.example:"].URL)
}

func TestRemoteReactionsFedibird(t *testing.T) {
	_, done := remoteOrigin(t, "fedibird", func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v1/statuses/1100" {
			return false
		}
		_, _ = w.Write([]byte(`{"id":"1100","emoji_reactions":[{"name":"⭐","count":4},{"name":"neko","count":2,"url":"https://f.example/neko.png","domain":"x.example"}]}`))
		return true
	})
	defer done()
	st := &models.Status{OriginUri: strp("https://fedibird.com/users/a/statuses/1100")}
	m := byName(RemoteStatusReactions(context.Background(), st))
	require.EqualValues(t, 4, m["⭐"].Count)
	require.EqualValues(t, 2, m[":neko@x.example:"].Count)
}

func TestRemoteReactionsFailuresFallBack(t *testing.T) {
	// Unsupported software: no reaction calls.
	_, done := remoteOrigin(t, "mastodon", func(w http.ResponseWriter, r *http.Request) bool {
		t.Fatalf("unexpected request %s", r.URL.Path)
		return false
	})
	st := &models.Status{OriginUri: strp("https://m.example/users/a/statuses/1")}
	local := []*serializer.Reaction{{Name: "👍", Count: 1}}
	require.Equal(t, local, WithRemoteReactions(context.Background(), st, local))
	done()

	// Errors and slow servers fall back to local data.
	_, done = remoteOrigin(t, "misskey", func(w http.ResponseWriter, r *http.Request) bool {
		time.Sleep(remoteReactionTimeout + 500*time.Millisecond)
		return false
	})
	defer done()
	st = &models.Status{OriginUri: strp("https://mk.example/notes/abc")}
	start := time.Now()
	require.Equal(t, local, WithRemoteReactions(context.Background(), st, local))
	require.Less(t, time.Since(start), remoteReactionTimeout+2*time.Second)
	// Local statuses never trigger a fetch.
	require.Nil(t, RemoteStatusReactions(context.Background(), &models.Status{}))
}

func TestRemoteReactionsProductionClientBlocksPrivateHosts(t *testing.T) {
	resetRemoteReactionCaches()
	defer resetRemoteReactionCaches()
	st := &models.Status{OriginUri: strp("https://127.0.0.1/notes/abc")}
	require.Nil(t, RemoteStatusReactions(context.Background(), st))
}
