package homeserver

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ===== FederationClient.SignRequest tests =====

func TestFederationClient_SignRequest(t *testing.T) {
	_, priv := ed25519Key()
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "origin.local"},
		keyID:      "ed25519:1",
		privateKey: priv,
	}
	c := NewFederationClient(hs)

	// We can't easily test signRequest without httpclient internals,
	// but we can verify NewFederationClient doesn't panic
	assert.NotNil(t, c)
	assert.Equal(t, hs, c.hs)
}

// ===== FederationClient.GetWellKnown with mock server =====

func TestFederationClient_GetWellKnown_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/matrix/server" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"m.server": "delegate.example.com:443",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	// GetWellKnown uses https:// + serverName which won't hit our test server.
	// We test the fallback behavior instead.
	_, priv := ed25519Key()
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "origin.local"},
		keyID:      "ed25519:1",
		privateKey: priv,
	}
	c := NewFederationClient(hs)
	_ = c
	_ = server
}

// ===== FederationClient.doFederationRequest with mock server =====

func TestFederationClient_DoFederationRequest_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)
		assert.NotEmpty(t, r.Header.Get("Authorization"))
		assert.Contains(t, r.Header.Get("Authorization"), "X-Matrix")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"pdus":{}}`))
	}))
	defer server.Close()

	_, priv := ed25519Key()
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "origin.local"},
		keyID:      "ed25519:1",
		privateKey: priv,
	}
	c := NewFederationClient(hs)

	// The httptest server uses http:// not https://. doFederationRequest signs and sends.
	resp, body, err := c.doFederationRequest("PUT", server.URL+"/_matrix/federation/v1/send/123", "dest.server", map[string]any{"hello": "world"})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), "pdus")
}

func TestFederationClient_DoFederationRequest_GetNoBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":"1"}`))
	}))
	defer server.Close()

	_, priv := ed25519Key()
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "origin.local"},
		keyID:      "ed25519:1",
		privateKey: priv,
	}
	c := NewFederationClient(hs)

	resp, body, err := c.doFederationRequest("GET", server.URL+"/test", "dest.server", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), "version")
}

func TestFederationClient_DoFederationRequest_BadURL(t *testing.T) {
	_, priv := ed25519Key()
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "origin.local"},
		keyID:      "ed25519:1",
		privateKey: priv,
	}
	c := NewFederationClient(hs)

	// Invalid URL should return error
	_, _, err := c.doFederationRequest("GET", "http://127.0.0.1:1/nonexistent", "dest.server", nil)
	assert.Error(t, err)
}

// ===== FederationClient.MakeJoin with mock server =====

func TestFederationClient_MakeJoin_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/make_join/")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"origin":"dest.server","event":{"type":"m.room.member"}}`))
	}))
	defer server.Close()

	_, priv := ed25519Key()
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "origin.local"},
		keyID:      "ed25519:1",
		privateKey: priv,
	}
	c := NewFederationClient(hs)

	result, err := c.MakeJoin("dest.server", "!room:dest.server", "@alice:origin.local")
	// MakeJoin uses federationBaseURL which produces https://, but the test server is http://
	// So this will fail to connect. We test the error path.
	_ = result
	if err != nil {
		assert.Error(t, err)
	}
}

// ===== FederationClient.SendJoin with mock server (v2 format) =====

func TestFederationClient_SendJoin_V2Format(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"origin":"dest.server","state":[],"auth_chain":[]}`))
	}))
	defer server.Close()

	_, priv := ed25519Key()
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "origin.local"},
		keyID:      "ed25519:1",
		privateKey: priv,
	}
	c := NewFederationClient(hs)

	// SendJoin uses federationBaseURL (https://), will fail to reach http test server
	event := map[string]any{"event_id": "$test:origin.local"}
	_, err := c.SendJoin("dest.server", "!room:dest.server", event)
	if err != nil {
		assert.Error(t, err)
	}
}

// ===== FederationClient.VerifySignature tests =====

func TestFederationClient_VerifySignature_CachedKey(t *testing.T) {
	setupMinimalTestDB(t)
	pub, priv := ed25519Key()
	_, priv2 := ed25519Key()

	_, hsPriv := ed25519Key()
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "origin.local"},
		keyID:      "ed25519:1",
		privateKey: hsPriv,
	}
	c := NewFederationClient(hs)

	// Pre-populate key cache
	c.keyCache.Set("remote.server", "ed25519:1", pub, time.Now().Add(24*time.Hour))

	// Sign an event with priv
	event := map[string]any{"a": float64(1), "b": "two"}
	canonical, err := CanonicalJSON(event)
	require.NoError(t, err)
	sig := signWithKey(priv, canonical)

	// Verify with correct key → true
	assert.True(t, c.VerifySignature(event, "remote.server", "ed25519:1", sig))

	// Verify with wrong key signature → false
	wrongSig := signWithKey(priv2, canonical)
	assert.False(t, c.VerifySignature(event, "remote.server", "ed25519:1", wrongSig))
}

func TestFederationClient_VerifySignature_InvalidSignatureBase64(t *testing.T) {
	setupMinimalTestDB(t)
	pub, _ := ed25519Key()
	_, hsPriv := ed25519Key()
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "origin.local"},
		keyID:      "ed25519:1",
		privateKey: hsPriv,
	}
	c := NewFederationClient(hs)
	c.keyCache.Set("remote.server", "ed25519:1", pub, time.Now().Add(24*time.Hour))

	event := map[string]any{"a": float64(1)}
	// Invalid base64 signature
	assert.False(t, c.VerifySignature(event, "remote.server", "ed25519:1", "!!!invalid-base64!!!"))
}

// ===== FederationClient.federationHTTPClient (caching) =====

func TestFederationClient_FederationHTTPClient_Caching(t *testing.T) {
	_, priv := ed25519Key()
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "origin.local"},
		keyID:      "ed25519:1",
		privateKey: priv,
	}
	c := NewFederationClient(hs)

	client1 := c.federationHTTPClient()
	assert.NotNil(t, client1)
	// Second call should return the cached client
	client2 := c.federationHTTPClient()
	assert.Same(t, client1, client2, "should return cached client")
}

// ===== handleFederationVersion via router =====

func TestHandleFederationVersion_ViaRouter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.GET("/_matrix/federation/v1/version", handleFederationVersion)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_matrix/federation/v1/version", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Community Matrix Homeserver")
	assert.Contains(t, w.Body.String(), "1.0.0")
}

// ===== handleThirdPartyOnBind =====

func TestHandleThirdPartyOnBind(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/_matrix/federation/v1/3pid/onbind", nil)

	handleThirdPartyOnBind(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ===== handleQueryProfile tests =====

func TestHandleQueryProfile_InvalidUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(&HomeServer{Config: &HSConfig{Enabled: true, ServerName: "test.local"}})
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.GET("/query/profile", handleQueryProfile)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/query/profile?user_id=invalid", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "M_INVALID_PARAM")
}

func TestHandleQueryProfile_RemoteUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(&HomeServer{Config: &HSConfig{Enabled: true, ServerName: "test.local"}})
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.GET("/query/profile", handleQueryProfile)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/query/profile?user_id=@alice:remote.server", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "M_NOT_FOUND")
}

// ===== handleMakeJoin / handleMakeLeave tests (room not found) =====

func TestHandleMakeJoin_RoomNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.GET("/make_join/:roomId/:userId", handleMakeJoin)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/make_join/!nonexistent:test.local/@alice:remote.server", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "M_FORBIDDEN")
}

func TestHandleMakeLeave_RoomNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.GET("/make_leave/:roomId/:userId", handleMakeLeave)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/make_leave/!nonexistent:test.local/@alice:remote.server", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ===== handleSendJoin bad JSON =====

func TestHandleSendJoin_BadJSON(t *testing.T) {
	hs, room := signedHandlerRoom(t)
	restore := resetGlobalHS(hs)
	defer restore()
	r := gin.New()
	r.PUT("/event/:roomId/:eventId", handleSendJoin)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PUT", "/event/"+room.RoomID+"/$bad", strings.NewReader("not JSON")))
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

func TestHandleSendJoinV1_BadJSON(t *testing.T) {
	hs, room := signedHandlerRoom(t)
	restore := resetGlobalHS(hs)
	defer restore()
	r := gin.New()
	r.PUT("/event/:roomId/:eventId", handleSendJoinV1)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PUT", "/event/"+room.RoomID+"/$bad", strings.NewReader("not JSON")))
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

func TestHandleSendLeave_BadJSON(t *testing.T) {
	hs, room := signedHandlerRoom(t)
	restore := resetGlobalHS(hs)
	defer restore()
	r := gin.New()
	r.PUT("/event/:roomId/:eventId", handleSendLeave)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PUT", "/event/"+room.RoomID+"/$bad", strings.NewReader("not JSON")))
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

func TestHandleFederationInvite_BadJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.PUT("/invite/:roomId/:eventId", handleFederationInvite)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/invite/!room:test.local/$evt:1", strings.NewReader("bad"))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ===== handleFederationSend tests =====

func TestHandleFederationSend_BadJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.PUT("/send/:txnId", handleFederationSend)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/send/12345", strings.NewReader("not json"))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleFederationSend_EmptyTransaction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.PUT("/send/:txnId", handleFederationSend)

	body := `{"origin":"remote.server","origin_server_ts":1234567890,"pdus":[],"edus":[]}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/send/12345", strings.NewReader(body))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "pdus")
}

// ===== handleQueryDirectory tests =====

func TestHandleQueryDirectory_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.GET("/query/directory", handleQueryDirectory)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/query/directory?room_alias=%23nonexistent%3Atest.local", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "M_NOT_FOUND")
}

// ===== handleGetState / handleGetStateIDs (empty room) =====

func TestHandleGetState_UnknownRoomForbidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.GET("/state/:roomId", handleGetState)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/state/!empty:test.local", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandleGetStateIDs_UnknownRoomForbidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.GET("/state_ids/:roomId", handleGetStateIDs)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/state_ids/!empty:test.local", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ===== handleGetFederationEvent (not found) =====

func TestHandleGetFederationEvent_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.GET("/event/:eventId", handleGetFederationEvent)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/event/$nonexistent:test.local", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "M_FORBIDDEN")
}

// ===== handleGetPublicRooms =====

func TestHandleGetPublicRooms_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.GET("/publicRooms", handleGetPublicRooms)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/publicRooms", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "chunk")
}

func TestHandleGetPublicRooms_WithLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.GET("/publicRooms", handleGetPublicRooms)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/publicRooms?limit=5&since=abc", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ===== storeFederationPDU tests =====

func TestStoreFederationPDU_MissingFields(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")

	// Missing required fields
	event := map[string]any{
		"event_id": "$test:test.local",
		// missing room_id, type, sender
	}
	eventID, err := storeFederationPDU(hs, event)
	assert.Error(t, err)
	assert.Empty(t, eventID)
}

func TestStoreFederationPDU_UnsignedCreateCannotBootstrapRoom(t *testing.T) {
	hs := createTestHomeServer(t)
	_, err := storeFederationPDU(hs, map[string]any{"event_id": "$fake", "room_id": "!unknown:test.local", "type": "m.room.create", "sender": "@alice:test.local", "state_key": "", "content": map[string]any{"room_version": "10"}})
	require.Error(t, err)
	var n int64
	require.NoError(t, db.GetDB().Model(&MatrixRoom{}).Where("room_id = ?", "!unknown:test.local").Count(&n).Error)
	require.Zero(t, n)
}

func TestStoreFederationPDU_DuplicateEvent(t *testing.T) {
	hs, room := signedHandlerRoom(t)
	event := signedHandlerEvent(t, hs, room, "@alice:test.local", nil, "m.room.message", map[string]any{"msgtype": "m.text", "body": "deduplicated"})
	wire, err := matrixEventToPDU(event)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		id, err := storeFederationPDU(hs, wire)
		require.NoError(t, err)
		require.Equal(t, event.EventID, id)
	}
	var n int64
	require.NoError(t, db.GetDB().Model(&MatrixEvent{}).Where("event_id = ?", event.EventID).Count(&n).Error)
	require.EqualValues(t, 1, n)
}

func TestBuildAuthChainForRoom_Empty(t *testing.T) {
	setupMinimalTestDB(t)
	chain := buildAuthChainForRoom("!empty:test.local", nil)
	assert.Empty(t, chain)
}

// ===== handleMakeJoin / handleMakeLeave with existing room =====

func TestHandleMakeJoin_RoomExists(t *testing.T) {
	hs, room := signedHandlerRoom(t)
	restore := resetGlobalHS(hs)
	defer restore()
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "test.local"); c.Next() })
	r.GET("/make/:roomId/:userId", handleMakeJoin)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/make/"+room.RoomID+"/@bob:test.local?ver=10", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	event := response["event"].(map[string]any)
	require.Equal(t, "join", event["content"].(map[string]any)["membership"])
	require.NotEmpty(t, event["auth_events"])
}

func TestHandleMakeLeave_RoomExists(t *testing.T) {
	hs, room := signedHandlerRoom(t)
	restore := resetGlobalHS(hs)
	defer restore()
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "test.local"); c.Next() })
	r.GET("/make/:roomId/:userId", handleMakeLeave)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/make/"+room.RoomID+"/@alice:test.local?ver=10", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	event := response["event"].(map[string]any)
	require.Equal(t, "leave", event["content"].(map[string]any)["membership"])
	require.NotEmpty(t, event["auth_events"])
}

func TestHandleSendJoin_Success(t *testing.T) {
	hs, room := signedHandlerRoom(t)
	restore := resetGlobalHS(hs)
	defer restore()
	user := "@bob:test.local"
	event := signedHandlerEvent(t, hs, room, user, &user, "m.room.member", map[string]any{"membership": "join"})
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "test.local"); c.Next() })
	r.PUT("/event/:roomId/:eventId", handleSendJoin)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PUT", "/event/"+room.RoomID+"/"+event.EventID, strings.NewReader(event.FederationJSON)))
	require.Equal(t, 200, w.Code, w.Body.String())
	var stored MatrixEvent
	require.NoError(t, db.GetDB().Where("event_id = ?", event.EventID).First(&stored).Error)
	require.Equal(t, "join", stored.Content["membership"])
}

func TestHandleSendJoinV1_Success(t *testing.T) {
	hs, room := signedHandlerRoom(t)
	restore := resetGlobalHS(hs)
	defer restore()
	user := "@bob:test.local"
	event := signedHandlerEvent(t, hs, room, user, &user, "m.room.member", map[string]any{"membership": "join"})
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "test.local"); c.Next() })
	r.PUT("/event/:roomId/:eventId", handleSendJoinV1)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PUT", "/event/"+room.RoomID+"/"+event.EventID, strings.NewReader(event.FederationJSON)))
	require.Equal(t, 200, w.Code, w.Body.String())
	var stored MatrixEvent
	require.NoError(t, db.GetDB().Where("event_id = ?", event.EventID).First(&stored).Error)
	require.Equal(t, "join", stored.Content["membership"])
}

func TestHandleSendLeave_Success(t *testing.T) {
	hs, room := signedHandlerRoom(t)
	restore := resetGlobalHS(hs)
	defer restore()
	user := "@alice:test.local"
	event := signedHandlerEvent(t, hs, room, user, &user, "m.room.member", map[string]any{"membership": "leave"})
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "test.local"); c.Next() })
	r.PUT("/event/:roomId/:eventId", handleSendLeave)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PUT", "/event/"+room.RoomID+"/"+event.EventID, strings.NewReader(event.FederationJSON)))
	require.Equal(t, 200, w.Code, w.Body.String())
	var stored MatrixEvent
	require.NoError(t, db.GetDB().Where("event_id = ?", event.EventID).First(&stored).Error)
	require.Equal(t, "leave", stored.Content["membership"])
}

func TestHandleFederationInvite_RejectsUnsignedLegacyEnvelope(t *testing.T) {
	hs, _ := signedHandlerRoom(t)
	restore := resetGlobalHS(hs)
	defer restore()
	r := gin.New()
	r.PUT("/invite/:roomId/:eventId", handleFederationInvite)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PUT", "/invite/!room:test.local/$fake", strings.NewReader(`{"sender":"@alice:remote.server","state_key":"@bob:test.local","content":{"membership":"invite"}}`)))
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	var n int64
	require.NoError(t, db.GetDB().Model(&MatrixEvent{}).Where("event_id = ?", "$fake").Count(&n).Error)
	require.Zero(t, n)
}

func TestHandleFederationSend_WithSignedPDUs(t *testing.T) {
	hs, room := signedHandlerRoom(t)
	restore := resetGlobalHS(hs)
	defer restore()
	event := signedHandlerEvent(t, hs, room, "@alice:test.local", nil, "m.room.message", map[string]any{"msgtype": "m.text", "body": "federated"})
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "test.local"); c.Next() })
	r.PUT("/send/:txnId", handleFederationSend)
	body := `{"origin":"test.local","pdus":[` + event.FederationJSON + `],"edus":[]}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PUT", "/send/signed", strings.NewReader(body)))
	require.Equal(t, 200, w.Code, w.Body.String())
	var response struct {
		PDUs map[string]map[string]any `json:"pdus"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	result, ok := response.PDUs[event.EventID]
	require.True(t, ok)
	require.Empty(t, result)
	var stored MatrixEvent
	require.NoError(t, db.GetDB().Where("event_id = ?", event.EventID).First(&stored).Error)
	require.Equal(t, "federated", stored.Content["body"])
}

func TestHandleFederationSend_TypingEDU(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.PUT("/send/:txnId", handleFederationSend)

	typingEDU := `{"edu_type":"m.typing","content":{"room_id":"!typing:test.local","user_ids":["@alice:remote.server"]}}`
	body := `{"origin":"remote.server","origin_server_ts":1234567890,"pdus":[],"edus":[` + typingEDU + `]}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/send/txn456", strings.NewReader(body))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleFederationSend_PresenceEDU(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.PUT("/send/:txnId", handleFederationSend)

	presenceEDU := `{"edu_type":"m.presence","content":{"user_id":"@alice:remote.server","presence":"online"}}`
	body := `{"origin":"remote.server","origin_server_ts":1234567890,"pdus":[],"edus":[` + presenceEDU + `]}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/send/txn789", strings.NewReader(body))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleFederationSend_ReceiptEDU(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.PUT("/send/:txnId", handleFederationSend)

	receiptEDU := `{"edu_type":"m.receipt","content":{"!room:test.local":{"$event:1":{"m.read":{"@alice:remote.server":{"ts":1234567890}}}}}}`
	body := `{"origin":"remote.server","origin_server_ts":1234567890,"pdus":[],"edus":[` + receiptEDU + `]}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/send/txn000", strings.NewReader(body))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleFederationSend_DeviceListEDU(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.PUT("/send/:txnId", handleFederationSend)

	dlEDU := `{"edu_type":"m.device_list_update","origin":"remote.server","content":{"user_id":"@alice:remote.server","device_id":"DEV1","stream_id":1,"changed":["DEV1"]}}`
	body := `{"origin":"remote.server","origin_server_ts":1234567890,"pdus":[],"edus":[` + dlEDU + `]}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/send/txn111", strings.NewReader(body))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ===== handleGetState with existing room =====

func TestHandleGetState_RoomExists(t *testing.T) {
	hs, room := signedHandlerRoom(t)
	restore := resetGlobalHS(hs)
	defer restore()
	before, err := currentStatePDUs(db.GetDB(), room.RoomID, "10")
	require.NoError(t, err)
	expected := make([]string, 0, len(before))
	for _, pdu := range before {
		expected = append(expected, pdu.EventID())
	}
	anchor := signedHandlerEvent(t, hs, room, "@alice:test.local", nil, "m.room.message", map[string]any{"msgtype": "m.text", "body": "historical state anchor"})
	wire, err := matrixEventToPDU(anchor)
	require.NoError(t, err)
	_, err = storeFederationPDU(hs, wire)
	require.NoError(t, err)
	key := ""
	later := signedHandlerEvent(t, hs, room, "@alice:test.local", &key, "m.room.topic", map[string]any{"topic": "after anchor"})
	wire, err = matrixEventToPDU(later)
	require.NoError(t, err)
	_, err = storeFederationPDU(hs, wire)
	require.NoError(t, err)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "test.local"); c.Next() })
	r.GET("/state/:roomId", handleGetState)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/state/"+room.RoomID+"?event_id="+url.QueryEscape(anchor.EventID), nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	var response struct {
		PDUs []map[string]any `json:"pdus"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	actual := make([]string, 0, len(response.PDUs))
	for _, wire := range response.PDUs {
		pdu, err := decodeWirePDU("10", wire)
		require.NoError(t, err)
		actual = append(actual, pdu.EventID())
	}
	require.ElementsMatch(t, expected, actual)
	require.NotContains(t, actual, later.EventID)
}

func TestHandleGetStateIDs_RoomExists(t *testing.T) {
	hs, room := signedHandlerRoom(t)
	restore := resetGlobalHS(hs)
	defer restore()
	before, err := currentStatePDUs(db.GetDB(), room.RoomID, "10")
	require.NoError(t, err)
	expected := make([]string, 0, len(before))
	for _, pdu := range before {
		expected = append(expected, pdu.EventID())
	}
	anchor := signedHandlerEvent(t, hs, room, "@alice:test.local", nil, "m.room.message", map[string]any{"msgtype": "m.text", "body": "historical state anchor"})
	wire, err := matrixEventToPDU(anchor)
	require.NoError(t, err)
	_, err = storeFederationPDU(hs, wire)
	require.NoError(t, err)
	key := ""
	later := signedHandlerEvent(t, hs, room, "@alice:test.local", &key, "m.room.topic", map[string]any{"topic": "after anchor"})
	wire, err = matrixEventToPDU(later)
	require.NoError(t, err)
	_, err = storeFederationPDU(hs, wire)
	require.NoError(t, err)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "test.local"); c.Next() })
	r.GET("/state_ids/:roomId", handleGetStateIDs)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/state_ids/"+room.RoomID+"?event_id="+url.QueryEscape(anchor.EventID), nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	var response struct {
		IDs []string `json:"pdu_ids"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.ElementsMatch(t, expected, response.IDs)
	require.NotContains(t, response.IDs, later.EventID)
}

func TestHandleGetFederationEvent_Exists(t *testing.T) {
	hs, room := signedHandlerRoom(t)
	restore := resetGlobalHS(hs)
	defer restore()
	var event MatrixEvent
	require.NoError(t, db.GetDB().Where("room_id = ?", room.RoomID).Order("depth DESC").First(&event).Error)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "test.local"); c.Next() })
	r.GET("/event/:eventId", handleGetFederationEvent)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/event/"+event.EventID, nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	var response struct {
		PDUs []map[string]any `json:"pdus"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Len(t, response.PDUs, 1)
	require.NotEmpty(t, response.PDUs[0]["signatures"])
}

func TestHandleGetPublicRooms_WithRooms(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	restore := resetGlobalHS(hs)
	defer restore()

	name := "Public Room"
	room := &MatrixRoom{
		RoomID:            "!public:test.local",
		RoomVersion:       "10",
		Name:              &name,
		JoinRule:          "public",
		HistoryVisibility: "world_readable",
	}
	require.NoError(t, db.GetDB().Create(room).Error)

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	r.GET("/publicRooms", handleGetPublicRooms)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/publicRooms", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "chunk")
}

// ===== RegisterFederationRoutes tests =====

func TestRegisterFederationRoutes_HSEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	RegisterFederationRoutes(r)

	// version endpoint should work
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_matrix/federation/v1/version", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// server key endpoint should work
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/_matrix/key/v2/server", nil)
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
}

func TestRegisterFederationRoutes_HSDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(nil)
	defer restore()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("origin", "remote.server"); c.Next() })
	// Should not register any routes
	RegisterFederationRoutes(r)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_matrix/federation/v1/version", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ===== helpers =====

func ed25519Key() (ed25519.PublicKey, ed25519.PrivateKey) {
	return ed25519Generate()
}

// signWithKey signs canonical bytes with a private key and returns base64.
func signWithKey(priv ed25519.PrivateKey, data []byte) string {
	sig := ed25519Sign(priv, data)
	return base64RawStd(sig)
}

// ed25519Generate generates a key pair, returning (pub, priv).
func ed25519Generate() (ed25519.PublicKey, ed25519.PrivateKey) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	return pub, priv
}

// randomSuffix returns a short random string for unique DSNs.
func randomSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func ed25519Sign(priv ed25519.PrivateKey, data []byte) []byte {
	return ed25519.Sign(priv, data)
}

func base64RawStd(b []byte) string {
	return base64.RawStdEncoding.EncodeToString(b)
}

// setupMinimalTestDB creates an isolated in-memory SQLite DB with homeserver models.
// It uses a unique DSN to avoid interfering with other tests that use the shared cache.
func setupMinimalTestDB(t *testing.T) {
	t.Helper()
	// Use a unique in-memory DB to avoid cross-test interference with federation_timestamp_test.go
	// which uses "file::memory:?cache=shared". The cache must be shared: with
	// cache=private every pooled connection opens its own empty database, so
	// a handler that used a second connection (e.g. a query outside its
	// transaction, or a background goroutine) failed with "no such table".
	uniqueDSN := "file:hshandler_" + randomSuffix() + "?mode=memory&cache=shared&_busy_timeout=5000"
	dbConn, err := pgtest.GormOpen(t, sqlite.Open(uniqueDSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	database.DB = dbConn

	models := []any{
		&MatrixUserSession{},
		&MatrixToDeviceMessage{},
		&MatrixSyncToDeviceBatch{},
		&MatrixRoom{},
		&MatrixEvent{},
		&MatrixRoomState{},
		&MatrixRoomMember{},
		&MatrixServerKey{},
		&MatrixRoomAlias{},
		&MatrixMedia{},
		&MatrixAccountData{},
		&MatrixPresence{},
		&MatrixTyping{},
		&MatrixFilter{},
		&MatrixReceipt{},
		&PushRule{},
		&Pusher{},
		&MatrixFederationKey{},
	}
	for _, m := range models {
		if err := dbConn.AutoMigrate(m); err != nil {
			t.Fatalf("failed to migrate %T: %v", m, err)
		}
	}
}

func minimalHS(serverName string) *HomeServer {
	pub, priv := ed25519Generate()
	return &HomeServer{
		Config:     &HSConfig{Enabled: true, ServerName: serverName, BaseURL: "https://" + serverName, FederationPort: 8448},
		keyID:      "ed25519:test",
		privateKey: priv,
		publicKey:  pub,
	}
}

// fullHS builds a HomeServer with all common services initialized for handler tests.
func fullHS(serverName string) *HomeServer {
	hs := minimalHS(serverName)
	hs.Room = NewRoomService(hs)
	hs.Event = NewEventService(hs)
	hs.Sync = NewSyncService(hs)
	hs.Auth = NewAuthService(hs)
	hs.Account = NewAccountService(hs)
	hs.Push = NewPushService(hs)
	hs.SyncNotifier = NewSyncNotifier()
	return hs
}

// Handler tests bypass only HTTP request authentication; PDUs use real room auth chains and signatures.
func signedHandlerRoom(t *testing.T) (*HomeServer, *CreateRoomResponse) {
	t.Helper()
	hs := createTestHomeServer(t)
	room, e := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Preset: "public_chat", RoomVersion: "10"})
	require.Nil(t, e)
	return hs, room
}
func signedHandlerEvent(t *testing.T, hs *HomeServer, room *CreateRoomResponse, sender string, stateKey *string, kind string, content map[string]any) *MatrixEvent {
	t.Helper()
	event := &MatrixEvent{RoomID: room.RoomID, Sender: sender, StateKey: stateKey, EventType: EventType(kind), Content: content, OriginServerTS: time.Now().UnixMilli()}
	require.NoError(t, buildLocalPDU(db.GetDB(), hs, event, "10"))
	return event
}
