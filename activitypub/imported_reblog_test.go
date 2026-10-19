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
	for path, want := range map[string]int{"/notes/renote2": 404, "/notes/renote2/activity": 404, "/notes/902/activity": 404, "/users/booster/statuses/902": 404} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", base+path, nil)
		req.Header.Set("Accept", "application/activity+json")
		router.ServeHTTP(w, req)
		require.Equal(t, want, w.Code, path)
	}
}
