package passkey

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMain(m *testing.M) {
	database.SetTestMode()
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

var b64 = base64.RawURLEncoding

// softKey is a software ES256 authenticator holding one credential.
type softKey struct {
	priv   *ecdsa.PrivateKey
	credID []byte
	handle []byte // user handle returned on assertion
	be, bs bool
	count  uint32
}

func newSoftKey(t *testing.T, handle string, be, bs bool) *softKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	id := make([]byte, 16)
	_, _ = rand.Read(id)

	return &softKey{priv: priv, credID: id, handle: []byte(handle), be: be, bs: bs, count: 5}
}

// coseB64 is the public key as Misskey stores it: base64url COSE_Key.
func (k *softKey) coseB64(t *testing.T) string {
	t.Helper()
	pub := k.priv.PublicKey
	x, y := make([]byte, 32), make([]byte, 32)
	pub.X.FillBytes(x)
	pub.Y.FillBytes(y)
	raw, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{KeyType: 2, Algorithm: -7},
		Curve:         1, XCoord: x, YCoord: y,
	})
	require.NoError(t, err)

	return b64.EncodeToString(raw)
}

func (k *softKey) assertion(t *testing.T, rpID, origin, challenge string) []byte {
	t.Helper()
	k.count++
	rpHash := sha256.Sum256([]byte(rpID))
	flags := byte(0x01 | 0x04) // UP | UV
	if k.be {
		flags |= 0x08
	}
	if k.bs {
		flags |= 0x10
	}
	authData := append(append(rpHash[:], flags), binary.BigEndian.AppendUint32(nil, k.count)...)
	clientData, err := json.Marshal(map[string]any{"type": "webauthn.get", "challenge": challenge, "origin": origin, "crossOrigin": false})
	require.NoError(t, err)
	cdHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte{}, authData...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, digest[:])
	require.NoError(t, err)
	body, err := json.Marshal(map[string]any{
		"id": b64.EncodeToString(k.credID), "rawId": b64.EncodeToString(k.credID), "type": "public-key",
		"response": map[string]any{
			"authenticatorData": b64.EncodeToString(authData),
			"clientDataJSON":    b64.EncodeToString(clientData),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        b64.EncodeToString(k.handle),
		},
	})
	require.NoError(t, err)

	return body
}

func (k *softKey) misskeyRow(t *testing.T, userID, username, deviceType string, backedUp bool) MisskeySecurityKey {
	return MisskeySecurityKey{
		ID: b64.EncodeToString(k.credID), UserID: userID, Username: username, PublicKey: k.coseB64(t),
		Name: "My key", Counter: int64(k.count), CredentialDeviceType: deviceType, CredentialBackedUp: backedUp,
		Transports: []string{"internal", "hybrid"},
	}
}

type passkeyEnv struct {
	gdb *gorm.DB
	fix *testutil.FixtureBuilder
}

func newPasskeyEnv(t *testing.T) *passkeyEnv {
	t.Helper()
	dsn := fmt.Sprintf("file:passkey_test_%d?mode=memory&cache=shared", time.Now().UnixNano())
	gdb, err := pgtest.GormOpen(t, sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&models.User{}, &models.Account{}, &models.AccountExtra{}, &models.AccountEmoji{}, &models.AuthToken{}, &models.Role{}, &models.RoleUser{},
		&models.PasskeyCredential{}, &models.WebAuthnSession{}))
	database.SetTestDB(gdb)
	systemsettings.Set(systemsettings.KeyRegistrationCheckEmail, false)
	t.Cleanup(database.ResetTestDB)

	return &passkeyEnv{gdb: gdb, fix: testutil.NewFixtureBuilder(t, gdb)}
}

func (e *passkeyEnv) localUser(username string) *models.User {
	u := e.fix.CreateUser(func(u *models.User) { u.UserName = username })
	e.fix.CreateAccount(u, func(a *models.Account) { a.Username = username })

	return u
}

// loadVia goes through the real Misskey SQL when running on PostgreSQL: the
// rows are written into Misskey-shaped tables and read back.
func loadVia(t *testing.T, gdb *gorm.DB, keys []MisskeySecurityKey) []MisskeySecurityKey {
	t.Helper()
	if !pgtest.Enabled() {
		return keys
	}
	require.NoError(t, gdb.Exec(`CREATE TABLE IF NOT EXISTS "user" (id varchar(32) PRIMARY KEY, username varchar(128) NOT NULL, host varchar(512))`).Error)
	require.NoError(t, gdb.Exec(`CREATE TABLE IF NOT EXISTS user_security_key (id varchar PRIMARY KEY, "userId" varchar(32) NOT NULL,
		"publicKey" varchar NOT NULL, "lastUsed" timestamptz NOT NULL DEFAULT now(), name varchar(30) NOT NULL, counter bigint NOT NULL DEFAULT 0,
		"credentialDeviceType" varchar(32), "credentialBackedUp" boolean, transports varchar(32)[])`).Error)
	require.NoError(t, gdb.Exec(`DELETE FROM user_security_key`).Error)
	require.NoError(t, gdb.Exec(`DELETE FROM "user"`).Error)
	// A remote user with a key must never be imported.
	require.NoError(t, gdb.Exec(`INSERT INTO "user" (id, username, host) VALUES ('remote1', 'alice', 'other.example')`).Error)
	require.NoError(t, gdb.Exec(`INSERT INTO user_security_key (id, "userId", "publicKey", name) VALUES ('cmVtb3Rl', 'remote1', 'pQECAyYg', 'x')`).Error)
	for _, k := range keys {
		require.NoError(t, gdb.Exec(`INSERT INTO "user" (id, username) VALUES (?, ?) ON CONFLICT DO NOTHING`, k.UserID, k.Username).Error)
		require.NoError(t, gdb.Exec(`INSERT INTO user_security_key (id, "userId", "publicKey", name, counter, "credentialDeviceType", "credentialBackedUp", transports)
			VALUES (?, ?, ?, ?, ?, ?, ?, string_to_array(?, ','))`, k.ID, k.UserID, k.PublicKey, k.Name, k.Counter,
			k.CredentialDeviceType, k.CredentialBackedUp, strings.Join(k.Transports, ",")).Error)
	}
	got, err := LoadMisskeySecurityKeys(gdb)
	require.NoError(t, err)
	require.Len(t, got, len(keys))
	for i := range got {
		assert.Equal(t, keys[i].Transports, got[i].Transports)
	}

	return got
}

func withRP(t *testing.T, base, rpID, origins string) {
	t.Helper()
	oldBase, oldConfigured := config.C.AppBaseURL, config.AppBaseURLConfigured()
	oldID, oldOrigins := config.C.WebAuthnRPID, config.C.WebAuthnRPOrigins
	config.SetAppBaseURL(base, true)
	config.C.WebAuthnRPID, config.C.WebAuthnRPOrigins = rpID, origins
	t.Cleanup(func() {
		config.SetAppBaseURL(oldBase, oldConfigured)
		config.C.WebAuthnRPID, config.C.WebAuthnRPOrigins = oldID, oldOrigins
	})
}

// login runs LoginBegin and LoginFinish against the real handlers.
func login(t *testing.T, key *softKey, rpID, origin string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/login/begin", nil)
	c.Request.Header.Set("Origin", origin)
	LoginBegin(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var begin struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			RPID      string `json:"rpId"`
		} `json:"publicKey"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &begin))
	require.Equal(t, rpID, begin.PublicKey.RPID)
	cookies := w.Result().Cookies()

	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/login/finish",
		bytes.NewReader(key.assertion(t, rpID, origin, begin.PublicKey.Challenge)))
	c2.Request.Header.Set("Content-Type", "application/json")
	c2.Request.Header.Set("Origin", origin)
	for _, ck := range cookies {
		c2.Request.AddCookie(ck)
	}
	LoginFinish(c2)

	return w2
}

func TestConvertMisskeySecurityKey(t *testing.T) {
	key := newSoftKey(t, "9xyzmisskeyid", true, true)
	row := key.misskeyRow(t, "9xyzmisskeyid", "alice", "multiDevice", true)
	assert.True(t, strings.HasPrefix(row.PublicKey, "pQECAyYg"), "ES256 COSE key as Misskey stores it")

	cred, err := ConvertMisskeySecurityKey(row)
	require.NoError(t, err)
	assert.Equal(t, key.credID, cred.ID)
	raw, _ := b64.DecodeString(row.PublicKey)
	assert.Equal(t, raw, cred.PublicKey)
	assert.Equal(t, "none", cred.AttestationType)
	assert.Equal(t, uint32(5), cred.Authenticator.SignCount)
	assert.True(t, cred.Flags.BackupEligible)
	assert.True(t, cred.Flags.BackupState)
	assert.Len(t, cred.Transport, 2)

	// Round trip through the stored JSON format.
	js, err := json.Marshal(cred)
	require.NoError(t, err)
	var back webauthn.Credential
	require.NoError(t, json.Unmarshal(js, &back))
	assert.Equal(t, cred.ID, back.ID)
	assert.True(t, back.Flags.BackupEligible)

	single := row
	single.CredentialDeviceType, single.CredentialBackedUp = "singleDevice", false
	cred, err = ConvertMisskeySecurityKey(single)
	require.NoError(t, err)
	assert.False(t, cred.Flags.BackupEligible)
	assert.False(t, cred.Flags.BackupState)

	backedOnly := single
	backedOnly.CredentialBackedUp = true
	cred, err = ConvertMisskeySecurityKey(backedOnly)
	require.NoError(t, err)
	assert.True(t, cred.Flags.BackupEligible)

	padded := row
	padded.ID = base64.URLEncoding.EncodeToString(key.credID)
	cred, err = ConvertMisskeySecurityKey(padded)
	require.NoError(t, err)
	assert.Equal(t, key.credID, cred.ID)

	bad := row
	bad.PublicKey = "not-a-key"
	_, err = ConvertMisskeySecurityKey(bad)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "not-a-key")
}

func TestConfiguredRPDefaultsToBaseURL(t *testing.T) {
	withRP(t, "https://c.hime.at", "", "")
	rp, err := ConfiguredRP()
	require.NoError(t, err)
	assert.Equal(t, "c.hime.at", rp.ID)
	assert.Equal(t, "Community", rp.DisplayName)
	assert.Equal(t, []string{"https://c.hime.at"}, rp.Origins)

	withRP(t, "https://c.hime.at", "Maid.lat", " https://maid.lat/ ,https://www.maid.lat")
	rp, err = ConfiguredRP()
	require.NoError(t, err)
	assert.Equal(t, "maid.lat", rp.ID)
	assert.Equal(t, []string{"https://c.hime.at", "https://maid.lat", "https://www.maid.lat"}, rp.Origins)
}

func TestMisskeyPasskeyImportAndLogin(t *testing.T) {
	env := newPasskeyEnv(t)
	withRP(t, "https://c.hime.at", "maid.lat", "https://maid.lat")
	alice := env.localUser("alice")
	bob := env.localUser("bob")

	multi := newSoftKey(t, "9aliceid", true, true)
	single := newSoftKey(t, "9bobid", false, false)
	orphan := newSoftKey(t, "9carolid", true, true)
	keys := loadVia(t, env.gdb, []MisskeySecurityKey{
		multi.misskeyRow(t, "9aliceid", "Alice", "multiDevice", true),
		single.misskeyRow(t, "9bobid", "bob", "singleDevice", false),
		orphan.misskeyRow(t, "9carolid", "carol", "multiDevice", true),
	})

	dry, err := ImportMisskeySecurityKeys(env.gdb, keys, true)
	require.NoError(t, err)
	assert.Equal(t, 2, dry.Imported)
	assert.Equal(t, 1, dry.Unmatched)
	var n int64
	require.NoError(t, env.gdb.Model(&models.PasskeyCredential{}).Count(&n).Error)
	assert.Zero(t, n, "dry run writes nothing")

	res, err := ImportMisskeySecurityKeys(env.gdb, keys, false)
	require.NoError(t, err)
	assert.Equal(t, MisskeyImportResult{Imported: 2, Skipped: 0, Unmatched: 1}, MisskeyImportResult{Imported: res.Imported, Skipped: res.Skipped, Unmatched: res.Unmatched, Invalid: res.Invalid})
	for _, line := range res.Details {
		assert.NotContains(t, line, keys[0].PublicKey[:20])
	}

	var row models.PasskeyCredential
	require.NoError(t, env.gdb.Where("credential_id = ?", b64.EncodeToString(multi.credID)).First(&row).Error)
	assert.Equal(t, alice.Id, row.UserId)
	assert.Equal(t, "My key", row.Name)

	again, err := ImportMisskeySecurityKeys(env.gdb, keys, false)
	require.NoError(t, err)
	assert.Equal(t, 0, again.Imported)
	assert.Equal(t, 2, again.Skipped)
	assert.Equal(t, 1, again.Unmatched)
	require.NoError(t, env.gdb.Model(&models.PasskeyCredential{}).Count(&n).Error)
	assert.Equal(t, int64(2), n)

	w := login(t, multi, "maid.lat", "https://maid.lat")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "login success")
	var tok models.AuthToken
	require.NoError(t, env.gdb.Where("user_id = ?", alice.Id).First(&tok).Error)

	w = login(t, single, "maid.lat", "https://maid.lat")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, env.gdb.Where("user_id = ?", bob.Id).First(&models.AuthToken{}).Error)

	// Sign count was updated after login.
	require.NoError(t, env.gdb.Where("credential_id = ?", b64.EncodeToString(multi.credID)).First(&row).Error)
	var stored webauthn.Credential
	require.NoError(t, json.Unmarshal(row.CredentialJSON, &stored))
	assert.Equal(t, multi.count, stored.Authenticator.SignCount)
	assert.NotNil(t, row.LastUsedAt)
}

func TestMisskeyPasskeyLoginRejections(t *testing.T) {
	env := newPasskeyEnv(t)
	withRP(t, "https://c.hime.at", "maid.lat", "https://maid.lat")
	env.localUser("alice")
	key := newSoftKey(t, "9aliceid", true, true)
	_, err := ImportMisskeySecurityKeys(env.gdb, []MisskeySecurityKey{key.misskeyRow(t, "9aliceid", "alice", "multiDevice", true)}, false)
	require.NoError(t, err)

	// A different user handle than the one stored is rejected.
	key.handle = []byte("someone-else")
	assert.Equal(t, http.StatusUnauthorized, login(t, key, "maid.lat", "https://maid.lat").Code)
	key.handle = []byte("9aliceid")

	// Mismatching BE flag (stored multiDevice, authenticator says not eligible).
	key.be, key.bs = false, false
	assert.Equal(t, http.StatusUnauthorized, login(t, key, "maid.lat", "https://maid.lat").Code)
	key.be, key.bs = true, true

	// Assertion for the old RP ID fails once the server is configured for maid.lat.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	LoginBegin(c)
	var begin struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &begin))
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(key.assertion(t, "c.hime.at", "https://c.hime.at", begin.PublicKey.Challenge)))
	for _, ck := range w.Result().Cookies() {
		c2.Request.AddCookie(ck)
	}
	LoginFinish(c2)
	assert.Equal(t, http.StatusUnauthorized, w2.Code)

	// Still works with the right RP afterwards.
	assert.Equal(t, http.StatusOK, login(t, key, "maid.lat", "https://maid.lat").Code)
}
