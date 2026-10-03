package homeserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFederatedToDeviceUsesSignedStandardEDU(t *testing.T) {
	hs := createTestHomeServer(t)
	var seen directToDeviceEDU
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin, destination, keyID, signature, err := parseXMatrixAuth(r.Header.Get("Authorization"))
		require.NoError(t, err)
		require.Equal(t, hs.Config.ServerName, origin)
		require.True(t, verifyRequestSignature(r, origin, destination, keyID, signature, hs.publicKey))
		var txn FederationTransaction
		require.NoError(t, json.NewDecoder(r.Body).Decode(&txn))
		require.Len(t, txn.EDUs, 1)
		var edu struct {
			Type    string            `json:"edu_type"`
			Content directToDeviceEDU `json:"content"`
		}
		require.NoError(t, json.Unmarshal(txn.EDUs[0], &edu))
		require.Equal(t, "m.direct_to_device", edu.Type)
		seen = edu.Content
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pdus":{}}`))
	}))
	defer server.Close()
	client := NewFederationClient(hs)
	client.fedHTTPClient = server.Client()
	target := strings.TrimPrefix(server.URL, "https://")
	edu := directToDeviceEDU{Sender: "@alice:test.local", Type: "m.room.encrypted", MessageID: "stable-retry-id", Messages: map[string]map[string]map[string]any{"@bob:" + target: {"DEVICE": {"ciphertext": "opaque"}}}}
	require.NoError(t, sendDirectToDeviceEDU(client, target, edu))
	require.Equal(t, edu, seen)
}

func TestFederationDirectToDeviceOriginReplayAndLocalIsolation(t *testing.T) {
	hs := createTestHomeServer(t)
	setupMinimalTestDB(t)
	hs.Config.Enabled = true
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixToDeviceMessage{}, &MatrixToDeviceDelivery{}, &MatrixUserSession{}))
	for i, device := range []string{"ONE", "TWO"} {
		require.NoError(t, db.GetDB().Create(&MatrixUserSession{UserID: "@receiver:test.local", DeviceID: device, AccessToken: device + string(rune('a'+i))}).Error)
	}
	edu := directToDeviceEDU{Sender: "@alice:test.local", Type: "m.room.encrypted", MessageID: "inbound-dedup", Messages: map[string]map[string]map[string]any{
		"@receiver:test.local": {"*": {"ciphertext": "opaque"}}, "@foreign:other.test": {"DEVICE": {"ciphertext": "not-local"}},
	}}
	r := gin.New()
	RegisterFederationRoutes(r)
	raw, err := json.Marshal(map[string]any{"origin": hs.Config.ServerName, "pdus": []any{}, "edus": []any{map[string]any{"edu_type": "m.direct_to_device", "content": edu}}})
	require.NoError(t, err)
	for _, txn := range []string{"first", "different-transaction-replay"} {
		w := signedAuxRequest(t, hs, r, "PUT", "/_matrix/federation/v1/send/"+txn, string(raw))
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	var messages []MatrixToDeviceMessage
	require.NoError(t, db.GetDB().Where("sender = ?", edu.Sender).Find(&messages).Error)
	require.Len(t, messages, 2)
	for _, message := range messages {
		require.Equal(t, "@receiver:test.local", message.TargetUser)
		require.NotEqual(t, "*", message.TargetDevice)
	}
	require.NoError(t, db.GetDB().Where("sender = ?", edu.Sender).Delete(&MatrixToDeviceMessage{}).Error)
	require.NoError(t, deliverLocalToDevice(hs, hs.Config.ServerName, edu))
	var count int64
	require.NoError(t, db.GetDB().Model(&MatrixToDeviceMessage{}).Where("sender = ?", edu.Sender).Count(&count).Error)
	require.Zero(t, count, "replay must not re-enqueue after sync consumed inbox")
	edu.MessageID = "forged"
	edu.Sender = "@alice:other.test"
	require.Error(t, deliverLocalToDevice(hs, hs.Config.ServerName, edu))
}

func TestToDeviceSyncDoesNotDeleteConcurrentlyArrivingKeys(t *testing.T) {
	hs := createTestHomeServer(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixToDeviceMessage{}))
	user := "@snapshot:test.local"
	first := MatrixToDeviceMessage{Sender: "@sender:remote.test", TargetUser: user, TargetDevice: "DEVICE", EventType: "m.room.encrypted", Content: map[string]any{"ciphertext": "first"}}
	require.NoError(t, db.GetDB().Create(&first).Error)
	injected := false
	require.NoError(t, db.GetDB().Callback().Delete().Before("gorm:delete").Register("test:arriving-key", func(tx *gorm.DB) {
		if !injected && tx.Statement.Table == "matrix_to_device_messages" {
			injected = true
			next := MatrixToDeviceMessage{Sender: first.Sender, TargetUser: user, TargetDevice: "DEVICE", EventType: first.EventType, Content: map[string]any{"ciphertext": "arrived-during-sync"}}
			require.NoError(t, tx.Session(&gorm.Session{NewDB: true}).Create(&next).Error)
		}
	}))
	t.Cleanup(func() { _ = db.GetDB().Callback().Delete().Remove("test:arriving-key") })
	response, batch, err := hs.Sync.getToDeviceMessages(user, "DEVICE", "s1")
	require.NoError(t, err)
	require.Len(t, response.Events, 1)
	require.Equal(t, "first", response.Events[0].Content["ciphertext"])
	retry, sameBatch, err := hs.Sync.getToDeviceMessages(user, "DEVICE", "s1")
	require.NoError(t, err)
	require.Equal(t, batch, sameBatch)
	require.Equal(t, response, retry)
	next, nextBatch, err := hs.Sync.getToDeviceMessages(user, "DEVICE", "s1_d"+batch)
	require.NoError(t, err)
	require.NotEqual(t, batch, nextBatch)
	require.Len(t, next.Events, 1)
	require.Equal(t, "arrived-during-sync", next.Events[0].Content["ciphertext"])
}
