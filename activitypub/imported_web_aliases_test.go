package activitypub

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestImportedWebAliasesExactURLAndAudience(t *testing.T) {
	conn := outboundDB(t)
	author := outboundAccount(t, conn, "alias_author", false)
	base := systemsettings.GetBaseURL()
	uri := base + "/@alias_author/source123"
	now := time.Now()
	status := models.Status{AccountId: author.Id, LocalObjectURI: base + "/notes/opaque", OriginUrl: &uri, Visibility: models.StatusVisibilityPublic, CreatedAt: &now, Content: models.StatusContent{Content: "source body"}}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&status).Error)
	router := gin.New()
	router.Use(ImportedWebAliases())
	router.GET("/notes/:id", ImportedStatusObject)
	router.NoRoute(func(c *gin.Context) { c.String(404, "spa fallback") })
	request := func(path, accept string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", base+path, nil)
		r.Header.Set("Accept", accept)
		router.ServeHTTP(w, r)
		return w
	}
	w := request("/notes/opaque", "text/html")
	require.Equal(t, 302, w.Code)
	w = request("/@alias_author/source123", "text/html")
	require.Equal(t, 302, w.Code)
	require.Contains(t, w.Header().Get("Location"), "/@alias_author/status/")
	w = request("/@alias_author/source123", "application/activity+json")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), base+"/notes/opaque")
	require.Equal(t, 404, request("/@alias_author/other", "text/html").Code)
	require.NoError(t, conn.Model(&status).Update("visibility", models.StatusVisibilityDirect).Error)
	require.Equal(t, 404, request("/@alias_author/source123", "text/html").Code)
	require.Equal(t, 401, request("/@alias_author/source123", "application/activity+json").Code)
	require.NoError(t, conn.Model(&status).Update("local_only", true).Error)
	require.Equal(t, 404, request("/@alias_author/source123", "application/activity+json").Code)
}
