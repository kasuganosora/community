package activitypub

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

// ImportedWebAliases handles only exact preserved local source URLs. Middleware
// keeps ordinary SPA routes intact and never rewrites the signed request URL.
func ImportedWebAliases() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet || !strings.HasPrefix(c.Request.URL.Path, "/@") {
			c.Next()
			return
		}
		parts := strings.Split(strings.TrimPrefix(c.Request.URL.Path, "/@"), "/")
		ap := strings.Contains(c.GetHeader("Accept"), "application/activity+json") || strings.Contains(c.GetHeader("Accept"), "application/ld+json")
		if len(parts) == 1 && parts[0] != "" && ap {
			var account models.Account
			if db.GetDB().Preload("Extra").Preload("Emojis").Where("username = ? AND domain IS NULL", parts[0]).First(&account).Error != nil {
				c.AbortWithStatus(404)
				return
			}
			actor, err := buildActorProfile(&account, localAccountEmojiTags(&account, systemsettings.GetBaseURL()))
			if err != nil {
				c.AbortWithStatus(500)
				return
			}
			c.Header("Vary", "Accept")
			c.Header("Content-Type", "application/activity+json")
			c.JSON(200, actor)
			c.Abort()
			return
		}
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			c.Next()
			return
		}
		uri := strings.TrimRight(systemsettings.GetBaseURL(), "/") + c.Request.URL.Path
		var status models.Status
		if db.GetDB().Preload("Account").Where("origin_url = ? AND deleted_at IS NULL AND scheduled_at IS NULL", uri).First(&status).Error != nil || status.Account == nil || status.Account.Domain != nil || !status.IsLocal() || status.Account.Username != parts[0] {
			c.Next()
			return
		}
		c.Header("Vary", "Accept")
		if ap {
			StatusObject(c)
			c.Abort()
			return
		}
		if status.Visibility != models.StatusVisibilityPublic && status.Visibility != models.StatusVisibilityUnlisted {
			c.AbortWithStatus(404)
			return
		}
		c.Redirect(http.StatusFound, "/@"+status.Account.Username+"/status/"+strconv.FormatUint(status.Id, 10))
		c.Abort()
	}
}
