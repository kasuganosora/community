package homeserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Federation query handler tests
// =============================================================================

// ----- handleQueryProfile success path (local user lookup) -----

func TestHandleQueryProfile_LocalUserNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoom{}))
	hs := minimalHS("test.local")
	hs.Auth = NewAuthService(hs)
	restore := resetGlobalHS(hs)
	defer restore()

	// Local user exists in domain check but GetLocalAccountID fails (no account)
	// → should return 404 from the "account not found" path.
	r := gin.New()
	r.GET("/query/profile", handleQueryProfile)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/query/profile?user_id=%40alice%3Atest.local", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "M_NOT_FOUND")
}

// ----- handleQueryDirectory success path (local alias resolves) -----

func TestHandleQueryDirectory_LocalAliasResolved(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoomAlias{}))
	hs := minimalHS("test.local")
	restore := resetGlobalHS(hs)
	defer restore()

	alias := &MatrixRoomAlias{
		RoomAlias: "#myroom:test.local",
		RoomID:    "!resolved:test.local",
		Creator:   "@admin:test.local",
	}
	require.NoError(t, db.GetDB().Create(alias).Error)

	r := gin.New()
	r.GET("/query/directory", handleQueryDirectory)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/query/directory?room_alias=%23myroom%3Atest.local", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp QueryDirectoryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "!resolved:test.local", resp.RoomID)
	require.Len(t, resp.Servers, 1)
	assert.Equal(t, "test.local", resp.Servers[0])
}

func TestHandleQueryDirectory_RemoteAliasSkipped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoomAlias{}))
	hs := minimalHS("test.local")
	restore := resetGlobalHS(hs)
	defer restore()

	// Alias for a different server → should not match local suffix check → 404
	r := gin.New()
	r.GET("/query/directory", handleQueryDirectory)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/query/directory?room_alias=%23room%3Aother.server", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// =============================================================================
// storeFederationPDU: member state events, tombstone, redaction
// =============================================================================

func TestStoreFederationPDU_SignedEvents(t *testing.T) {
	hs := createTestHomeServer(t)
	room, e := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: "10"})
	require.Nil(t, e)
	for _, kind := range []string{"m.room.message", "m.room.member", "m.room.tombstone"} {
		t.Run(kind, func(t *testing.T) {
			event := &MatrixEvent{RoomID: room.RoomID, Sender: "@alice:test.local", EventType: EventType(kind), Content: map[string]any{"body": "signed federation content"}}
			switch kind {
			case "m.room.member":
				event.Sender = "@bob:test.local"
				event.StateKey = new(event.Sender)
				event.Content = map[string]any{"membership": "join", "displayname": "Bob"}
			case "m.room.tombstone":
				event.StateKey = new("")
				event.Content["replacement_room"] = "!replacement:test.local"
			}
			require.NoError(t, buildLocalPDU(db.GetDB(), hs, event, "10"))
			wire, err := matrixEventToPDU(event)
			require.NoError(t, err)
			id, err := storeFederationPDU(hs, wire)
			require.NoError(t, err)
			assert.Equal(t, event.EventID, id)
			var stored MatrixEvent
			require.NoError(t, db.GetDB().Where("event_id = ?", id).First(&stored).Error)
			assert.NotEmpty(t, stored.FederationJSON)
			if kind == "m.room.member" {
				var member MatrixRoomMember
				require.NoError(t, db.GetDB().Where("room_id = ? AND user_id = ?", room.RoomID, event.Sender).First(&member).Error)
				assert.Equal(t, MembershipJoin, member.Membership)
			}
		})
	}
}
func TestStoreFederationPDU_RejectsUnsignedUnknownRoom(t *testing.T) {
	hs := createTestHomeServer(t)
	for _, kind := range []string{"m.room.message", "m.room.member", "m.room.redaction", "m.room.tombstone"} {
		_, err := storeFederationPDU(hs, map[string]any{"event_id": "$fabricated", "room_id": "!unknown:test.local", "type": kind, "sender": "@mallory:test.local", "content": map[string]any{}})
		require.Error(t, err)
	}
	var n int64
	require.NoError(t, db.GetDB().Model(&MatrixRoom{}).Where("room_id = ?", "!unknown:test.local").Count(&n).Error)
	assert.Zero(t, n)
}

// =============================================================================
// parseFederationPDU: edge cases
// =============================================================================

func TestParseFederationPDU_StateKeyExplicitNil(t *testing.T) {
	event := map[string]any{
		"event_id":  "$evt:test",
		"room_id":   "!room:test",
		"type":      "m.room.message",
		"sender":    "@alice:test",
		"state_key": nil,
		"content":   map[string]any{"body": "hello"},
	}

	_, _, _, _, _, hasStateKey, _, _ := parseFederationPDU(event)
	// state_key exists in map but is nil (interface nil). The type assertion
	// to string fails, and the fallback check `val != nil` is false for a nil
	// interface, so hasStateKey is false.
	assert.False(t, hasStateKey)
}

// =============================================================================
// computeReferenceHash: additional event types
// =============================================================================

func TestComputeReferenceHash_PowerLevelsEvent(t *testing.T) {
	event := map[string]any{
		"type":      "m.room.power_levels",
		"room_id":   "!room:test",
		"sender":    "@admin:test",
		"state_key": "",
		"content": map[string]any{
			"ban":           float64(50),
			"events":        map[string]any{"m.room.message": float64(0)},
			"users":         map[string]any{"@admin:test": float64(100)},
			"users_default": float64(0),
			"extra":         "should-be-stripped",
		},
		"origin_server_ts": float64(1234567890),
	}
	hash := computeReferenceHash(event, "m.room.power_levels")
	assert.NotEmpty(t, hash)
	assert.Equal(t, "$", string(hash[0]))
}

func TestComputeReferenceHash_JoinRulesEvent(t *testing.T) {
	event := map[string]any{
		"type":      "m.room.join_rules",
		"room_id":   "!room:test",
		"sender":    "@admin:test",
		"state_key": "",
		"content": map[string]any{
			"join_rule": "public",
			"extra":     "stripped",
		},
		"origin_server_ts": float64(1234567890),
	}
	hash := computeReferenceHash(event, "m.room.join_rules")
	assert.NotEmpty(t, hash)
	assert.Equal(t, "$", string(hash[0]))
}

func TestComputeReferenceHash_HistoryVisibilityEvent(t *testing.T) {
	event := map[string]any{
		"type":      "m.room.history_visibility",
		"room_id":   "!room:test",
		"sender":    "@admin:test",
		"state_key": "",
		"content": map[string]any{
			"history_visibility": "world_readable",
			"extra":              "stripped",
		},
		"origin_server_ts": float64(1234567890),
	}
	hash := computeReferenceHash(event, "m.room.history_visibility")
	assert.NotEmpty(t, hash)
	assert.Equal(t, "$", string(hash[0]))
}

func TestComputeReferenceHash_UnknownEventType(t *testing.T) {
	event := map[string]any{
		"type":             "org.custom.event",
		"room_id":          "!room:test",
		"sender":           "@alice:test",
		"content":          map[string]any{"body": "hello"},
		"origin_server_ts": float64(1234567890),
	}
	hash := computeReferenceHash(event, "org.custom.event")
	assert.NotEmpty(t, hash)
	assert.Equal(t, "$", string(hash[0]))
}

// =============================================================================
// fetchMissingEvents: deeper coverage paths
// =============================================================================

func TestFetchMissingEvents_AllPrevEventsKnown(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixEvent{}, &MatrixRoom{}))
	hs := minimalHS("test.local")

	// Insert a known prev_event in this room
	knownEvt := &MatrixEvent{
		EventID:        "$known:test.local",
		RoomID:         "!room:test.local",
		EventType:      EventType("m.room.message"),
		Sender:         "@alice:remote.test",
		OriginServerTS: 1000000,
	}
	require.NoError(t, db.GetDB().Create(knownEvt).Error)

	// All prev_events are known → should return early without fetching
	assert.NotPanics(t, func() {
		fetchMissingEvents(hs, map[string]any{
			"room_id":     "!room:test.local",
			"event_id":    "$new:test.local",
			"prev_events": []any{"$known:test.local"},
		}, "remote.test")
	})
}

func TestFetchMissingEvents_NoEarliestEvents(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixEvent{}, &MatrixRoom{}))
	hs := minimalHS("test.local")

	// prev_event is unknown (missing), but no earliest events in the room → return early
	assert.NotPanics(t, func() {
		fetchMissingEvents(hs, map[string]any{
			"room_id":     "!room:test.local",
			"event_id":    "$new:test.local",
			"prev_events": []any{"$unknown:remote.test"},
		}, "remote.test")
	})
}

// =============================================================================
// FederationClient: homeserver communication via mock TLS servers
// =============================================================================

func TestFederationClient_FetchServerKey_SuccessViaMockTLS(t *testing.T) {
	pub, _ := ed25519Key()
	pubB64 := base64RawStd(pub)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/_matrix/key/v2/server")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"server_name":"remote.test","valid_until_ts":9999999999999,"verify_keys":{"ed25519:1":{"key":"` + pubB64 + `"}},"old_verify_keys":{},"signatures":{}}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	resp, err := c.FetchServerKey(addr)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "remote.test", resp.ServerName)
	assert.Contains(t, resp.VerifyKeys, "ed25519:1")
}

func TestFederationClient_GetWellKnown_SuccessViaMockTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/.well-known/matrix/server", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"m.server":"delegate.example.com:443"}`))
	}))
	defer server.Close()

	// GetWellKnown uses https://serverName/.well-known/matrix/server via httpClient.
	pub, priv := ed25519Key()
	hs := &HomeServer{
		Config:     &HSConfig{Enabled: true, ServerName: "origin.local", BaseURL: "https://origin.local", FederationPort: 8448},
		keyID:      "ed25519:1",
		privateKey: priv,
		publicKey:  pub,
	}
	c := NewFederationClient(hs)
	testClient := server.Client()
	if testClient != nil && testClient.Transport != nil {
		c.httpClient.Transport = testClient.Transport
	}

	addr := strings.TrimPrefix(server.URL, "https://")
	result, err := c.GetWellKnown(addr)
	require.NoError(t, err)
	assert.Equal(t, "delegate.example.com:443", result)
}

func TestFederationClient_GetWellKnown_EmptyServerResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"m.server":""}`))
	}))
	defer server.Close()

	pub, priv := ed25519Key()
	hs := &HomeServer{
		Config:     &HSConfig{Enabled: true, ServerName: "origin.local"},
		keyID:      "ed25519:1",
		privateKey: priv,
		publicKey:  pub,
	}
	c := NewFederationClient(hs)
	testClient := server.Client()
	if testClient != nil && testClient.Transport != nil {
		c.httpClient.Transport = testClient.Transport
	}

	addr := strings.TrimPrefix(server.URL, "https://")
	// Empty m.server → fallback to addr:8448
	result, err := c.GetWellKnown(addr)
	require.NoError(t, err)
	assert.Equal(t, addr+":8448", result)
}

func TestFederationClient_QueryDirectory_SuccessViaMockTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/query/directory")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"room_id":"!resolved:dest","servers":["dest.server"]}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	resp, err := c.QueryDirectory(addr, "#room:dest.server")
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "!resolved:dest", resp.RoomID)
	require.Len(t, resp.Servers, 1)
	assert.Equal(t, "dest.server", resp.Servers[0])
}

func TestFederationClient_FetchThumbnail_SuccessViaMockTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/thumbnail/")
		writeMockFederationMedia(w, "image/png", []byte("thumbnail-bytes"))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	data, contentType, err := c.FetchThumbnail(addr, "thumb123", 64, 64, "scale")
	require.NoError(t, err)
	assert.Equal(t, []byte("thumbnail-bytes"), data)
	assert.Equal(t, "image/png", contentType)
}

func TestFederationClient_FetchThumbnail_DefaultContentType(t *testing.T) {
	// When the server returns a specific content-type, it should be used as-is.
	// The "image/jpeg" fallback only triggers when Content-Type header is
	// completely absent, which Go's httptest server won't allow (it defaults
	// to text/plain). Instead we test that the returned content-type matches
	// what the server sends.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMockFederationMedia(w, "image/webp", []byte("thumb"))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	data, contentType, err := c.FetchThumbnail(addr, "thumb456", 32, 32, "crop")
	require.NoError(t, err)
	assert.Equal(t, []byte("thumb"), data)
	assert.Equal(t, "image/webp", contentType)
}

func TestFederationClient_DownloadAndSaveMedia_FetchFailure(t *testing.T) {
	setupMinimalTestDB(t)
	c, _, _, _ := newTestFederationClient()
	// FetchMedia will fail to connect to a non-existent host
	media, err := c.DownloadAndSaveMedia("127.0.0.1.nx-test.invalid", "media123")
	assert.Error(t, err)
	assert.Nil(t, media)
	assert.Contains(t, err.Error(), "failed to fetch media")
}

func TestFederationClient_VerifySignature_RemoteFetchFailure_BogusBase64(t *testing.T) {
	setupMinimalTestDB(t)
	c, _, _, _ := newTestFederationClient()
	// Key not in cache → attempts FetchServerKey which fails to connect.
	// Signature is valid base64 but won't verify (remote key fetch fails first).
	event := map[string]any{"a": float64(1)}
	result := c.VerifySignature(event, "127.0.0.1.nx-test.invalid", "ed25519:1", "bogussig")
	assert.False(t, result)
}
