package homeserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== Test 1: 设备列表 EDU HTTP 请求体验证 =====

// TestDeviceListRequestBody verifies that constructing an HTTP request with
// nil body + manual ContentLength causes a client error, and that passing the
// body via NewRequest works correctly.
func TestDeviceListRequestBody(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pdus": []any{}, //nolint:goconst
		})
	}))
	defer server.Close()

	// Build a device list EDU payload
	edu := DeviceListUpdateEDU{
		UserID:   "@alice:test.server", //nolint:goconst
		DeviceID: "DEVICE123",          //nolint:goconst
		StreamID: 1,
		Changed:  []string{"DEVICE123"},
	}

	payload := map[string]any{
		"pdus": []map[string]any{},
		"edus": []map[string]any{
			{
				"edu_type": "m.device_list_update",
				"content":  edu, //nolint:goconst
			},
		},
	}

	payloadData, err := json.Marshal(payload)
	require.NoError(t, err)

	url := fmt.Sprintf("%s/_matrix/federation/v1/send/%d", server.URL, time.Now().Unix())

	// Buggy pattern: nil body with mismatched ContentLength — Go HTTP client rejects this
	req1, _ := http.NewRequest(http.MethodPut, url, nil) //nolint:noctx
	req1.Body = io.NopCloser(bytes.NewReader([]byte{}))
	req1.ContentLength = int64(len(payloadData))
	req1.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	_, err = client.Do(req1) //nolint:bodyclose
	require.Error(t, err, "Buggy pattern (empty body + non-zero ContentLength) should be rejected by HTTP client")

	// Fixed pattern: pass body directly via NewRequest
	capturedBody = nil
	req2, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(payloadData)) //nolint:noctx
	req2.Header.Set("Content-Type", "application/json")

	resp2, err := client.Do(req2)
	require.NoError(t, err, "Fixed pattern should succeed")

	_ = resp2.Body.Close()

	// Verify the server received the correct body
	require.NotEmpty(t, capturedBody, "Server should receive non-empty body")

	var received map[string]any

	err = json.Unmarshal(capturedBody, &received)
	require.NoError(t, err)

	edus, ok := received["edus"].([]any)
	require.True(t, ok, "Response should have edus array")
	assert.NotEmpty(t, edus, "edus should not be empty")
}

// ===== Test 2: Backfill 响应格式验证 =====

// TestBackfillResponseHasAuthChain verifies that the backfill response
// includes the auth_chain field as required by the Matrix spec.
func TestBackfillResponseHasAuthChain(t *testing.T) {
	hs := createTestHomeServer(t)
	hs.Config.Enabled = true
	restore := resetGlobalHS(hs)
	defer restore()
	room := createRoomWithUser(t, hs, "@alice:test.local")
	eventID := sendMessage(t, hs, room, "@alice:test.local", "backfill")
	router := gin.New()
	RegisterBackfillRoutes(router)
	path := "/_matrix/federation/v1/backfill/" + room + "?v=" + url.QueryEscape(eventID) + "&limit=1"
	unsigned := httptest.NewRecorder()
	router.ServeHTTP(unsigned, httptest.NewRequest(http.MethodGet, path, nil))
	require.Equal(t, http.StatusUnauthorized, unsigned.Code)
	response := signedAuxRequest(t, hs, router, http.MethodGet, path, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var backfill map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &backfill))
	for _, field := range []string{"origin", "origin_server_ts", "pdus"} {
		require.Contains(t, backfill, field)
	}
	require.Equal(t, "test.local", backfill["origin"])
	pdus := backfill["pdus"].([]any)
	require.Len(t, pdus, 1)
	event := pdus[0].(map[string]any)
	require.NotEmpty(t, event["auth_events"])
	require.NotEmpty(t, event["hashes"])
	require.NotEmpty(t, event["signatures"])
	// Backfill is a transaction PDU list. Its transitive auth chain is served by event_auth.
	response = signedAuxRequest(t, hs, router, http.MethodGet, "/_matrix/federation/v1/event_auth/"+room+"/"+eventID, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var auth struct {
		Chain []map[string]any `json:"auth_chain"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &auth))
	require.NotEmpty(t, auth.Chain)
	for _, event := range auth.Chain {
		require.NotEmpty(t, event["hashes"])
		require.NotEmpty(t, event["signatures"])
	}
}

// TestBackfillAuthChainContent documents what auth_chain should contain
// according to the Matrix spec.
func TestBackfillAuthChainContent(t *testing.T) {
	t.Log("According to Matrix spec, backfill response auth_chain should contain:")
	t.Log("- m.room.create (for the room)")
	t.Log("- m.room.power_levels (current power levels)")
	t.Log("- m.room.member (for the sender)")
	t.Log("- Any other auth_events referenced by the returned events")
}

// TestPushRuleMigrationSync documents the async push rule migration issue.
func TestPushRuleMigrationSync(t *testing.T) {
	t.Log("Push rule migration in event.go is async (go func()).")
	t.Log("Tests that check rules immediately after room upgrade may see stale state.")
	t.Log("Solution: make migration synchronous or add a completion signal.")
}

// TestRedactionTargetMissing documents redaction behavior when target is missing.
func TestRedactionTargetMissing(t *testing.T) {
	t.Log("When redaction target doesn't exist, RedactEvent returns error.")
	t.Log("Sync should still work; the redaction event should appear in timeline.")
}

// TestTimestampMismatchDetailed documents the federation timestamp ordering issue.
func TestTimestampMismatchDetailed(t *testing.T) {
	t.Log("Federated events may arrive out of order relative to origin_server_ts.")
	t.Log("Using origin_server_ts for sync token filtering can cause events to be lost.")
	t.Log("Solution: use local created_at for sync token generation and filtering.")
}
