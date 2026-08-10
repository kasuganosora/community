package matrix

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// performWebhookRequest is a helper that invokes a gin handler with the
// given method, path, auth header, query and JSON body. It extracts the
// last path segment as a "param" so that handlers using c.Param() for
// the resource ID (txnId, userId, roomAlias) receive the value.
func performWebhookRequest(
	t *testing.T,
	handler gin.HandlerFunc,
	method, path, authHeader, query, body string,
) *httptest.ResponseRecorder {
	t.Helper()

	fullPath := path + query
	req := httptest.NewRequest(method, fullPath, bytes.NewBufferString(body))
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{}

	// Extract the last path segment as the resource id for common param
	// names used by the webhook handlers (txnId, userId, roomAlias).
	trimmed := strings.TrimRight(req.URL.Path, "/")
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		lastSegment := trimmed[idx+1:]
		// Try each known param key — gin.Param lookup is by exact key match.
		for _, key := range []string{"txnId", "userId", "roomAlias"} {
			c.Params = append(c.Params, gin.Param{Key: key, Value: lastSegment})
		}
	}

	handler(c)
	return w
}

// ====================================================================
// extractBearerToken (additional cases beyond webhook_test.go)
// ====================================================================

func TestExtractBearerToken_AdditionalCases(t *testing.T) {
	// Exactly "Bearer " (7 chars) — not >7, so returned as-is.
	assert.Equal(t, "Bearer ", extractBearerToken("Bearer "))
	// Longer than 7 with Bearer prefix.
	assert.Equal(t, "tok", extractBearerToken("Bearer tok"))
}

// ====================================================================
// WebhookHandler.HandleTransaction
// ====================================================================

func newTestWebhookHandler(t *testing.T, bridge *Bridge, hsToken string) *WebhookHandler {
	t.Helper()
	c := NewASClientWithConfig(&Config{
		HomeserverURL: "http://unused",
		ASToken:       "as",
		HSToken:       hsToken,
	})
	return &WebhookHandler{
		bridge:   bridge,
		asClient: c,
	}
}

func TestWebhookHandler_HandleTransaction_InvalidToken(t *testing.T) {
	h := newTestWebhookHandler(t, nil, "correct-hs-token")

	w := performWebhookRequest(t, h.HandleTransaction,
		"PUT", "/_matrix/app/v1/transactions/txn1", "Bearer wrong", "", `{"events":[]}`)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "M_UNAUTHORIZED", resp["errcode"])
}

func TestWebhookHandler_HandleTransaction_MissingTxnID(t *testing.T) {
	h := newTestWebhookHandler(t, nil, "hs-token")

	// Manually set txnID param to empty by calling the handler directly.
	req := httptest.NewRequest("PUT", "/_matrix/app/v1/transactions/", bytes.NewBufferString(`{}`))
	req.Header.Set("Authorization", "Bearer hs-token")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "txnId", Value: ""}}

	h.HandleTransaction(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "M_BAD_JSON", resp["errcode"])
}

func TestWebhookHandler_HandleTransaction_InvalidJSON(t *testing.T) {
	h := newTestWebhookHandler(t, nil, "hs-token")

	// Mark the txn as not-yet-processed by clearing the cache.
	resetTxnCache()

	req := httptest.NewRequest("PUT", "/_matrix/app/v1/transactions/txn-bad", bytes.NewBufferString("not json"))
	req.Header.Set("Authorization", "Bearer hs-token")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "txnId", Value: "txn-bad"}}

	h.HandleTransaction(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWebhookHandler_HandleTransaction_SuccessNoBridge(t *testing.T) {
	h := newTestWebhookHandler(t, nil, "hs-token")
	resetTxnCache()

	txnBody := `{"events":[{"type":"m.room.message","content":{"msgtype":"m.text","body":"hi"}}]}`
	w := performWebhookRequest(t, h.HandleTransaction,
		"PUT", "/_matrix/app/v1/transactions/txn-ok", "Bearer hs-token", "", txnBody)

	assert.Equal(t, http.StatusOK, w.Code)
	// Success returns empty object.
	assert.Equal(t, "{}", w.Body.String())
}

func TestWebhookHandler_HandleTransaction_DuplicateTxn(t *testing.T) {
	h := newTestWebhookHandler(t, nil, "hs-token")
	resetTxnCache()

	txnBody := `{"events":[]}`

	// First call succeeds.
	w1 := performWebhookRequest(t, h.HandleTransaction,
		"PUT", "/_matrix/app/v1/transactions/txn-dup", "Bearer hs-token", "", txnBody)
	assert.Equal(t, http.StatusOK, w1.Code)

	// Second call with same txnID should be skipped (still 200).
	w2 := performWebhookRequest(t, h.HandleTransaction,
		"PUT", "/_matrix/app/v1/transactions/txn-dup", "Bearer hs-token", "", txnBody)
	assert.Equal(t, http.StatusOK, w2.Code)
}

func TestWebhookHandler_HandleTransaction_TokenInQuery(t *testing.T) {
	h := newTestWebhookHandler(t, nil, "hs-token")
	resetTxnCache()

	// access_token in query instead of Authorization header.
	w := performWebhookRequest(t, h.HandleTransaction,
		"PUT", "/_matrix/app/v1/transactions/txn-q", "", "?access_token=hs-token", `{"events":[]}`)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ====================================================================
// WebhookHandler.HandleUserQuery
// ====================================================================

func TestWebhookHandler_HandleUserQuery_InvalidToken(t *testing.T) {
	h := newTestWebhookHandler(t, nil, "hs-token")

	w := performWebhookRequest(t, h.HandleUserQuery,
		"GET", "/_matrix/app/v1/users/@test:example.com", "Bearer wrong", "", "")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWebhookHandler_HandleUserQuery_MissingUserID(t *testing.T) {
	h := newTestWebhookHandler(t, nil, "hs-token")

	req := httptest.NewRequest("GET", "/_matrix/app/v1/users/", nil)
	req.Header.Set("Authorization", "Bearer hs-token")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "userId", Value: ""}}

	h.HandleUserQuery(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWebhookHandler_HandleUserQuery_InvalidFormat(t *testing.T) {
	h := newTestWebhookHandler(t, nil, "hs-token")

	req := httptest.NewRequest("GET", "/_matrix/app/v1/users/invalid", nil)
	req.Header.Set("Authorization", "Bearer hs-token")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "userId", Value: "invalid"}}

	h.HandleUserQuery(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "M_INVALID_PARAM", resp["errcode"])
}

func TestWebhookHandler_HandleUserQuery_InNamespace(t *testing.T) {
	h := newTestWebhookHandler(t, nil, "hs-token")
	setupTestGlobalConfig("http://unused")

	req := httptest.NewRequest("GET", "/_matrix/app/v1/users/@_c_alice:example.com", nil)
	req.Header.Set("Authorization", "Bearer hs-token")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "userId", Value: "@_c_alice:example.com"}}

	h.HandleUserQuery(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestWebhookHandler_HandleUserQuery_NotInNamespace(t *testing.T) {
	h := newTestWebhookHandler(t, nil, "hs-token")
	setupTestGlobalConfig("http://unused")

	req := httptest.NewRequest("GET", "/_matrix/app/v1/users/@bob:example.com", nil)
	req.Header.Set("Authorization", "Bearer hs-token")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "userId", Value: "@bob:example.com"}}

	h.HandleUserQuery(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ====================================================================
// WebhookHandler.HandleRoomQuery
// ====================================================================

func TestWebhookHandler_HandleRoomQuery_InvalidToken(t *testing.T) {
	h := newTestWebhookHandler(t, nil, "hs-token")

	w := performWebhookRequest(t, h.HandleRoomQuery,
		"GET", "/_matrix/app/v1/rooms/_c_test", "Bearer wrong", "", "")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWebhookHandler_HandleRoomQuery_MissingAlias(t *testing.T) {
	h := newTestWebhookHandler(t, nil, "hs-token")

	req := httptest.NewRequest("GET", "/_matrix/app/v1/rooms/", nil)
	req.Header.Set("Authorization", "Bearer hs-token")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "roomAlias", Value: ""}}

	h.HandleRoomQuery(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWebhookHandler_HandleRoomQuery_InNamespace(t *testing.T) {
	h := newTestWebhookHandler(t, nil, "hs-token")
	setupTestGlobalConfig("http://unused")

	req := httptest.NewRequest("GET", "/_matrix/app/v1/rooms/_c_myroom", nil)
	req.Header.Set("Authorization", "Bearer hs-token")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "roomAlias", Value: "_c_myroom"}}

	h.HandleRoomQuery(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestWebhookHandler_HandleRoomQuery_NotInNamespace(t *testing.T) {
	h := newTestWebhookHandler(t, nil, "hs-token")
	setupTestGlobalConfig("http://unused")

	req := httptest.NewRequest("GET", "/_matrix/app/v1/rooms/otherroom", nil)
	req.Header.Set("Authorization", "Bearer hs-token")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Params = gin.Params{{Key: "roomAlias", Value: "otherroom"}}

	h.HandleRoomQuery(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ====================================================================
// WebhookHandler.HandleHealthCheck
// ====================================================================

func TestWebhookHandler_HandleHealthCheck_Disabled(t *testing.T) {
	h := newTestWebhookHandler(t, nil, "hs-token")
	// Set GlobalConfig to disabled state.
	GlobalConfig = Config{Enabled: false}

	req := httptest.NewRequest("GET", "/_matrix/health", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	h.HandleHealthCheck(c)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "disabled", resp["status"])
}

func TestWebhookHandler_HandleHealthCheck_NotRunning(t *testing.T) {
	h := newTestWebhookHandler(t, nil, "hs-token")
	setupTestGlobalConfig("http://unused")
	// bridge is nil, so it should report stopped.

	req := httptest.NewRequest("GET", "/_matrix/health", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	h.HandleHealthCheck(c)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "stopped", resp["status"])
}

func TestWebhookHandler_HandleHealthCheck_Running(t *testing.T) {
	// Build a bridge that is started (atomic flag) without a real HS.
	b := &Bridge{}
	b.started.Store(true)

	h := &WebhookHandler{
		bridge:   b,
		asClient: NewASClientWithConfig(&Config{HSToken: "hs"}),
	}
	setupTestGlobalConfig("http://unused")

	req := httptest.NewRequest("GET", "/_matrix/health", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	h.HandleHealthCheck(c)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "running", resp["status"])
}

// resetTxnCache clears the global transaction idempotency cache so each
// test starts clean.
func resetTxnCache() {
	processedTxnsMu.Lock()
	processedTxns = make(map[string]time.Time)
	// Force the cleanup sweep to run on next isProcessedTxn call.
	lastTxnCleanup = time.Time{}
	processedTxnsMu.Unlock()
}
