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

func TestStoreFederationPDU_MessageEvent(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixEvent{}, &MatrixRoom{}, &MatrixRoomState{}))
	hs := minimalHS("store.test")

	event := map[string]any{
		"event_id":         "$msgevt:store.test",
		"room_id":          "!msgroom:store.test",
		"type":             "m.room.message",
		"sender":           "@alice:store.test",
		"content":          map[string]any{"body": "hello world"},
		"origin_server_ts": float64(1234567890),
	}

	eventID, err := storeFederationPDU(hs, event)
	require.NoError(t, err)
	assert.Equal(t, "$msgevt:store.test", eventID)

	// Verify event was stored
	var stored MatrixEvent
	require.NoError(t, db.GetDB().Where("event_id = ?", "$msgevt:store.test").First(&stored).Error)
	assert.Equal(t, "!msgroom:store.test", stored.RoomID)
	assert.Equal(t, "@alice:store.test", stored.Sender)
}

func TestStoreFederationPDU_MemberStateEvent(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixEvent{}, &MatrixRoom{}, &MatrixRoomState{}, &MatrixRoomMember{}))
	hs := minimalHS("store.test")

	// First create the room
	createEvent := map[string]any{
		"event_id":         "$create2:store.test",
		"room_id":          "!memberroom:store.test",
		"type":             "m.room.create",
		"sender":           "@creator:store.test",
		"content":          map[string]any{"creator": "@creator:store.test"},
		"origin_server_ts": float64(1234567890),
	}
	_, err := storeFederationPDU(hs, createEvent)
	require.NoError(t, err)

	// Now store a member join event
	memberEvent := map[string]any{
		"event_id":         "$memberevt:store.test",
		"room_id":          "!memberroom:store.test",
		"type":             "m.room.member",
		"sender":           "@joiner:store.test",
		"state_key":        "@joiner:store.test",
		"content":          map[string]any{"membership": "join", "displayname": "Joiner"},
		"origin_server_ts": float64(1234567891),
	}

	eventID, err := storeFederationPDU(hs, memberEvent)
	require.NoError(t, err)
	assert.Equal(t, "$memberevt:store.test", eventID)

	// Verify room state was stored
	var state MatrixRoomState
	require.NoError(t, db.GetDB().Where("room_id = ? AND event_type = ?", "!memberroom:store.test", "m.room.member").First(&state).Error)
	assert.Equal(t, "@joiner:store.test", state.StateKey)

	// Verify room member was stored
	var member MatrixRoomMember
	require.NoError(t, db.GetDB().Where("room_id = ? AND user_id = ?", "!memberroom:store.test", "@joiner:store.test").First(&member).Error)
	assert.Equal(t, MembershipJoin, member.Membership)
}

func TestStoreFederationPDU_RedactionEvent(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixEvent{}, &MatrixRoom{}, &MatrixRoomState{}))
	hs := minimalHS("store.test")

	// Store the original message
	origEvent := map[string]any{
		"event_id":         "$origmsg:store.test",
		"room_id":          "!redactroom:store.test",
		"type":             "m.room.message",
		"sender":           "@alice:store.test",
		"content":          map[string]any{"body": "to be redacted"},
		"origin_server_ts": float64(1234567890),
	}
	_, err := storeFederationPDU(hs, origEvent)
	require.NoError(t, err)

	// Store a redaction event
	redactEvent := map[string]any{
		"event_id":         "$redactevt:store.test",
		"room_id":          "!redactroom:store.test",
		"type":             "m.room.redaction",
		"sender":           "@alice:store.test",
		"content":          map[string]any{"redacts": "$origmsg:store.test"},
		"redacts":          "$origmsg:store.test",
		"origin_server_ts": float64(1234567891),
	}
	eventID, err := storeFederationPDU(hs, redactEvent)
	require.NoError(t, err)
	assert.Equal(t, "$redactevt:store.test", eventID)

	// Verify original event content was redacted
	var redacted MatrixEvent
	require.NoError(t, db.GetDB().Where("event_id = ?", "$origmsg:store.test").First(&redacted).Error)
	if redacted.Content != nil {
		assert.Empty(t, redacted.Content)
	}
}

func TestStoreFederationPDU_TombstoneEvent(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixEvent{}, &MatrixRoom{}, &MatrixRoomState{}))
	hs := minimalHS("store.test")
	// RoomUpgrade service is nil; the code checks for nil before calling
	// migratePushRules, so this should not panic.

	event := map[string]any{
		"event_id":         "$tomb:store.test",
		"room_id":          "!tombroom:store.test",
		"type":             "m.room.tombstone",
		"sender":           "@admin:store.test",
		"state_key":        "",
		"content":          map[string]any{"replacement_room": "!newroom:store.test", "body": "Room upgraded"},
		"origin_server_ts": float64(1234567890),
	}
	eventID, err := storeFederationPDU(hs, event)
	require.NoError(t, err)
	assert.Equal(t, "$tomb:store.test", eventID)
}

func TestStoreFederationPDU_RedactionNoContentField(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixEvent{}, &MatrixRoom{}, &MatrixRoomState{}))
	hs := minimalHS("store.test")

	// Redaction event with redacts at top-level only (not in content)
	event := map[string]any{
		"event_id":         "$redact2:store.test",
		"room_id":          "!room2:store.test",
		"type":             "m.room.message",
		"sender":           "@alice:store.test",
		"content":          map[string]any{"body": "msg"},
		"origin_server_ts": float64(1234567890),
	}
	_, err := storeFederationPDU(hs, event)
	require.NoError(t, err)

	// Now store a redaction with redacts only at top level
	redactEvent := map[string]any{
		"event_id":         "$redact3:store.test",
		"room_id":          "!room2:store.test",
		"type":             "m.room.redaction",
		"sender":           "@alice:store.test",
		"redacts":          "$redact2:store.test",
		"content":          map[string]any{},
		"origin_server_ts": float64(1234567891),
	}
	eventID, err := storeFederationPDU(hs, redactEvent)
	require.NoError(t, err)
	assert.Equal(t, "$redact3:store.test", eventID)
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
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("thumbnail-bytes"))
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
		w.Header().Set("Content-Type", "image/webp")
		_, _ = w.Write([]byte("thumb"))
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
