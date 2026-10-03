package transfer

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/systemsettings"
)

// ServeMediaAlias is registered only on GET /system/*file and /files/*file.
// The origin is trusted server configuration, never a request Host header.
func ServeMediaAlias(c *gin.Context) {
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery || strings.Contains(strings.ToLower(c.Request.URL.EscapedPath()), "%2f") || strings.Contains(strings.ToLower(c.Request.URL.EscapedPath()), "%5c") {
		c.Status(http.StatusNotFound)
		return
	}
	target, err := datatransfer.ResolveMediaAlias(db.GetDB().WithContext(c.Request.Context()), systemsettings.GetBaseURL(), c.Request.URL.Path)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusTemporaryRedirect, target)
}
