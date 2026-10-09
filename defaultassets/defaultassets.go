// Package defaultassets embeds the raster fallbacks (PNG) that Mastodon API
// documents must reference instead of empty URLs: default avatar/header and
// the instance thumbnail/icons. iOS clients cannot render the SVG logo.
package defaultassets

import (
	"embed"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed files/*.png
var files embed.FS

// Prefix is the URL path prefix under which the assets are served.
const Prefix = "/default-assets/"

// Path returns the URL path of an embedded asset (e.g. "avatar.png").
func Path(name string) string { return Prefix + name }

// Handler serves GET /default-assets/:name.
func Handler(c *gin.Context) {
	name := strings.TrimPrefix(c.Param("name"), "/")

	data, err := files.ReadFile("files/" + name)
	if err != nil || strings.Contains(name, "/") {
		c.Status(http.StatusNotFound)

		return
	}

	c.Header("Cache-Control", "public, max-age=86400")
	c.Data(http.StatusOK, "image/png", data)
}
