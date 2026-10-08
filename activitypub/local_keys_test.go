package activitypub

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLocalActorKeyPersistsAndSigns(t *testing.T) {
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous) })
	require.NoError(t, conn.AutoMigrate(&models.Account{}, &models.AccountKey{}, &models.AccountExtra{}, &models.Emoji{}))
	account := &models.Account{Id: 991, Username: "local_actor"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(account).Error)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/users/local_actor", nil)
	c.Params = gin.Params{{Key: "username", Value: "local_actor"}}
	ActorProfile(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "application/activity+json", w.Header().Get("Content-Type"))
	first, err := localAccountKey(account)
	require.NoError(t, err)
	second, err := localAccountKey(account)
	require.NoError(t, err)
	require.Equal(t, first.PublicKey, second.PublicKey)
	require.Equal(t, first.ID, second.ID)
	block, _ := pem.Decode([]byte(first.PrivateKey))
	require.NotNil(t, block)
	private, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	require.NoError(t, err)
	require.GreaterOrEqual(t, private.N.BitLen(), 2048)
	actorURL := "https://community.federation.test/users/local_actor"
	client, err := localAccountClient(account, actorURL)
	require.NoError(t, err)
	body := []byte(`{"type":"Accept"}`)
	request := httptest.NewRequest("POST", "https://misskey.federation.test/inbox", bytes.NewReader(body))
	require.NoError(t, client.signRequest(request, body))
	require.NoError(t, VerifyRequest(request, &private.PublicKey))
	require.Contains(t, request.Header.Get("Signature"), actorURL+"#main-key")
	remoteDomain := "misskey.federation.test"
	_, err = localAccountKey(&models.Account{Id: 992, Domain: &remoteDomain})
	require.Error(t, err)
}
