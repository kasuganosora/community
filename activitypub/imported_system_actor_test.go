package activitypub

import (
	"bytes"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http/httptest"
	"testing"
)

func TestImportedSystemActorEndpointsRetainIdentityAndKey(t *testing.T) {
	conn := outboundDB(t)
	require.NoError(t, conn.AutoMigrate(&models.AccountFederationKey{}, &models.PinStatus{}))
	account := outboundAccount(t, conn, "source_system", false)
	preserved, err := localAccountKey(account)
	require.NoError(t, err)
	account.LocalActorURI = systemsettings.GetBaseURL() + "/actor"
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(account).Update("local_actor_uri", account.LocalActorURI).Error)
	preserved.KeyID = account.LocalActorURI + "#rsa-original"
	require.NoError(t, conn.Model(preserved).Update("key_id", preserved.KeyID).Error)
	router := gin.New()
	router.GET("/actor", ActorProfile)
	router.GET("/actor/outbox", Outbox)
	router.GET("/actor/followers", Followers)
	router.GET("/actor/following", Following)
	router.GET("/actor/collections/featured", Featured)
	router.POST("/actor/inbox", Inbox)
	for _, path := range []string{"/actor", "/actor/outbox", "/actor/followers", "/actor/following", "/actor/collections/featured"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		require.Equal(t, 200, w.Code, path+": "+w.Body.String())
		if path == "/actor" {
			var actor Actor
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &actor))
			require.Equal(t, account.LocalActorURI, actor.ID)
			require.Equal(t, preserved.KeyID, actor.PublicKey.ID)
			require.Equal(t, preserved.PublicKey, actor.PublicKey.PublicKeyPem)
			require.Equal(t, account.LocalActorURI+"/inbox", actor.Inbox)
		}
	}
	// The exact system inbox must resolve its own target before reading a body.
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Delete(account).Error)
	request := httptest.NewRequest("POST", "/actor/inbox", bytes.NewBufferString("{}"))
	request.Header.Set("Content-Type", "application/activity+json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, request)
	require.Equal(t, 404, w.Code)
	require.False(t, isImportedSystemActorPath("/actor/other"))
	require.False(t, isImportedSystemActorPath("/actors/inbox"))
}
