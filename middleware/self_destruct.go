package middleware

import (
	"net/http"
	"slices"
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
// All other write operations are blocked.
type SelfDestructMode struct {
	mu      sync.RWMutex
	enabled bool
	loaded  bool
}

var selfDestructMode = &SelfDestructMode{}

// IsSelfDestructEnabled returns true if the server is in self-destruct mode.
func IsSelfDestructEnabled() bool {
	selfDestructMode.mu.RLock()

	if selfDestructMode.loaded {
		enabled := selfDestructMode.enabled
		selfDestructMode.mu.RUnlock()

		return enabled
	}

	selfDestructMode.mu.RUnlock()

	// 首次访问时从数据库加载
	selfDestructMode.mu.Lock()
	defer selfDestructMode.mu.Unlock()

	// Double check
	if selfDestructMode.loaded {
		return selfDestructMode.enabled
	}

	var setting models.SystemSetting
	err := db.GetDB().Where("key = ?", "self_destruct_mode").First(&setting).Error
	if err == nil {
		selfDestructMode.enabled = setting.Val == "true" || setting.Val == true //nolint:goconst
	}

	selfDestructMode.loaded = true

	return selfDestructMode.enabled
}

// RefreshSelfDestructMode forces a refresh of the self-destruct mode status.
func RefreshSelfDestructMode() {
	selfDestructMode.mu.Lock()
	defer selfDestructMode.mu.Unlock()

	selfDestructMode.enabled = false

	var setting models.SystemSetting
	err := db.GetDB().Where("key = ?", "self_destruct_mode").First(&setting).Error
	if err == nil {
		selfDestructMode.enabled = setting.Val == "true" || setting.Val == true
	}

	selfDestructMode.loaded = true
}

// SelfDestructMiddleware blocks write operations when server is in self-destruct mode.
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
			"/api/v1/exports", //nolint:goconst
			"/api/v1/backups", //nolint:goconst
		}

		// Allow read-only methods
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions {
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
		if c.Request.Method == http.MethodPost {
			exportPaths := []string{
				"/api/v1/backups",
				"/api/v1/exports",
			}
			if slices.Contains(exportPaths, path) {
				c.Next()

				return
			}
		}

		// Block all other write operations
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "This server is in self-destruct mode. Only read operations and data export are allowed.", //nolint:goconst
		})
		c.Abort()
	}
}

// SelfDestructInfoMiddleware adds a header to indicate self-destruct mode.
func SelfDestructInfoMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if IsSelfDestructEnabled() {
			c.Header("X-Self-Destruct-Mode", "true")
		}

		c.Next()
	}
}
