package homeserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== WellKnownService unit tests =====

// newTestWellKnownHS builds a minimal HomeServer for WellKnown tests without DB.
func newTestWellKnownHS(serverName string, fedPort int, enabled bool) *HomeServer {
	return &HomeServer{
		Config: &HSConfig{
			Enabled:        enabled,
			ServerName:     serverName,
			BaseURL:        "https://" + serverName,
			FederationPort: fedPort,
		},
	}
}

func TestWellKnownService_GetServerInfo_DefaultPort443(t *testing.T) {
	hs := newTestWellKnownHS("matrix.example.com", 443, true)
	ws := NewWellKnownService(hs)
	info := ws.GetServerInfo()

	require.NotNil(t, info)
	// When port is 443, no port suffix should be appended
	assert.Equal(t, "matrix.example.com", info.ServerName)
}

func TestWellKnownService_GetServerInfo_NonDefaultPort(t *testing.T) {
	hs := newTestWellKnownHS("matrix.example.com", 8448, true)
	ws := NewWellKnownService(hs)
	info := ws.GetServerInfo()

	require.NotNil(t, info)
	// When port != 443, port suffix should be appended
	assert.Equal(t, "matrix.example.com:8448", info.ServerName)
}

func TestWellKnownService_GetClientInfo(t *testing.T) {
	hs := newTestWellKnownHS("matrix.example.com", 8448, true)
	ws := NewWellKnownService(hs)
	info := ws.GetClientInfo()

	require.NotNil(t, info)
	assert.Equal(t, "https://matrix.example.com", info.HomeServer.BaseURL)
	require.NotNil(t, info.IdentityServer)
	assert.Equal(t, "https://matrix.example.com", info.IdentityServer.BaseURL)
}

func TestWellKnownService_NewWellKnownService(t *testing.T) {
	hs := newTestWellKnownHS("srv.test", 8448, true)
	ws := NewWellKnownService(hs)
	assert.NotNil(t, ws)
	assert.Equal(t, hs, ws.hs)
}

// ===== HTTP handler tests =====

// withWellKnownRouter creates a gin router with the well-known handlers bound to a configured HS.
func withWellKnownRouter(hs *HomeServer) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Bind handlers directly so we don't depend on GlobalHS / RegisterWellKnownRoutes side effects
	if hs != nil && hs.Config.Enabled {
		r.GET("/.well-known/matrix/server", func(c *gin.Context) {
			info := hs.WellKnown.GetServerInfo()
			c.JSON(http.StatusOK, info)
		})
		r.GET("/.well-known/matrix/client", func(c *gin.Context) {
			info := hs.WellKnown.GetClientInfo()
			c.JSON(http.StatusOK, info)
		})
	}
	return r
}

func TestHandleWellKnownServer_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hs := newTestWellKnownHS("hs.test", 8448, true)
	hs.WellKnown = NewWellKnownService(hs)

	r := withWellKnownRouter(hs)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/matrix/server", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var info ServerInfo
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &info))
	assert.Equal(t, "hs.test:8448", info.ServerName)
}

func TestHandleWellKnownClient_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hs := newTestWellKnownHS("hs.test", 443, true)
	hs.WellKnown = NewWellKnownService(hs)

	r := withWellKnownRouter(hs)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/matrix/client", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var info ClientInfo
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &info))
	assert.Equal(t, "https://hs.test", info.HomeServer.BaseURL)
	assert.NotNil(t, info.IdentityServer)
}

// TestRegisterWellKnownRoutes_Disabled verifies that when the global HS is nil,
// RegisterWellKnownRoutes is a no-op (does not register any routes).
func TestRegisterWellKnownRoutes_GlobalHSNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(nil)
	defer restore()

	r := gin.New()
	// This should not panic and should not register routes
	RegisterWellKnownRoutes(r)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/matrix/server", nil)
	r.ServeHTTP(w, req)

	// No route registered → 404
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestRegisterWellKnownRoutes_GlobalHSEnabled verifies that when the global HS
// is enabled, RegisterWellKnownRoutes registers handlers that respond correctly.
func TestRegisterWellKnownRoutes_GlobalHSEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hs := newTestWellKnownHS("global.test", 8448, true)
	hs.WellKnown = NewWellKnownService(hs)
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	RegisterWellKnownRoutes(r)

	// server endpoint
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/.well-known/matrix/server", nil)
	r.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusOK, w1.Code)

	var sInfo ServerInfo
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &sInfo))
	assert.Equal(t, "global.test:8448", sInfo.ServerName)

	// client endpoint
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/.well-known/matrix/client", nil)
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
}

// TestHandleWellKnownServer_GlobalHSNil verifies the handler returns 404 when HS is nil.
func TestHandleWellKnownServer_GlobalHSNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(nil)
	defer restore()

	r := gin.New()
	r.GET("/.well-known/matrix/server", handleWellKnownServer)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/matrix/server", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "M_NOT_FOUND", body["errcode"])
}

// TestHandleWellKnownClient_GlobalHSNil verifies the handler returns 404 when HS is nil.
func TestHandleWellKnownClient_GlobalHSNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(nil)
	defer restore()

	r := gin.New()
	r.GET("/.well-known/matrix/client", handleWellKnownClient)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/matrix/client", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ===== JSON serialization tests for well-known structs =====

func TestServerInfo_JSON(t *testing.T) {
	si := &ServerInfo{ServerName: "server.test:8448"}
	data, err := json.Marshal(si)
	require.NoError(t, err)

	var parsed map[string]string
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Equal(t, "server.test:8448", parsed["m.server"])
}

func TestClientInfo_JSON_OmitsNilIdentityServer(t *testing.T) {
	ci := &ClientInfo{
		HomeServer:     HomeServerInfo{BaseURL: "https://a.test"},
		IdentityServer: nil,
	}
	data, err := json.Marshal(ci)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))
	// m.identity_server should be omitted when nil (omitempty)
	_, hasIS := parsed["m.identity_server"]
	assert.False(t, hasIS, "m.identity_server should be omitted when nil")
	assert.Equal(t, "https://a.test", parsed["m.homeserver"].(map[string]any)["base_url"])
}
