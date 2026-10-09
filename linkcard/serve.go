package linkcard

import (
	"net/http"
	"path"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/emojiasset"
)

var imageName = regexp.MustCompile(`^[0-9a-f]{32}\.(png|jpg|gif|webp)$`)

// ServeImage serves GET /card-images/:file from the storage layer.
func ServeImage(c *gin.Context) {
	name := c.Param("file")
	if !imageName.MatchString(name) {
		c.Status(http.StatusNotFound)
		return
	}
	kind := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}[path.Ext(name)]
	emojiasset.ServeObject(c, cardImageDir+name, name, kind, "public, max-age=31536000, immutable")
}
