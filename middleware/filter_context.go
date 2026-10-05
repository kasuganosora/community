package middleware

import (
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
)

var statusDetailPath = regexp.MustCompile(`/statuses/\d+$`)

// AttachFilterContext tells status serialization which Mastodon filter context
// the current request belongs to. An empty context still matches every filter.
func AttachFilterContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		if name := statusFilterContext(c.Request.URL.Path); name != "" {
			c.Request = c.Request.WithContext(models.WithFilterContext(c.Request.Context(), name))
		}
		c.Next()
	}
}

func statusFilterContext(path string) string {
	switch {
	case strings.Contains(path, "/timelines/home"), strings.Contains(path, "/timelines/list/"), strings.Contains(path, "/bookmarks"), strings.Contains(path, "/favourites"):
		return "home"
	case strings.Contains(path, "/timelines/public"), strings.Contains(path, "/timelines/tag/"):
		return "public"
	case strings.Contains(path, "/notifications"):
		return "notifications"
	case strings.Contains(path, "/accounts/") && strings.Contains(path, "/statuses"):
		return "account"
	case strings.HasSuffix(path, "/context") && strings.Contains(path, "/statuses/"), statusDetailPath.MatchString(path):
		return "thread"
	default:
		return ""
	}
}
