package homeserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestWebGroupTargetParsing(t *testing.T) {
	tests := []struct {
		input, target string
		via           []string
	}{
		{"#chat:matrix.org", "#chat:matrix.org", nil},
		{"!v12hash", "!v12hash", []string{"matrix.org"}},
		{"https://matrix.to/#/%23chat%3Amatrix.org?via=matrix.org", "#chat:matrix.org", []string{"matrix.org"}},
		{"https://matrix.to/#/!hash?via=one.example&via=two.example", "!hash", []string{"one.example", "two.example"}},
		{"matrix:r/chat:matrix.org?via=matrix.org", "#chat:matrix.org", []string{"matrix.org"}},
		{"matrix:roomid/hash?via=matrix.org", "!hash", []string{"matrix.org"}},
		{webGroupID("!opaque:example.org"), "!opaque:example.org", nil},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			target, via, err := parseWebGroupTarget(tt.input, tt.via)
			require.NoError(t, err)
			require.Equal(t, tt.target, target)
			require.ElementsMatch(t, tt.via, via)
		})
	}
	for _, input := range []string{"http://matrix.to/#/!x:test.local", "https://evil.test/#/!x:test.local", "https://matrix.to.evil/#/!x:test.local", "https://user@matrix.to/#/!x:test.local", "https://matrix.to/#/@alice:test.local", "https://matrix.to/#/!x:test.local/$event", "matrix:u/alice:test.local", "matrix:roomid/room/e/event", "#room", "!x?via=x", "#room:localhost/path", "https://matrix.to/#/!hash?via=https://evil"} {
		_, _, err := parseWebGroupTarget(input, nil)
		require.Error(t, err, input)
	}
}
func TestPublicDirectoryVisibilityPaginationAndBoundCursor(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	for _, r := range []MatrixRoom{
		{RoomID: "!a:test.local", Name: new("Listed invitation group"), Visibility: "public", JoinRule: "invite"},
		{RoomID: "!b:test.local", Name: new("Listed public group"), Visibility: "public", JoinRule: "public"},
		{RoomID: "!secret:test.local", Name: new("Unlisted public group"), Visibility: "private", JoinRule: "public"},
	} {
		require.NoError(t, db.GetDB().Create(&r).Error)
	}
	req := publicDirectoryRequest{Limit: 1}
	bind := directoryBinding("alice", "local", "")
	first, err := localPublicDirectory(context.Background(), hs, req, bind, false)
	require.NoError(t, err)
	require.Len(t, first.Chunk, 1)
	require.Equal(t, "!a:test.local", first.Chunk[0].RoomID)
	require.EqualValues(t, 2, first.TotalRoomCountEstimate)
	require.NotEmpty(t, first.NextBatch)
	req.Since = first.NextBatch
	second, err := localPublicDirectory(context.Background(), hs, req, bind, false)
	require.NoError(t, err)
	require.Len(t, second.Chunk, 1)
	require.Equal(t, "!b:test.local", second.Chunk[0].RoomID)
	require.Empty(t, second.NextBatch)
	_, err = localPublicDirectory(context.Background(), hs, req, directoryBinding("bob", "local", ""), false)
	require.ErrorContains(t, err, "cursor")
	_, err = decodeDirectoryCursor(hs, first.NextBatch+"tamper", bind)
	require.Error(t, err)
	token := encodeDirectoryCursor(hs, bind, "position")
	_, err = decodeDirectoryCursor(hs, token, directoryBinding("alice", "remote", ""))
	require.Error(t, err)
}
func TestPublicDirectorySearchEscapesWildcardAndFindsAliases(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	for _, r := range []MatrixRoom{{RoomID: "!a:test.local", Name: new("100% chat"), Visibility: "public"}, {RoomID: "!b:test.local", Name: new("general"), Visibility: "public"}} {
		require.NoError(t, db.GetDB().Create(&r).Error)
	}
	require.NoError(t, db.GetDB().Create(&MatrixRoomAlias{RoomID: "!b:test.local", RoomAlias: "#gamedev:test.local"}).Error)
	req := publicDirectoryRequest{Limit: 10}
	req.Filter.GenericSearchTerm = "gamedev"
	result, err := localPublicDirectory(context.Background(), hs, req, "bind", false)
	require.NoError(t, err)
	require.Len(t, result.Chunk, 1)
	require.Equal(t, "!b:test.local", result.Chunk[0].RoomID)
	req.Filter.GenericSearchTerm = "%"
	result, err = localPublicDirectory(context.Background(), hs, req, "bind2", false)
	require.NoError(t, err)
	require.Len(t, result.Chunk, 1)
	require.Equal(t, "!a:test.local", result.Chunk[0].RoomID)
}
func TestPublicDirectoryPostLimitClampedAndInvalidCursorRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()
	router := gin.New()
	router.POST("/publicRooms", func(c *gin.Context) {
		req, merr := parsePublicDirectoryRequest(c)
		require.Nil(t, merr)
		require.Equal(t, publicDirectoryMaxLimit, req.Limit)
		c.JSON(200, gin.H{})
	})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/publicRooms", strings.NewReader(`{"limit":999999}`)))
	require.Equal(t, 200, w.Code)
	router.GET("/publicRooms", handleGetPublicRooms)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/publicRooms?since=untrusted", nil))
	require.Equal(t, 400, w.Code)
}
func TestRemotePublicDirectoryForwardsSearchAndPagination(t *testing.T) {
	hs := minimalHS("local.example")
	seen := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/_matrix/federation/v1/publicRooms", r.URL.Path)
		require.Contains(t, r.Header.Get("Authorization"), "X-Matrix")
		var req publicDirectoryRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "games", req.Filter.GenericSearchTerm)
		require.Equal(t, "opaque-remote-token", req.Since)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"chunk":[{"room_id":"!hash","name":"Game chat","num_joined_members":12},{"room_id":"!hash","name":"duplicate"}],"next_batch":"page2","total_room_count_estimate":20}`))
	}))
	defer server.Close()
	client := NewFederationClient(hs)
	client.fedHTTPClient = server.Client()
	req := publicDirectoryRequest{Limit: 10, Since: "opaque-remote-token"}
	req.Filter.GenericSearchTerm = "games"
	result, err := client.PublicRoomsContext(context.Background(), strings.TrimPrefix(server.URL, "https://"), req)
	require.NoError(t, err)
	require.Equal(t, 1, seen)
	require.Len(t, result.Chunk, 1)
	require.Equal(t, "page2", result.NextBatch)
}
func setupWebDirectoryDB(t *testing.T) *HomeServer {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&BridgeConversation{}, &models.MatrixDMDelivery{}))
	hs := fullHS("test.local")
	restore := resetGlobalHS(hs)
	t.Cleanup(restore)
	return hs
}
func directoryTestRouter(user string) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("web_group_identity", true); c.Set("user_id", user); c.Next() })
	registerWebGroupSearchRoutes(router.Group(""))
	return router
}
func TestWebGroupSearchPrivateRoomsAndBridgeIsolation(t *testing.T) {
	hs := setupWebDirectoryDB(t)
	_ = hs
	for _, r := range []MatrixRoom{{RoomID: "!mine:test.local", Name: new("private mine")}, {RoomID: "!other:test.local", Name: new("private other")}, {RoomID: "!bridge:test.local", Name: new("private bridge")}, {RoomID: "!public:test.local", Name: new("public room"), Visibility: "public"}} {
		require.NoError(t, db.GetDB().Create(&r).Error)
	}
	for _, m := range []MatrixRoomMember{{RoomID: "!mine:test.local", UserID: "@alice:test.local", Membership: MembershipJoin}, {RoomID: "!bridge:test.local", UserID: "@alice:test.local", Membership: MembershipJoin}, {RoomID: "!other:test.local", UserID: "@bob:test.local", Membership: MembershipJoin}} {
		require.NoError(t, db.GetDB().Create(&m).Error)
	}
	require.NoError(t, db.GetDB().Create(&BridgeConversation{ID: "known-bridge", RoomID: "!bridge:test.local"}).Error)
	router := directoryTestRouter("@alice:test.local")
	for _, tc := range []struct{ scope, include, exclude string }{{"joined", "private mine", "private other"}, {"local", "public room", "private mine"}} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/groups/search?scope="+tc.scope, nil))
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), tc.include)
		require.NotContains(t, w.Body.String(), tc.exclude)
		require.NotContains(t, w.Body.String(), "private bridge")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/groups/resolve", strings.NewReader(`{"target":"!other:test.local"}`)))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "private other")
	require.NotContains(t, w.Body.String(), `"group":`)
	var memberships int64
	require.NoError(t, db.GetDB().Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ?", "!other:test.local", "@alice:test.local").Count(&memberships).Error)
	require.Zero(t, memberships, "read-only resolve never joins")
}
func TestWebGroupSearchRemoteFailureIsNotEmptySuccess(t *testing.T) {
	hs := setupWebDirectoryDB(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{"errcode":"M_FORBIDDEN"}`, 403) }))
	defer server.Close()
	original := newFederationClient
	newFederationClient = func(h *HomeServer) *FederationClient {
		client := NewFederationClient(h)
		client.fedHTTPClient = server.Client()
		return client
	}
	defer func() { newFederationClient = original }()
	_ = hs
	router := directoryTestRouter("@alice:test.local")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/groups/search?scope=remote&server="+url.QueryEscape(strings.TrimPrefix(server.URL, "https://")), nil))
	require.Equal(t, 403, w.Code)
	require.Contains(t, w.Body.String(), "directory_closed")
}
func TestWebDirectoryCursorSigningKey(t *testing.T) {
	hs := minimalHS("test.local")
	token := encodeDirectoryCursor(hs, "bind", "next")
	raw := strings.Split(token, ".")
	require.Len(t, raw, 2)
	position, err := decodeDirectoryCursor(hs, token, "bind")
	require.NoError(t, err)
	require.Equal(t, "next", position)
	// A different server signing key and a changed search cannot reuse a cursor.
	_, err = decodeDirectoryCursor(minimalHS("test.local"), token, "bind")
	require.Error(t, err)
}

func TestPublicDirectoryAvatarReferencesBoundToUserAndCannotExposeLocalMedia(t *testing.T) {
	hs := minimalHS("test.local")
	entry := publicDirectoryEntry{RoomID: "!room:remote.example", AvatarURL: "mxc://remote.example/image"}
	dto := publicEntryGroup(hs, entry, "remote.example", "@alice:test.local")
	avatar, ok := dto["avatar_url"].(string)
	require.True(t, ok)
	uri, err := url.Parse(avatar)
	require.NoError(t, err)
	mxc, err := decodeDirectoryCursor(hs, uri.Query().Get("ref"), directoryBinding("directory-avatar", "@alice:test.local"))
	require.NoError(t, err)
	require.Equal(t, entry.AvatarURL, mxc)
	_, err = decodeDirectoryCursor(hs, uri.Query().Get("ref"), directoryBinding("directory-avatar", "@bob:test.local"))
	require.Error(t, err)
	entry.AvatarURL = "mxc://test.local/private-image"
	remote := publicEntryGroup(hs, entry, "remote.example", "@alice:test.local")
	require.NotContains(t, remote, "avatar_url", "foreign directory cannot authorize local private media")
	local := publicEntryGroup(hs, entry, "test.local", "@alice:test.local")
	require.Contains(t, local, "avatar_url", "our own explicitly public room metadata can reference its avatar")
}

func TestDirectoryPreviewRetainsRemoteJoinRuleWithoutJoining(t *testing.T) {
	hs := setupWebDirectoryDB(t)
	entry := publicDirectoryEntry{RoomID: "!unknown-hash", Name: "Approval group", JoinRule: "knock"}
	dto := publicEntryGroup(hs, entry, "remote.example", "@alice:test.local")
	body, err := json.Marshal(gin.H{"target": entry.RoomID, "preview_ref": dto["preview_ref"]})
	require.NoError(t, err)
	router := directoryTestRouter("@alice:test.local")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/groups/resolve", strings.NewReader(string(body))))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"join_rule":"knock"`)
	require.Contains(t, w.Body.String(), "remote.example")
	var count int64
	require.NoError(t, db.GetDB().Model(&MatrixRoomMember{}).Count(&count).Error)
	require.Zero(t, count)
	w = httptest.NewRecorder()
	directoryTestRouter("@bob:test.local").ServeHTTP(w, httptest.NewRequest("POST", "/groups/resolve", strings.NewReader(string(body))))
	require.Equal(t, 400, w.Code)
	body, err = json.Marshal(gin.H{"target": "!different-hash", "preview_ref": dto["preview_ref"]})
	require.NoError(t, err)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/groups/resolve", strings.NewReader(string(body))))
	require.Equal(t, 400, w.Code)
}
