package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/feed"
)

func FeedAdmissionMiddleware() gin.HandlerFunc { return feedAdmissionMiddleware(feed.AdmitWrite) }

func feedAdmissionMiddleware(admit func(context.Context) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		producesStatus := c.Request.Method == http.MethodPost && (path == "/api/v1/statuses" || path == "/api/v2/statuses" || ((strings.HasPrefix(path, "/api/v1/statuses/") || strings.HasPrefix(path, "/api/v2/statuses/")) && (strings.HasSuffix(path, "/replace") || strings.HasSuffix(path, "/reblog"))))
		if producesStatus && admit(c.Request.Context()) != nil {
			c.Header("Retry-After", "5")
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "timeline delivery is busy; retry shortly"})
			return
		}
		c.Next()
	}
}
