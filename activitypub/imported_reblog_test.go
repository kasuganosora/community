package activitypub

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Imported Misskey pure renotes keep /notes/<id> and are served as the
// Announce Misskey published (id <uri>/activity); c.hime.at's own boosts
// (no preserved object URI) are not reachable this way.
func TestImportedReblogServedAsAnnounce(t *testing.T) {
	conn := outboundDB(t)
	raw := conn.Session(&gorm.Session{SkipHooks: true})
	base := systemsettings.GetBaseURL()
	booster := outboundAccount(t, conn, "booster", false)
	author := outboundAccount(t, conn, "author", true)
	now := time.Now()
	uri := "https://remote.example/notes/orig"
	original := models.Status{Id: 900, AccountId: author.Id, OriginUri: &uri, Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, raw.Create(&original).Error)
	rid := original.Id
	boost := models.Status{Id: 901, AccountId: booster.Id, ReblogId: &rid, LocalObjectURI: base + "/notes/renote1", Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now}
	own := models.Status{Id: 902, AccountId: booster.Id, ReblogId: &rid, Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now}
	private := models.Status{Id: 903, AccountId: booster.Id, ReblogId: &rid, LocalObjectURI: base + "/notes/renote2", Visibility: models.StatusVisibilityPrivate, CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, raw.Create(&boost).Error)
	require.NoError(t, raw.Create(&own).Error)
	require.NoError(t, raw.Create(&private).Error)
	router := gin.New()
	router.GET("/notes/:id", ImportedStatusObject)
	router.GET("/notes/:id/activity", ImportedStatusActivity)
	router.GET("/users/:username/statuses/:id", StatusObject)
	router.GET("/users/:username/statuses/:id/activity", LocalStatusActivity)
	for _, path := range []string{"/notes/renote1", "/notes/renote1/activity"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", base+path, nil)
		req.Header.Set("Accept", "application/activity+json")
		router.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, path+": "+w.Body.String())
		require.Contains(t, w.Header().Get("Content-Type"), "application/activity+json")
		var a map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &a))
		require.Equal(t, "Announce", a["type"])
		require.Equal(t, base+"/notes/renote1/activity", a["id"])
		require.Equal(t, uri, a["object"])
		require.Equal(t, booster.ActorURI(base), a["actor"])
	}
	// Native boosts answer at their status URI and at the Announce id their
	// delivery used (<status uri>/activity), with the same document.
	for _, path := range []string{"/users/booster/statuses/902", "/users/booster/statuses/902/activity"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", base+path, nil)
		req.Header.Set("Accept", "application/activity+json")
		router.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, path+": "+w.Body.String())
		var a map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &a))
		require.Equal(t, "Announce", a["type"])
		require.Equal(t, base+"/users/booster/statuses/902/activity", a["id"])
		require.Equal(t, uri, a["object"])
		own.Reblog = &original
		own.Reblog.Account = author
		delivered, id := announcePayload(&own, booster, booster.ActorURI(base), "", false)
		require.Equal(t, id, a["id"], "same id as the delivered Announce")
		require.Equal(t, delivered["object"], a["object"])
	}
	privateOwn := models.Status{Id: 904, AccountId: booster.Id, ReblogId: &rid, Visibility: models.StatusVisibilityPrivate, CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, raw.Create(&privateOwn).Error)
	hiddenID := uint64(905)
	hidden := models.Status{Id: hiddenID, AccountId: author.Id, OriginUri: &[]string{"https://remote.example/notes/hidden"}[0], Visibility: models.StatusVisibilityPrivate, CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, raw.Create(&hidden).Error)
	ofHidden := models.Status{Id: 906, AccountId: booster.Id, ReblogId: &hiddenID, Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, raw.Create(&ofHidden).Error)
	for path, want := range map[string]int{"/notes/renote2": 404, "/notes/renote2/activity": 404, "/notes/902/activity": 404,
		"/users/booster/statuses/904": 404, "/users/booster/statuses/904/activity": 404, "/users/booster/statuses/906": 404,
		"/users/booster/statuses/906/activity": 404, "/users/author/statuses/902": 404, "/users/booster/statuses/901/activity": 404} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", base+path, nil)
		req.Header.Set("Accept", "application/activity+json")
		router.ServeHTTP(w, req)
		require.Equal(t, want, w.Code, path)
	}
}
