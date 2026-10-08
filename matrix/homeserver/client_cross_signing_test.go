package homeserver

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func clientSigningDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.Account{}, &models.User{}, &models.UserSecretData{}, &MatrixCrossSigningKey{}, &MatrixDeviceKey{}))
	old := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(old) })
	for i, name := range []string{"alice", "bob"} {
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.Account{Id: uint64(i + 1), UserId: uint64(i + 1), Username: name}).Error)
	}
	return conn
}
func publicSigningKey(t *testing.T, user, usage string) (map[string]any, ed25519.PrivateKey, string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	encoded := base64.RawStdEncoding.EncodeToString(public)
	return map[string]any{"user_id": user, "usage": []any{usage}, "keys": map[string]any{"ed25519:" + encoded: encoded}}, private, "ed25519:" + encoded
}
func addTestSignature(t *testing.T, object map[string]any, user, id string, private ed25519.PrivateKey) {
	t.Helper()
	canonical, err := signedJSON(object)
	require.NoError(t, err)
	signatures, ok := object["signatures"].(map[string]any)
	if !ok {
		signatures = make(map[string]any)
		object["signatures"] = signatures
	}
	mine, ok := signatures[user].(map[string]any)
	if !ok {
		mine = make(map[string]any)
		signatures[user] = mine
	}
	mine[id] = base64.RawStdEncoding.EncodeToString(ed25519.Sign(private, canonical))
}
func signingBundle(t *testing.T, user string) (*signingUpload, ed25519.PrivateKey, string) {
	master, private, id := publicSigningKey(t, user, "master")
	self, selfPrivate, _ := publicSigningKey(t, user, "self_signing")
	signing, _, _ := publicSigningKey(t, user, "user_signing")
	addTestSignature(t, self, user, id, private)
	addTestSignature(t, signing, user, id, private)
	return &signingUpload{Master: master, Self: self, User: signing}, selfPrivate, id
}
func TestClientSigningUploadValidatesAndQueriesPublicKeys(t *testing.T) {
	conn := clientSigningDB(t)
	user := "@alice:test.local"
	request, _, _ := signingBundle(t, user)
	require.NoError(t, storeClientSigningKeys(conn, user, request))
	response := &QueryKeysResponse{}
	require.NoError(t, appendClientSigningKeys(response, map[string][]string{user: nil}, user))
	require.Len(t, response.MasterKeys, 1)
	require.Len(t, response.SelfSigningKeys, 1)
	require.Len(t, response.UserSigningKeys, 1)
	require.Nil(t, response.MasterKeys[user].PrivateKey)
	require.NoError(t, appendClientSigningKeys(response, map[string][]string{user: nil}, "@bob:test.local"))
	require.Empty(t, response.UserSigningKeys)
	request.Self["user_id"] = "@bob:test.local"
	require.Error(t, storeClientSigningKeys(conn, user, request))
	request.Self["user_id"] = user
	request.Self["signatures"] = map[string]any{}
	require.Error(t, storeClientSigningKeys(conn, user, request))
	// Rejected uploads leave all previous active keys intact.
	var active int64
	require.NoError(t, conn.Model(&MatrixCrossSigningKey{}).Where("is_active = ?", true).Count(&active).Error)
	require.EqualValues(t, 3, active)
	rotated, _, _ := signingBundle(t, user)
	require.NoError(t, storeClientSigningKeys(conn, user, &signingUpload{Master: rotated.Master}))
	require.NoError(t, appendClientSigningKeys(response, map[string][]string{user: nil}, user))
	require.Len(t, response.MasterKeys, 1)
	require.Empty(t, response.SelfSigningKeys)
	require.Empty(t, response.UserSigningKeys)
}
func TestClientSigningSignatureUploadPersistsOnlyVerifiedObjects(t *testing.T) {
	conn := clientSigningDB(t)
	user := "@alice:test.local"
	request, selfPrivate, masterID := signingBundle(t, user)
	require.NoError(t, storeClientSigningKeys(conn, user, request))
	public, devicePrivate, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	device := map[string]any{"user_id": user, "device_id": "DEV", "algorithms": []any{"m.olm.v1.curve25519-aes-sha2"}, "keys": map[string]any{"ed25519:DEV": base64.RawStdEncoding.EncodeToString(public)}}
	addTestSignature(t, device, user, "ed25519:DEV", devicePrivate)
	raw, _ := json.Marshal(device)
	require.NoError(t, conn.Create(&MatrixDeviceKey{UserID: user, DeviceID: "DEV", DeviceKey: string(raw)}).Error)
	selfID, _, err := decodeSigningPublic(request.Self, user, "self_signing")
	require.NoError(t, err)
	addTestSignature(t, device, user, selfID, selfPrivate)
	require.NoError(t, uploadObjectSignatures(conn, user, user, "DEV", device))
	var saved MatrixDeviceKey
	require.NoError(t, conn.First(&saved).Error)
	require.Contains(t, saved.DeviceKey, selfID)
	addTestSignature(t, request.Master, user, "ed25519:DEV", devicePrivate)
	require.NoError(t, uploadObjectSignatures(conn, user, user, masterID[len("ed25519:"):], request.Master))
	require.Error(t, uploadObjectSignatures(conn, "@bob:test.local", user, "DEV", device))
	addTestSignature(t, device, user, selfID, devicePrivate)
	require.Error(t, uploadObjectSignatures(conn, user, user, "DEV", device), "a signature from the wrong private key must fail")
	device["device_id"] = "ALTERED"
	require.Error(t, uploadObjectSignatures(conn, user, user, "DEV", device))
}
func TestClientSigningUploadPasswordUIA(t *testing.T) {
	conn := clientSigningDB(t)
	password := "local-test-password"
	salt := "local-test-salt"
	hash, err := models.EncryptPassword(password, salt)
	require.NoError(t, err)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.User{Id: 1, UserName: "alice"}).Error)
	require.NoError(t, conn.Create(&models.UserSecretData{UserId: 1, Password: hash, Salt: salt}).Error)
	old := GlobalHS
	hs := &HomeServer{Config: &HSConfig{ServerName: "test.local"}}
	hs.Auth = NewAuthService(hs)
	GlobalHS = hs
	t.Cleanup(func() { GlobalHS = old })
	request, _, _ := signingBundle(t, "@alice:test.local")
	call := func(device string) (int, map[string]any) {
		raw, _ := json.Marshal(request)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/keys/device_signing/upload", bytes.NewReader(raw))
		c.Set("user_id", "@alice:test.local")
		c.Set("device_id", device)
		handleDeviceSigningUpload(c)
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		return w.Code, body
	}
	code, challenge := call("DEV")
	require.Equal(t, 401, code)
	request.Auth = &signingAuth{Type: "m.login.password", Session: challenge["session"].(string), Password: "wrong"}
	code, _ = call("DEV")
	require.Equal(t, 401, code)
	request.Auth.Password = password
	code, body := call("OTHER")
	require.Equal(t, 401, code)
	require.NotEqual(t, request.Auth.Session, body["session"])
	code, _ = call("DEV")
	require.Equal(t, 200, code)
	code, _ = call("DEV")
	require.Equal(t, 401, code, "successful UIA sessions cannot be replayed")
}

// Both requests finish password verification after reading the same valid
// session. Exactly one may consume it; the other must receive a fresh challenge.
func TestClientSigningUIAConcurrentConsumption(t *testing.T) {
	request := &signingUpload{Auth: &signingAuth{Type: "m.login.password", Session: "concurrent-session", Password: "verified"}}
	payload := *request
	payload.Auth = nil
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	signingChallenges.Lock()
	signingChallenges.Sessions[request.Auth.Session] = signingChallenge{User: "@alice:test.local", Device: "DEV", Hash: sha256.Sum256(raw), Expires: time.Now().Add(time.Minute)}
	signingChallenges.Unlock()
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	type result struct {
		accepted bool
		status   int
		session  string
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Set("user_id", "@alice:test.local")
			context.Set("device_id", "DEV")
			accepted := signingUIAWithVerifier(context, request, func(string, string) bool { entered <- struct{}{}; <-release; return true })
			var body struct {
				Session string `json:"session"`
			}
			_ = json.Unmarshal(recorder.Body.Bytes(), &body)
			results <- result{accepted: accepted, status: recorder.Code, session: body.Session}
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("requests did not reach password verification")
		}
	}
	close(release)
	accepted := 0
	for range 2 {
		got := <-results
		if got.accepted {
			accepted++
		} else {
			require.Equal(t, 401, got.status)
			require.NotEmpty(t, got.session)
			require.NotEqual(t, request.Auth.Session, got.session)
		}
	}
	require.Equal(t, 1, accepted)
	signingChallenges.Lock()
	_, exists := signingChallenges.Sessions[request.Auth.Session]
	signingChallenges.Unlock()
	require.False(t, exists, "original session is consumed exactly once")
}
