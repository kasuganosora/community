package activitypub

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

const (
	nodeInfoSchema20 = "http://nodeinfo.diaspora.software/ns/schema/2.0"
	nodeInfoSchema21 = "http://nodeinfo.diaspora.software/ns/schema/2.1"
	// nodeInfoSoftwareName must match ^[a-z0-9-]+$.
	nodeInfoSoftwareName = "community"
		nodeInfoUsageTTL     = 10 * time.Minute
)

// NodeInfoMatrixEnabled reports whether the Matrix homeserver is running. It
// is wired by the router (the homeserver package imports activitypub, so it
// cannot be referenced directly).
var NodeInfoMatrixEnabled = func() bool { return false }

// NodeInfo handles GET /.well-known/nodeinfo: the discovery document listing
// both supported schema versions.
func NodeInfo(c *gin.Context) {
	baseURL := systemsettings.GetBaseURL()

	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Access-Control-Allow-Origin", "*")
	c.JSON(200, gin.H{
		"links": []gin.H{
			{"rel": nodeInfoSchema21, "href": baseURL + "/nodeinfo/2.1"},
			{"rel": nodeInfoSchema20, "href": baseURL + "/nodeinfo/2.0"}, //nolint:goconst
		},
	})
}

// NodeInfo20 handles GET /nodeinfo/2.0.
func NodeInfo20(c *gin.Context) { writeNodeInfo(c, "2.0") }

// NodeInfo21 handles GET /nodeinfo/2.1.
func NodeInfo21(c *gin.Context) { writeNodeInfo(c, "2.1") }

type nodeInfoUsers struct {
	Total          int64 `json:"total"`
	ActiveMonth    int64 `json:"activeMonth"`
	ActiveHalfyear int64 `json:"activeHalfyear"`
}

type nodeInfoUsage struct {
	Users      nodeInfoUsers `json:"users"`
	LocalPosts int64         `json:"localPosts"`
}

var nodeInfoUsageCache struct {
	sync.Mutex
	value nodeInfoUsage
	at    time.Time
}

// resetNodeInfoCache clears cached usage (tests).
func resetNodeInfoCache() {
	nodeInfoUsageCache.Lock()
	nodeInfoUsageCache.at = time.Time{}
	nodeInfoUsageCache.Unlock()
}

// nodeInfoUsageStats counts real local users (accounts owned by a user, so
// service actors such as system.proxy are excluded) and local posts. The
// endpoint is public and polled by every peer, so results are cached.
func nodeInfoUsageStats() nodeInfoUsage {
	nodeInfoUsageCache.Lock()
	defer nodeInfoUsageCache.Unlock()

	if !nodeInfoUsageCache.at.IsZero() && time.Since(nodeInfoUsageCache.at) < nodeInfoUsageTTL {
		return nodeInfoUsageCache.value
	}

	var usage nodeInfoUsage

	gdb := db.GetDB()
	users := func() *gorm.DB {
		return gdb.Model(&models.Account{}).Where("domain IS NULL AND user_id IS NOT NULL AND user_id <> 0")
	}
	active := func(since time.Time) int64 {
		var n int64
		users().Where("last_activity_at >= ? OR last_status_at >= ?", since, since).Count(&n)

		return n
	}

	users().Count(&usage.Users.Total)
	now := time.Now()
	usage.Users.ActiveMonth = active(now.AddDate(0, 0, -30))
	usage.Users.ActiveHalfyear = active(now.AddDate(0, 0, -180))
	gdb.Model(&models.Status{}).Where("domain IS NULL").Count(&usage.LocalPosts)

	nodeInfoUsageCache.value = usage
	nodeInfoUsageCache.at = time.Now()

	return usage
}

func writeNodeInfo(c *gin.Context, version string) {
	software := gin.H{
		"name":    nodeInfoSoftwareName,
		"version": config.GetVersion(),
	}
	brand := systemsettings.GetBranding()
	if version == "2.1" {
		software["repository"] = brand.RepositoryURL
		software["homepage"] = brand.RepositoryURL
	}

	// The schema's protocol enum has no "matrix"; Matrix support is
	// advertised in metadata instead so strict validators keep working.
	metadata := gin.H{
		"nodeName":        systemsettings.GetString(systemsettings.KeyTitle, systemsettings.GetDomain()),
		"nodeDescription": systemsettings.GetString(systemsettings.KeyDescription, ""),
		"matrix":          NodeInfoMatrixEnabled(),
		// Misskey-compatible branding: remote Misskey/Sharkey servers read
		// metadata.themeColor for the instance ticker.
		"repositoryUrl": brand.RepositoryURL,
		"feedbackUrl":   nilIfEmpty(brand.FeedbackURL),
		"themeColor":    nilIfEmpty(brand.ThemeColor),
		"iconUrl":       nilIfEmpty(brand.IconURL),
		"bannerUrl":     nilIfEmpty(brand.BannerURL),
	}
	if strings.TrimSpace(metadata["nodeName"].(string)) == "" {
		metadata["nodeName"] = systemsettings.GetDomain()
	}

	body := gin.H{
		"version":           version,
		"software":          software,
		"protocols":         []string{"activitypub"},
		"services":          gin.H{"inbound": []string{}, "outbound": []string{}},
		"openRegistrations": systemsettings.GetBool(systemsettings.KeyIsEnableRegistration, false),
		"usage":             nodeInfoUsageStats(),
		"metadata":          metadata,
	}

	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Cache-Control", "public, max-age=1800")
	data, err := json.Marshal(body)
	if err != nil {
		c.Status(500)

		return
	}

	c.Data(200, `application/json; profile="http://nodeinfo.diaspora.software/ns/schema/`+version+`#"`, data)
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}

	return s
}
