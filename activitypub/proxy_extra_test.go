package activitypub

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyServiceGetOrCreateProxyAccount(t *testing.T) {
	testDB := setupProxyTestDB(t)
	service := &ProxyService{db: testDB.GormDB}

	// First call should create the proxy account
	account, key, err := service.GetOrCreateProxyAccount()
	require.NoError(t, err)
	require.NotNil(t, account)
	require.NotNil(t, key)
	assert.Equal(t, proxyUsername, account.Username)
	assert.True(t, account.Bot)
	assert.Contains(t, key.PublicKey, "PUBLIC KEY")
	assert.Contains(t, key.PrivateKey, "PRIVATE KEY")

	// Second call should return the cached account
	account2, key2, err2 := service.GetOrCreateProxyAccount()
	require.NoError(t, err2)
	assert.Equal(t, account.Id, account2.Id)
	assert.Equal(t, key.AccountID, key2.AccountID)
}

func TestProxyServiceGetOrCreateProxyAccountExisting(t *testing.T) {
	testDB := setupProxyTestDB(t)

	// Pre-create proxy account + key
	existingAccount := &models.Account{
		Username: proxyUsername,
		Bot:      true,
	}
	require.NoError(t, testDB.GormDB.Create(existingAccount).Error)

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
	pubBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubBytes,
	})

	existingKey := &models.AccountKey{
		AccountID:  existingAccount.Id,
		PublicKey:  string(pubPEM),
		PrivateKey: string(privPEM),
	}
	require.NoError(t, testDB.GormDB.Create(existingKey).Error)

	service := &ProxyService{db: testDB.GormDB}
	account, key, err := service.GetOrCreateProxyAccount()
	require.NoError(t, err)
	assert.Equal(t, existingAccount.Id, account.Id)
	assert.Equal(t, existingKey.AccountID, key.AccountID)
}

func TestProxyServiceGetOrCreateProxyAccountKeyNotFound(t *testing.T) {
	testDB := setupProxyTestDB(t)

	// Create proxy account without a key
	account := &models.Account{
		Username: proxyUsername,
		Bot:      true,
	}
	require.NoError(t, testDB.GormDB.Create(account).Error)

	// Call the internal method directly (bypassing the sync.Once cache)
	service := &ProxyService{db: testDB.GormDB}
	_, _, err := service.getOrCreateProxyAccountInternal()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key not found")
}

func TestProxySendFollowActivity(t *testing.T) {
	testDB := setupProxyTestDB(t)

	// Mock remote actor server
	actorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":  "Person",
			"id":    "https://mock.example.com/users/remote",
			"inbox": r.Header.Get("X-Test-Inbox"),
		})
	}))
	defer actorServer.Close()

	// Generate a real RSA key for signing
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	proxyAccount := &models.Account{Username: proxyUsername, Bot: true}
	require.NoError(t, testDB.GormDB.Create(proxyAccount).Error)

	proxyKey := &models.AccountKey{
		AccountID:  proxyAccount.Id,
		PublicKey:  "test",
		PrivateKey: string(privPEM),
	}
	require.NoError(t, testDB.GormDB.Create(proxyKey).Error)

	service := &ProxyService{db: testDB.GormDB}

	// Test sendFollowActivity with a remote account that has no OriginId
	remoteAccount := &models.Account{Username: "remote"}
	_, err = service.sendFollowActivity(proxyAccount, proxyKey, remoteAccount)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no OriginId")
}

func TestProxyEnsureProxyFollowNonExistentAccount(t *testing.T) {
	testDB := setupProxyTestDB(t)

	// Create proxy account + key so GetOrCreateProxyAccount works
	proxyAccount := &models.Account{Username: proxyUsername, Bot: true}
	require.NoError(t, testDB.GormDB.Create(proxyAccount).Error)
	proxyKey := &models.AccountKey{
		AccountID:  proxyAccount.Id,
		PublicKey:  "test-pub",
		PrivateKey: "test-priv",
	}
	require.NoError(t, testDB.GormDB.Create(proxyKey).Error)

	service := &ProxyService{db: testDB.GormDB}

	// Non-existent remote account ID
	err := service.EnsureProxyFollow(99999, models.ProxyFollowReasonList, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote account not found")
}

func TestProxyCleanupProxyFollowNonExistent(t *testing.T) {
	testDB := setupProxyTestDB(t)

	service := &ProxyService{db: testDB.GormDB}

	// Cleanup non-existent reference should not error
	err := service.CleanupProxyFollow(99999, models.ProxyFollowReasonList, 1)
	require.NoError(t, err)
}

func TestProxySendUndoFollowActivityNoOriginId(t *testing.T) {
	testDB := setupProxyTestDB(t)

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	proxyAccount := &models.Account{Username: proxyUsername, Bot: true}
	require.NoError(t, testDB.GormDB.Create(proxyAccount).Error)

	proxyKey := &models.AccountKey{
		AccountID:  proxyAccount.Id,
		PublicKey:  "test",
		PrivateKey: string(privPEM),
	}
	require.NoError(t, testDB.GormDB.Create(proxyKey).Error)

	service := &ProxyService{db: testDB.GormDB}

	// Remote account with no OriginId
	remoteAccount := &models.Account{Username: "remote2"}
	err = service.sendUndoFollowActivity(proxyAccount, proxyKey, remoteAccount, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no OriginId")
}

func TestProxyHandleAcceptNotFound(t *testing.T) {
	testDB := setupProxyTestDB(t)
	service := &ProxyService{db: testDB.GormDB}

	err := service.HandleAccept("https://nonexistent.example.com/follow/999")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// Test Featured handler
func TestFeaturedUserNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testDB := setupStatusReachDB(t)
	_ = testDB

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = append(c.Params, gin.Param{Key: "username", Value: "ghost"})
	c.Request = httptest.NewRequest(http.MethodGet, "/users/ghost/collections/featured", nil)

	Featured(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestFeaturedSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testDB := setupStatusReachDB(t)

	account := &models.Account{Username: "featureduser"}
	require.NoError(t, testDB.GormDB.Create(account).Error)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = append(c.Params, gin.Param{Key: "username", Value: "featureduser"})
	c.Request = httptest.NewRequest(http.MethodGet, "/users/featureduser/collections/featured", nil)

	Featured(c)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "OrderedCollection", resp["type"])
	assert.Equal(t, float64(0), resp["totalItems"])
}

// Test Outbox handler
func TestOutboxUserNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupStatusReachDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = append(c.Params, gin.Param{Key: "username", Value: "ghostoutbox"})
	c.Request = httptest.NewRequest(http.MethodGet, "/users/ghostoutbox/outbox", nil)

	Outbox(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestOutboxCollectionInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testDB := setupStatusReachDB(t)

	account := &models.Account{Username: "outboxuser"}
	require.NoError(t, testDB.GormDB.Create(account).Error)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = append(c.Params, gin.Param{Key: "username", Value: "outboxuser"})
	c.Request = httptest.NewRequest(http.MethodGet, "/users/outboxuser/outbox", nil)

	Outbox(c)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "OrderedCollection", resp["type"])
	assert.Contains(t, resp["id"], "outbox")
}

// Additional mention tests with DB
func TestResolveMentionsWithDB(t *testing.T) {
	setupStatusReachDB(t)

	// No accounts in DB, so all resolutions should return empty
	mentions := []Mention{
		{Username: "ghost", Domain: "example.com", URI: "https://example.com/users/ghost"},
	}
	result := ResolveMentions(mentions)
	assert.Empty(t, result)
}

func TestGetMentionAccountIDsEmpty(t *testing.T) {
	setupStatusReachDB(t)

	// No accounts in DB
	ids := GetMentionAccountIDs("Hello @ghost@example.com", nil)
	assert.Empty(t, ids)
}

func TestParseTagsToAccountIDsFromDB(t *testing.T) {
	testDB := setupStatusReachDB(t)

	// Create an account with an origin_id
	account := &models.Account{
		Username: "tagged",
		OriginId: "https://example.com/users/tagged",
	}
	require.NoError(t, testDB.GormDB.Create(account).Error)

	tags := []any{
		map[string]any{
			"type": "Mention",
			"href": "https://example.com/users/tagged",
			"name": "@tagged@example.com",
		},
	}
	ids := parseTagsToAccountIDs(tags)
	assert.Len(t, ids, 1)
	assert.Equal(t, account.Id, ids[0])
}
