package activitypub

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http/httptest"
	"testing"
	"time"
)

func TestImportedActorKeysAndOpaqueObject(t *testing.T) {
	conn := outboundDB(t)
	require.NoError(t, conn.AutoMigrate(&models.AccountFederationKey{}))
	raw := conn.Session(&gorm.Session{SkipHooks: true})
	base := systemsettings.GetBaseURL()
	account := outboundAccount(t, conn, "migrated_login", false)
	account.LocalActorURI = base + "/ap/users/12345"
	require.NoError(t, raw.Model(account).Update("local_actor_uri", account.LocalActorURI).Error)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	stored := models.AccountKey{AccountID: account.Id, KeyID: account.LocalActorURI + "#rsa-source", PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), PublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}))}
	require.NoError(t, stored.ValidateKeyPair())
	require.NoError(t, raw.Create(&stored).Error)
	require.NoError(t, conn.Create(&models.AccountFederationKey{AccountID: account.Id, KeyID: account.LocalActorURI + "#rsa-extra", PublicKey: stored.PublicKey, PrivateKey: stored.PrivateKey, Algorithm: "RSA"}).Error)
	expired := time.Now().Add(-time.Hour)
	require.NoError(t, conn.Create(&models.AccountFederationKey{AccountID: account.Id, KeyID: account.LocalActorURI + "#expired", PublicKey: stored.PublicKey, Algorithm: "RSA", ExpiresAt: &expired}).Error)
	actor, err := buildActorProfile(account, nil)
	require.NoError(t, err)
	data, err := json.Marshal(actor)
	require.NoError(t, err)
	var decoded Actor
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, account.LocalActorURI, decoded.ID)
	require.Len(t, decoded.PublicKeys, 1)
	selected, err := actorSigningKey(&decoded, account.LocalActorURI, account.LocalActorURI+"#rsa-extra")
	require.NoError(t, err)
	require.Equal(t, stored.PublicKey, selected.PublicKeyPem)
	_, err = actorSigningKey(&decoded, account.LocalActorURI, account.LocalActorURI+"#expired")
	require.Error(t, err)
	decoded.PublicKeys[0].Owner = "https://attacker.test/users/x"
	_, err = actorSigningKey(&decoded, account.LocalActorURI, account.LocalActorURI+"#rsa-extra")
	require.Error(t, err)
	client, err := localAccountClient(account, account.LocalActorURI)
	require.NoError(t, err)
	body := []byte(`{"type":"Like"}`)
	request := httptest.NewRequest("POST", "https://remote.test/inbox", bytes.NewReader(body))
	require.NoError(t, client.signRequest(request, body))
	require.NoError(t, VerifyRequest(request, &key.PublicKey))
	require.Contains(t, request.Header.Get("Signature"), stored.KeyID)
	var reread models.AccountKey
	require.NoError(t, conn.First(&reread, stored.ID).Error)
	require.Equal(t, stored.PrivateKey, reread.PrivateKey)
	now := time.Now()
	status := models.Status{Id: 778, AccountId: account.Id, LocalObjectURI: base + "/notes/sourceOpaque", Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: models.StatusContent{Content: "migrated"}}
	require.NoError(t, raw.Create(&status).Error)
	router := gin.New()
	router.GET("/notes/:id", ImportedStatusObject)
	router.GET("/ap/users/:username", ActorProfile)
	for path, want := range map[string]int{"/notes/sourceOpaque": 200, "/notes/778": 404, "/ap/users/12345": 200, "/ap/users/migrated_login": 404} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", base+path, nil))
		require.Equal(t, want, w.Code, w.Body.String())
	}
	// Expiry after import must switch the actual signing ID, not merely hide it.
	require.NoError(t, conn.Create(&models.AccountFederationKey{AccountID: account.Id, KeyID: stored.KeyID, PublicKey: stored.PublicKey, PrivateKey: stored.PrivateKey, Algorithm: "rsa", ExpiresAt: &expired}).Error)
	rotated, err := localAccountClient(account, account.LocalActorURI)
	require.NoError(t, err)
	request = httptest.NewRequest("POST", "https://remote.test/inbox", bytes.NewReader(body))
	require.NoError(t, rotated.signRequest(request, body))
	require.Contains(t, request.Header.Get("Signature"), account.LocalActorURI+"#rsa-extra")
	require.NoError(t, VerifyRequest(request, &key.PublicKey))
	require.NoError(t, conn.Model(&models.AccountFederationKey{}).Where("account_id = ?", account.Id).UpdateColumn("revoked", true).Error)
	_, err = localAccountClient(account, account.LocalActorURI)
	require.Error(t, err)
	missing := outboundAccount(t, conn, "missing_import_key", false)
	missing.LocalActorURI = base + "/users/sourceNoKey"
	require.NoError(t, raw.Model(missing).Update("local_actor_uri", missing.LocalActorURI).Error)
	_, err = localAccountKey(missing)
	require.Error(t, err)
}

func TestImportedFollowAcceptanceUsesOriginalActivityIdentity(t *testing.T) {
	conn := persistenceDB(t, false)
	local := models.Account{Username: "source_local", LocalActorURI: systemsettings.GetBaseURL() + "/ap/users/991"}
	domain := "remote.example"
	remote := models.Account{Username: "remote", Domain: &domain, OriginId: "https://remote.example/users/remote"}
	require.NoError(t, conn.Create(&local).Error)
	require.NoError(t, conn.Create(&remote).Error)
	original := systemsettings.GetBaseURL() + "/opaque-follow/123456"
	follow := models.Follow{AccountId: local.Id, FollowId: remote.Id, Status: models.FollowStatusRequested, Properties: map[string]any{"activity_uri": original}}
	require.NoError(t, conn.Create(&follow).Error)
	invoke := func(actor, id string) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		handleAcceptActivity(context.WithValue(context.Background(), actorKey, actor), c, map[string]any{"object": map[string]any{"type": "Follow", "id": id, "actor": local.LocalActorURI, "object": remote.OriginId}}, &local)
		return w.Code
	}
	require.Equal(t, 403, invoke("https://remote.example/users/attacker", original))
	require.Equal(t, 200, invoke(remote.OriginId, original))
	require.Equal(t, 200, invoke(remote.OriginId, original))
	require.NoError(t, conn.First(&follow, follow.Id).Error)
	require.Equal(t, models.FollowStatusFollowed, follow.Status)
	var extra models.AccountExtra
	require.NoError(t, conn.First(&extra, "account_id = ?", local.Id).Error)
	require.Equal(t, 1, extra.FollowingCount)
}
