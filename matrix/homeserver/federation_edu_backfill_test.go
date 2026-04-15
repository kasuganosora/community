package homeserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/tests/testutil"
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
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"pdus": []interface{}{},
		})
	}))
	defer server.Close()

	// Build a device list EDU payload
	edu := DeviceListUpdateEDU{
		UserID:   "@alice:test.server",
		DeviceID: "DEVICE123",
		StreamID: 1,
		Changed:  []string{"DEVICE123"},
	}

	payload := map[string]interface{}{
		"pdus": []map[string]interface{}{},
		"edus": []map[string]interface{}{
			{
				"edu_type": "m.device_list_update",
				"content":  edu,
			},
		},
	}

	payloadData, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/_matrix/federation/v1/send/%d", server.URL, time.Now().Unix())

	// Buggy pattern: nil body with mismatched ContentLength — Go HTTP client rejects this
	req1, _ := http.NewRequest("PUT", url, nil)
	req1.Body = io.NopCloser(bytes.NewReader([]byte{}))
	req1.ContentLength = int64(len(payloadData))
	req1.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	_, err := client.Do(req1)
	assert.Error(t, err, "Buggy pattern (empty body + non-zero ContentLength) should be rejected by HTTP client")

	// Fixed pattern: pass body directly via NewRequest
	capturedBody = nil
	req2, _ := http.NewRequest("PUT", url, bytes.NewReader(payloadData))
	req2.Header.Set("Content-Type", "application/json")

	resp2, err := client.Do(req2)
	require.NoError(t, err, "Fixed pattern should succeed")
		_ = resp2.Body.Close()

	// Verify the server received the correct body
	require.NotEmpty(t, capturedBody, "Server should receive non-empty body")

	var received map[string]interface{}
	err = json.Unmarshal(capturedBody, &received)
	require.NoError(t, err)

	edus, ok := received["edus"].([]interface{})
	require.True(t, ok, "Response should have edus array")
	assert.Greater(t, len(edus), 0, "edus should not be empty")
}

// ===== Test 2: Backfill 响应格式验证 =====

// TestBackfillResponseHasAuthChain verifies that the backfill response
// includes the auth_chain field as required by the Matrix spec.
func TestBackfillResponseHasAuthChain(t *testing.T) {
	// Set up isolated test DB with all Matrix models
	testDB, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)

	// Set global DB so db.GetDB() works throughout homeserver code
	savedDB := database.DB
	database.SetTestDB(testDB.GormDB)

	// Set SERVER_NAME to skip systemsettings.GetDomain() (avoids system_settings table dep)
	oldServerName := os.Getenv("SERVER_NAME")
		_ = os.Setenv("SERVER_NAME", "test.server")

	// Reset global homeserver state so InitHomeServer runs fresh
	savedHS := GlobalHS
	GlobalHS = nil
	initOnce = sync.Once{}
	defer func() {
		GlobalHS = savedHS
		initOnce = sync.Once{} // Reset so future calls can re-init if needed
		database.DB = savedDB
		_ = os.Setenv("SERVER_NAME", oldServerName)
	}()

	// AutoMigrate the Matrix models needed for backfill
	require.NoError(t, testDB.GormDB.AutoMigrate(
		&MatrixRoom{},
		&MatrixEvent{},
		&MatrixRoomState{},
		&MatrixRoomMember{},
		&MatrixFederationKey{},
		&MatrixServerKey{},
		&MatrixUserSession{},
		&MatrixToDeviceMessage{},
		&MatrixReceipt{},
		&MatrixAccountData{},
		&MatrixTyping{},
		&MatrixFilter{},
		&MatrixPresence{},
		&MatrixDeviceKey{},
		&MatrixOneTimeKey{},
		&MatrixCrossSigningKey{},
		&MatrixFederationTxn{},
		&MatrixKeyBackup{},
		&MatrixKeyBackupSession{},
		&MatrixKeyBackupRecovery{},
		&MatrixSSSSSecret{},
		&MatrixSSSSDefaultKey{},
		&EventRelation{},
		&Pusher{},
		&PushRule{},
		&AppService{},
		&MatrixRoomUpgrade{},
		&RoomKeyRequest{},
		&DeviceListChange{},
		&DeviceListStream{},
		&AsyncMediaInfo{},
		&PasswordResetToken{},
		&OpenIDToken{},
		&ThreePID{},
		&ThreePIDToken{},
		&MatrixRoomTag{},
		&MatrixRoomAlias{},
		&MatrixMedia{},
		&MatrixThumbnail{},
	))

	gin.SetMode(gin.TestMode)
	router := gin.New()

	// RegisterBackfillRoutes calls GetHomeServer() which triggers InitHomeServer()
	RegisterBackfillRoutes(router)
	require.NotNil(t, GlobalHS, "HomeServer should be initialized")

	// Test backfill endpoint — room won't exist so we expect 404
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/_matrix/federation/v1/backfill/!test:test.server?v=$event1&limit=10", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code, "Non-existent room should return 404")

	// Create a test room and event to verify full response format
	room := &MatrixRoom{
		RoomID:      "!testroom:test.server",
		RoomVersion: "10",
	}
	require.NoError(t, testDB.GormDB.Create(room).Error)

	event := &MatrixEvent{
		RoomID:         "!testroom:test.server",
		EventType:      EventTypeRoomMessage,
		Sender:         "@alice:test.server",
		OriginServerTS: time.Now().UnixMilli(),
		Content:        map[string]interface{}{"body": "hello", "msgtype": "m.text"},
	}
	require.NoError(t, testDB.GormDB.Create(event).Error)

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET",
		fmt.Sprintf("/_matrix/federation/v1/backfill/%s?v=%s&limit=10", room.RoomID, event.EventID),
		nil)
	router.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w2.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify all Matrix spec required fields are present
	for _, field := range []string{"origin", "origin_server_ts", "pdus", "auth_chain"} {
		_, exists := response[field]
		assert.True(t, exists, "Response should contain %q field", field)
	}

	assert.Equal(t, "test.server", response["origin"])
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
