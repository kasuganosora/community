package activitypub

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
	"strings"
)

// localActorQuery resolves exact preserved identity first; migrated actors never
// silently acquire a second identity derived from their login name.
func localActorQuery(conn *gorm.DB, uri string) *gorm.DB {
	base := strings.TrimRight(systemsettings.GetBaseURL(), "/")
	name := ""
	if strings.HasPrefix(uri, base+"/users/") {
		candidate := strings.TrimPrefix(uri, base+"/users/")
		if candidate != "" && !strings.ContainsAny(candidate, "/?#%") {
			name = candidate
		}
	}
	if name == "" {
		return conn.Where("domain IS NULL AND local_actor_uri = ? AND local_actor_uri <> ''", uri)
	}
	return conn.Where("domain IS NULL AND (local_actor_uri = ? OR (username = ? AND (local_actor_uri = '' OR local_actor_uri IS NULL)))", uri, name)
}
func localActorRouteQuery(conn *gorm.DB, c *gin.Context) *gorm.DB {
	if isImportedSystemActorPath(c.Request.URL.Path) {
		return localActorQuery(conn, InstanceActorURI())
	}
	prefix := "/users/"
	if strings.HasPrefix(c.Request.URL.Path, "/ap/users/") {
		prefix = "/ap/users/"
	}
	return localActorQuery(conn, strings.TrimRight(systemsettings.GetBaseURL(), "/")+prefix+c.Param("username"))
}

func isImportedSystemActorPath(path string) bool {
	switch path {
	case "/actor", "/actor/inbox", "/actor/outbox", "/actor/followers", "/actor/following", "/actor/collections/featured":
		return true
	}
	return false
}
