package middleware

import (
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm/clause"
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
	err := db.GetDB().Where(clause.Eq{Column: "key", Value: "self_destruct_mode"}).First(&setting).Error
	if err == nil {
		selfDestructMode.enabled = isSelfDestructValue(setting.Val)
	}

	selfDestructMode.loaded = true

	return selfDestructMode.enabled
}

// isSelfDestructValue 判断 SystemSetting.Val 是否表示启用了自毁模式.
func isSelfDestructValue(val any) bool {
	switch v := val.(type) {
	case string:
		return v == "true"
	case bool:
		return v
	default:
		return false
	}
}

// RefreshSelfDestructMode forces a refresh of the self-destruct mode status.
func RefreshSelfDestructMode() {
	selfDestructMode.mu.Lock()
	defer selfDestructMode.mu.Unlock()

	selfDestructMode.enabled = false

	var setting models.SystemSetting
	err := db.GetDB().Where(clause.Eq{Column: "key", Value: "self_destruct_mode"}).First(&setting).Error
	if err == nil {
		selfDestructMode.enabled = isSelfDestructValue(setting.Val)
	}

	selfDestructMode.loaded = true
}

// selfDestructAllowedPaths 自毁模式下允许的路径前缀（包级常量，避免每次请求重新分配）.
var selfDestructAllowedPaths = []string{
	"/api/v1/accounts/verify_credentials",
	"/api/v1/accounts/update_credentials",
	"/api/v1/instance",
	"/oauth/",
	"/auth/",
	"/api/v1/exports",
	"/api/v1/backups",
}

// selfDestructExportPaths 自毁模式下允许的精确 POST 路径.
var selfDestructExportPaths = []string{
	"/api/v1/backups",
	"/api/v1/exports",
}

// SelfDestructMiddleware blocks write operations when server is in self-destruct mode.
func SelfDestructMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !IsSelfDestructEnabled() {
			c.Next()

			return
		}

		// Allow read-only methods
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions {
			c.Next()

			return
		}

		// Check if path is allowed (prefix match)
		path := c.Request.URL.Path
		for _, allowedPath := range selfDestructAllowedPaths {
			if strings.HasPrefix(path, allowedPath) {
				c.Next()

				return
			}
		}

		// Allow data export endpoints (exact match)
		if c.Request.Method == http.MethodPost {
			if slices.Contains(selfDestructExportPaths, path) {
				c.Next()

				return
			}
		}

		// Block all other write operations
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "This server is in self-destruct mode. Only read operations and data export are allowed.",
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
