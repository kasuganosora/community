package middleware

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// SelfDestructMode middleware restricts user actions when server is in self-destruct mode
// Users can only:
// - Log in
// - View their own data
// - Download their data (export)
// All other write operations are blocked
type SelfDestructMode struct {
	enabled     bool
	enabledOnce sync.Once
}

var selfDestructMode = &SelfDestructMode{}

// IsSelfDestructEnabled returns true if the server is in self-destruct mode
func IsSelfDestructEnabled() bool {
	selfDestructMode.enabledOnce.Do(func() {
		// Check database for self-destruct mode setting
		var setting models.SystemSetting
		if err := db.GetDB().Where("key = ?", "self_destruct_mode").First(&setting).Error; err == nil {
			selfDestructMode.enabled = setting.Val == "true" || setting.Val == true
		}
	})
	return selfDestructMode.enabled
}

// RefreshSelfDestructMode forces a refresh of the self-destruct mode status
func RefreshSelfDestructMode() {
	selfDestructMode.enabledOnce = sync.Once{}
	selfDestructMode.enabled = false
	
	var setting models.SystemSetting
	if err := db.GetDB().Where("key = ?", "self_destruct_mode").First(&setting).Error; err == nil {
		selfDestructMode.enabled = setting.Val == "true" || setting.Val == true
	}
}

// SelfDestructMiddleware blocks write operations when server is in self-destruct mode
func SelfDestructMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !IsSelfDestructEnabled() {
			c.Next()
			return
		}

		// Allow certain paths even in self-destruct mode
		allowedPaths := []string{
			"/api/v1/accounts/verify_credentials",
			"/api/v1/accounts/update_credentials",
			"/api/v1/instance",
			"/oauth/",
			"/auth/",
			"/api/v1/exports",
			"/api/v1/backups",
		}

		// Allow read-only methods
		if c.Request.Method == "GET" || c.Request.Method == "HEAD" || c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		// Check if path is allowed
		path := c.Request.URL.Path
		for _, allowedPath := range allowedPaths {
			if len(path) >= len(allowedPath) && path[:len(allowedPath)] == allowedPath {
				c.Next()
				return
			}
		}

		// Allow data export endpoints
		if c.Request.Method == "POST" {
			exportPaths := []string{
				"/api/v1/backups",
				"/api/v1/exports",
			}
			for _, exportPath := range exportPaths {
				if path == exportPath {
					c.Next()
					return
				}
			}
		}

		// Block all other write operations
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "This server is in self-destruct mode. Only read operations and data export are allowed.",
		})
		c.Abort()
	}
}

// SelfDestructInfoMiddleware adds a header to indicate self-destruct mode
func SelfDestructInfoMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if IsSelfDestructEnabled() {
			c.Header("X-Self-Destruct-Mode", "true")
		}
		c.Next()
	}
}
