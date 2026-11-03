package activitypub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestInboundCreateKeepsReplyParentURI(t *testing.T) {
	conn := persistenceDB(t, false)
	// Warm settings outside the inbox transaction. SQLite allows one
	// connection, and a lookup from inside that transaction would wait forever.
	_ = systemsettings.GetBaseURL()
	domain := "remote.example"
	author := models.Account{Username: "sky", Domain: &domain, OriginId: "https://remote.example/users/sky"}
	require.NoError(t, conn.Create(&author).Error)
	parent := "https://remote.example/notes/parent"
	for _, inReplyTo := range []any{
		parent,
		map[string]any{"type": "Note", "id": parent},
		map[string]any{"@id": parent},
	} {
		id := "https://remote.example/notes/reply-" + inReplyToKind(inReplyTo)
		object := map[string]any{
			"id": id, "type": "Note", "attributedTo": author.OriginId,
			"content": "<p>reply</p>", "inReplyTo": inReplyTo,
			"to": []any{"https://www.w3.org/ns/activitystreams#Public"},
		}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		ctx := context.WithValue(context.Background(), actorKey, author.OriginId)
		processInboxTransaction(ctx, c, id+"/activity", func(ctx context.Context) {
			handleCreateActivity(ctx, c, map[string]any{"object": object}, &author)
		})
		require.Equal(t, 200, w.Code, w.Body.String())
		var saved models.Status
		require.NoError(t, conn.Where("origin_uri = ?", id).First(&saved).Error)
		require.Nil(t, saved.InReplyToId)
		require.Equal(t, parent, models.ReplyToURI(&saved))
		require.False(t, models.ReplyParentChecked(&saved))
	}
}

func inReplyToKind(value any) string {
	switch value.(type) {
	case string:
		return "string"
	case map[string]any:
		if _, ok := value.(map[string]any)["@id"]; ok {
			return "at-id"
		}
		return "object"
	default:
		return "other"
	}
}

func TestLoadMissingReplyAncestorsRefetchesSourceThenSignedParent(t *testing.T) {
	conn := persistenceDB(t, false)
	const (
		actorURI  = "https://remote.example/users/alice"
		replyURI  = "https://remote.example/notes/reply"
		parentURI = "https://remote.example/notes/parent"
	)
	now := time.Now()
	holder := models.Account{Username: "holder", CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Omit("Extra").Create(&holder).Error)
	domain := "remote.example"
	reply := models.Status{
		AccountId: holder.Id, Domain: &domain, OriginUri: strPtr(replyURI),
		Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now,
		Content: models.StatusContent{Content: "<p>reply</p>"},
	}
	require.NoError(t, conn.Create(&reply).Error)

	var unsigned, signed int
	withFetchTransport(t, func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Signature") == "" {
			unsigned++
			return outboundResponse(http.StatusUnauthorized, ""), nil
		}
		signed++
		id := "https://remote.example" + r.URL.Path
		switch r.URL.Path {
		case "/users/alice":
			return outboundResponse(http.StatusOK, activityJSON(map[string]any{
				"id": id, "type": "Person", "preferredUsername": "alice", "name": "Alice", "inbox": "https://remote.example/inbox",
			})), nil
		case "/notes/parent":
			return outboundResponse(http.StatusOK, activityJSON(map[string]any{
				"id": id, "type": "Note", "attributedTo": actorURI, "content": "<p>parent</p>",
				"published": "2026-11-01T00:00:00Z", "to": []any{asPublic},
			})), nil
		case "/notes/reply":
			return outboundResponse(http.StatusOK, activityJSON(map[string]any{
				"id": id, "type": "Note", "attributedTo": actorURI, "content": "<p>reply</p>",
				"inReplyTo": parentURI, "published": "2026-11-04T03:17:00Z", "to": []any{asPublic},
			})), nil
		default:
			return outboundResponse(http.StatusNotFound, ""), nil
		}
	}, testSigner(t))

	require.NoError(t, LoadMissingReplyAncestors(context.Background(), &reply))
	require.Equal(t, 3, unsigned, "authorized fetch must be tried signed after 401")
	require.Equal(t, 3, signed)

	var saved models.Status
	require.NoError(t, conn.First(&saved, reply.Id).Error)
	require.NotNil(t, saved.InReplyToId)
	var parent models.Status
	require.NoError(t, conn.Preload("Content").First(&parent, *saved.InReplyToId).Error)
	require.Equal(t, parentURI, *parent.OriginUri)
	require.Contains(t, parent.Content.Content, "parent")
	require.Equal(t, uint64(1), parent.RepliesCount)
	require.Empty(t, models.ReplyToURI(&saved))
}

func TestLoadMissingReplyAncestorsWalksMissingChain(t *testing.T) {
	conn, server := replyContextServer(t, func(base, path string) (int, map[string]any) {
		switch path {
		case "/users/alice":
			return http.StatusOK, map[string]any{"id": base + path, "type": "Person", "preferredUsername": "alice", "inbox": base + "/inbox", "name": "Alice"}
		case "/notes/grand":
			return http.StatusOK, map[string]any{"id": base + path, "type": "Note", "attributedTo": base + "/users/alice", "content": "<p>grand</p>", "published": "2026-11-01T00:00:00Z", "to": []any{asPublic}}
		case "/notes/parent":
			return http.StatusOK, map[string]any{"id": base + path, "type": "Note", "attributedTo": base + "/users/alice", "content": "<p>parent</p>", "inReplyTo": base + "/notes/grand", "published": "2026-11-02T00:00:00Z", "to": []any{asPublic}}
		case "/notes/reply":
			return http.StatusOK, map[string]any{"id": base + path, "type": "Note", "attributedTo": base + "/users/alice", "content": "<p>reply</p>", "inReplyTo": map[string]any{"id": base + "/notes/parent"}, "published": "2026-11-03T00:00:00Z", "to": []any{asPublic}}
		default:
			return http.StatusNotFound, nil
		}
	})
	now := time.Now()
	holder := models.Account{Username: "holder", CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Omit("Extra").Create(&holder).Error)
	domain := "remote.example"
	origin := server.URL + "/notes/reply"
	reply := models.Status{
		AccountId: holder.Id, Domain: &domain, OriginUri: &origin,
		Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now,
		Content: models.StatusContent{Content: "<p>reply</p>"},
	}
	require.NoError(t, conn.Create(&reply).Error)

	require.NoError(t, LoadMissingReplyAncestors(context.Background(), &reply))
	var saved models.Status
	require.NoError(t, conn.First(&saved, reply.Id).Error)
	require.NotNil(t, saved.InReplyToId)
	var parent, grand models.Status
	require.NoError(t, conn.Preload("Content").First(&parent, *saved.InReplyToId).Error)
	require.Contains(t, parent.Content.Content, "parent")
	require.NotNil(t, parent.InReplyToId)
	require.NoError(t, conn.Preload("Content").First(&grand, *parent.InReplyToId).Error)
	require.Contains(t, grand.Content.Content, "grand")
	require.Nil(t, grand.InReplyToId)
}

func TestLoadMissingReplyAncestorsUsesStoredURI(t *testing.T) {
	var paths []string
	conn, server := replyContextServer(t, func(base, path string) (int, map[string]any) {
		paths = append(paths, path)
		switch path {
		case "/users/alice":
			return http.StatusOK, map[string]any{"id": base + path, "type": "Person", "preferredUsername": "alice", "inbox": base + "/inbox"}
		case "/notes/parent":
			return http.StatusOK, map[string]any{"id": base + path, "type": "Note", "attributedTo": base + "/users/alice", "content": "<p>parent</p>", "published": "2026-11-01T00:00:00Z", "to": []any{asPublic}}
		default:
			return http.StatusNotFound, nil
		}
	})
	now := time.Now()
	holder := models.Account{Username: "holder", CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Omit("Extra").Create(&holder).Error)
	domain := "remote.example"
	origin := server.URL + "/notes/reply"
	parentURI := server.URL + "/notes/parent"
	reply := models.Status{
		AccountId: holder.Id, Domain: &domain, OriginUri: &origin,
		Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now,
		Properties: map[string]any{models.StatusPropReplyToURI: parentURI},
		Content:    models.StatusContent{Content: "<p>reply</p>"},
	}
	require.NoError(t, conn.Create(&reply).Error)

	require.NoError(t, LoadMissingReplyAncestors(context.Background(), &reply))
	require.NotContains(t, paths, "/notes/reply", "a stored parent URI is fetched directly")
	require.Contains(t, paths, "/notes/parent")
	var saved models.Status
	require.NoError(t, conn.First(&saved, reply.Id).Error)
	require.NotNil(t, saved.InReplyToId)
	require.Empty(t, models.ReplyToURI(&saved))
}

func TestLoadMissingReplyAncestorsDoesNotRefetchAKnownNonReply(t *testing.T) {
	hits := 0
	conn, server := replyContextServer(t, func(base, path string) (int, map[string]any) {
		hits++
		if path != "/notes/plain" {
			return http.StatusNotFound, nil
		}
		return http.StatusOK, map[string]any{
			"id": base + path, "type": "Note", "attributedTo": base + "/users/alice",
			"content": "<p>plain</p>", "published": "2026-11-01T00:00:00Z", "to": []any{asPublic},
		}
	})
	now := time.Now()
	holder := models.Account{Username: "holder", CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Omit("Extra").Create(&holder).Error)
	domain := "remote.example"
	origin := server.URL + "/notes/plain"
	status := models.Status{
		AccountId: holder.Id, Domain: &domain, OriginUri: &origin,
		Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now,
		Content: models.StatusContent{Content: "<p>plain</p>"},
	}
	require.NoError(t, conn.Create(&status).Error)

	require.NoError(t, LoadMissingReplyAncestors(context.Background(), &status))
	require.Equal(t, 1, hits)
	require.Nil(t, status.InReplyToId)
	var saved models.Status
	require.NoError(t, conn.First(&saved, status.Id).Error)
	require.True(t, models.ReplyParentChecked(&saved))

	require.NoError(t, LoadMissingReplyAncestors(context.Background(), &saved))
	require.Equal(t, 1, hits, "a status with no inReplyTo is not fetched again")
}

func TestCreateStatusKeepsReplyURIWhenParentFetchFails(t *testing.T) {
	conn, server := replyContextServer(t, func(base, path string) (int, map[string]any) {
		switch path {
		case "/users/sky":
			return http.StatusOK, map[string]any{"id": base + path, "type": "Person", "preferredUsername": "sky", "inbox": base + "/inbox"}
		default:
			return http.StatusUnauthorized, map[string]any{"error": "authorized_fetch"}
		}
	})
	_ = conn
	uri := server.URL + "/notes/reply"
	parent := server.URL + "/notes/parent"
	note := map[string]any{
		"id": uri, "type": "Note", "attributedTo": server.URL + "/users/sky",
		"content": "<p>reply</p>", "inReplyTo": parent,
		"published": "2026-11-04T03:17:00Z", "to": []any{asPublic},
	}
	resolver := NewResolver(nil, "https://example.com/actor")
	resolver.client = &Client{httpClient: ssrfHTTPClientForTesting()}
	resolver.noQuoteFetch = true
	saved, err := resolver.createStatusFromObject(note, uri)
	require.NoError(t, err)
	require.Nil(t, saved.InReplyToId)
	require.Equal(t, parent, models.ReplyToURI(&saved))
}

func replyContextServer(t *testing.T, serve func(base, path string) (int, map[string]any)) (*gorm.DB, *httptest.Server) {
	t.Helper()
	conn := persistenceDB(t, false)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code, body := serve(server.URL, r.URL.Path)
		if body == nil {
			http.Error(w, "", code)
			return
		}
		w.Header().Set("Content-Type", "application/activity+json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(server.Close)
	previous := replyContextResolver
	replyContextResolver = func() *Resolver {
		resolver := NewResolver(nil, "https://example.com/actor")
		resolver.client = &Client{httpClient: ssrfHTTPClientForTesting()}
		resolver.noQuoteFetch = true
		resolver.skipReplyFetch = true
		return resolver
	}
	t.Cleanup(func() { replyContextResolver = previous })
	replyParentFailures.Lock()
	replyParentFailures.until = nil
	replyParentFailures.Unlock()
	return conn, server
}

func activityJSON(doc map[string]any) string {
	raw, _ := json.Marshal(doc)
	return string(raw)
}
