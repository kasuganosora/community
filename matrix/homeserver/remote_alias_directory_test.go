package homeserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const (
	galgameAlias = "#main:galgame.dev"
	galgameRoom  = "!gZFF7KRC90SC4zN5:galgame.dev"
	galgameName  = "galgame 聊天室"
)

// galgameDirectory is a stand-in for Dendrite's public directory: the alias
// query is authoritative, and federation publicRooms ignores the search term
// and returns every published room.
type galgameDirectory struct {
	srv           *httptest.Server
	mu            sync.Mutex
	paths         []string
	hosts         []string
	filter        string
	status        int
	listingStatus int
}

func newGalgameDirectory(t *testing.T, status int) *galgameDirectory {
	t.Helper()
	g := &galgameDirectory{status: status}
	g.srv = httptest.NewTLSServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.srv.Close)
	rt := &rewriteRoundTripper{to: g.srv}
	original := newFederationClient
	newFederationClient = func(h *HomeServer) *FederationClient {
		c := NewFederationClient(h)
		c.fedHTTPClient = &http.Client{Transport: rt, Timeout: 5 * time.Second}
		return c
	}
	t.Cleanup(func() { newFederationClient = original })
	return g
}

func (g *galgameDirectory) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.paths = append(g.paths, r.URL.Path)
	g.hosts = append(g.hosts, r.Host)
	if r.URL.Path == "/_matrix/federation/v1/publicRooms" {
		var req publicDirectoryRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		g.filter = req.Filter.GenericSearchTerm
	}
	status := g.status
	if r.URL.Path == "/_matrix/federation/v1/publicRooms" && g.listingStatus != 0 {
		status = g.listingStatus
	}
	g.mu.Unlock()
	if status != http.StatusOK {
		http.Error(w, `{"errcode":"M_FORBIDDEN"}`, status)
		return
	}
	// Only the alias's own server publishes these rooms. An explicit directory
	// query for a different server must stay on that server and stay empty.
	if !strings.Contains(r.Host, "galgame.dev") {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/_matrix/federation/v1/publicRooms" {
			_, _ = io.WriteString(w, `{"chunk":[],"total_room_count_estimate":0}`)
			return
		}
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/query/directory"):
		if r.URL.Query().Get("room_alias") != galgameAlias {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"room_id":"`+galgameRoom+`","servers":["galgame.dev","matrix.org"]}`)
	case r.URL.Path == "/_matrix/federation/v1/publicRooms":
		_, _ = io.WriteString(w, `{
			"chunk":[
				{"room_id":"!bridge:galgame.dev","name":"bridge","canonical_alias":"#test_bridge:galgame.dev","num_joined_members":1,"join_rule":"public"},
				{"room_id":"!other:galgame.dev","name":"other","canonical_alias":"#wu138di:galgame.dev","num_joined_members":2,"join_rule":"public"},
				{"room_id":"!kungal:matrix.org","name":"kungal","canonical_alias":"#kungalgame:matrix.org","num_joined_members":3,"join_rule":"public"},
				{"room_id":"`+galgameRoom+`","name":"`+galgameName+`","topic":"galgame","canonical_alias":"`+galgameAlias+`","num_joined_members":284,"join_rule":"public","avatar_url":"mxc://galgame.dev/avatar"}
			],
			"next_batch":"unfiltered-page",
			"total_room_count_estimate":4
		}`)
	default:
		http.NotFound(w, r)
	}
}

func (g *galgameDirectory) saw(suffix string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, path := range g.paths {
		if strings.Contains(path, suffix) {
			return true
		}
	}
	return false
}

func publicRoomsRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/_matrix/client/v3/publicRooms", handleGetPublicRoomsClient)
	router.GET("/_matrix/client/v3/directory/room/:roomAlias", handleGetRoomAlias)
	return router
}

func TestPublicRoomsSearchResolvesRemoteAliasAndDropsUnfilteredRooms(t *testing.T) {
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	t.Cleanup(restore)
	remote := newGalgameDirectory(t, http.StatusOK)
	router := publicRoomsRouter()

	w := httptest.NewRecorder()
	body := `{"limit":10,"filter":{"generic_search_term":"` + galgameAlias + `"}}`
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/_matrix/client/v3/publicRooms", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var page publicDirectoryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	require.Len(t, page.Chunk, 1)
	require.Equal(t, galgameRoom, page.Chunk[0].RoomID)
	require.Equal(t, galgameName, page.Chunk[0].Name)
	require.Equal(t, galgameAlias, page.Chunk[0].CanonicalAlias)
	require.EqualValues(t, 284, page.Chunk[0].NumJoinedMembers)
	require.Empty(t, page.NextBatch, "a filtered page must not continue the remote unfiltered listing")
	require.EqualValues(t, 1, page.TotalRoomCountEstimate)
	require.NotContains(t, w.Body.String(), "test_bridge")
	require.NotContains(t, w.Body.String(), "kungalgame")
	require.True(t, remote.saw("/query/directory"))
	require.True(t, remote.saw("/publicRooms"))
	remote.mu.Lock()
	require.Equal(t, galgameAlias, remote.filter)
	remote.mu.Unlock()
}

func TestPublicRoomsKeywordDoesNotQueryRemoteServers(t *testing.T) {
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	t.Cleanup(restore)
	remote := newGalgameDirectory(t, http.StatusOK)
	router := publicRoomsRouter()
	for _, term := range []string{"main", "galgame.dev"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/_matrix/client/v3/publicRooms", strings.NewReader(`{"filter":{"generic_search_term":"`+term+`"}}`)))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.NotContains(t, w.Body.String(), galgameRoom)
	}
	require.False(t, remote.saw("/publicRooms"))
	require.False(t, remote.saw("/query/directory"))
}

func TestPublicRoomsRemoteAliasRefusalIsNotEmptySuccess(t *testing.T) {
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	t.Cleanup(restore)
	_ = newGalgameDirectory(t, http.StatusForbidden)
	router := publicRoomsRouter()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/_matrix/client/v3/publicRooms", strings.NewReader(`{"filter":{"generic_search_term":"`+galgameAlias+`"}}`)))
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), `"chunk":[]`)
}

func TestPublicRoomsExplicitServerIsNotRedirectedToAliasServer(t *testing.T) {
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	t.Cleanup(restore)
	remote := newGalgameDirectory(t, http.StatusOK)
	router := publicRoomsRouter()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/_matrix/client/v3/publicRooms?server=matrix.org", strings.NewReader(`{"filter":{"generic_search_term":"`+galgameAlias+`"}}`)))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), galgameRoom, "an explicit directory server stays that server")
	remote.mu.Lock()
	defer remote.mu.Unlock()
	for _, host := range remote.hosts {
		require.NotContains(t, host, "galgame.dev")
		require.Contains(t, host, "matrix.org")
	}
}

func TestClientDirectoryResolvesPercentEncodedRemoteAlias(t *testing.T) {
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	t.Cleanup(restore)
	remote := newGalgameDirectory(t, http.StatusOK)
	router := publicRoomsRouter()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/_matrix/client/v3/directory/room/"+url.PathEscape(galgameAlias), nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), galgameRoom)
	require.True(t, remote.saw("/query/directory"))
}

func TestPublicRoomsMatrixLinkAndAliasResolvedWhenListingClosed(t *testing.T) {
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	t.Cleanup(restore)
	remote := newGalgameDirectory(t, http.StatusOK)
	router := publicRoomsRouter()
	link := "https://matrix.to/#/" + url.PathEscape(galgameAlias)
	payload, err := json.Marshal(map[string]any{"limit": 10, "filter": map[string]string{"generic_search_term": link}})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/_matrix/client/v3/publicRooms", strings.NewReader(string(payload))))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), galgameRoom)
	require.NotContains(t, w.Body.String(), "test_bridge")

	remote.mu.Lock()
	remote.listingStatus = http.StatusForbidden
	remote.mu.Unlock()
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/_matrix/client/v3/publicRooms", strings.NewReader(`{"filter":{"generic_search_term":"`+galgameAlias+`"}}`)))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), galgameRoom)
	require.NotContains(t, w.Body.String(), "test_bridge")
}

func TestJoinedAndInboundDirectoryDoNotFollowRemoteAlias(t *testing.T) {
	setupWebDirectoryDB(t)
	remote := newGalgameDirectory(t, http.StatusOK)
	router := directoryTestRouter("@alice:test.local")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/groups/search?scope=joined&q="+url.QueryEscape(galgameAlias), nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), galgameName)

	gin.SetMode(gin.TestMode)
	inbound := gin.New()
	inbound.POST("/publicRooms", handleGetPublicRooms)
	w = httptest.NewRecorder()
	inbound.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/publicRooms", strings.NewReader(`{"filter":{"generic_search_term":"`+galgameAlias+`"}}`)))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), galgameRoom)
	require.False(t, remote.saw("/publicRooms"))
	require.False(t, remote.saw("/query/directory"))
}

func TestWebGroupSearchAndResolveRemoteAlias(t *testing.T) {
	setupWebDirectoryDB(t)
	remote := newGalgameDirectory(t, http.StatusOK)
	router := directoryTestRouter("@alice:test.local")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/groups/search?scope=local&q="+url.QueryEscape(galgameAlias), nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), galgameName)
	require.Contains(t, w.Body.String(), galgameAlias)
	require.NotContains(t, w.Body.String(), "test_bridge")
	require.NotContains(t, w.Body.String(), "kungalgame")
	require.Contains(t, w.Body.String(), `"source":"galgame.dev"`)

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/groups/resolve", strings.NewReader(`{"target":"`+galgameAlias+`"}`)))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), galgameName)
	require.Contains(t, w.Body.String(), `"topic":"galgame"`)
	require.Contains(t, w.Body.String(), galgameRoom)
	require.True(t, remote.saw("/query/directory"))
	require.True(t, remote.saw("/publicRooms"))
}
