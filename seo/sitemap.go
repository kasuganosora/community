package seo

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/moderation"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

const (
	defaultProfileLimit = 1000
	defaultTagLimit     = 1000
)

// profileLimit and tagLimit are the wave-1 caps: the most-posted public
// local accounts, and the most recently used local hashtags. Individual
// status URLs are intentionally absent.
var (
	profileLimit = defaultProfileLimit
	tagLimit     = defaultTagLimit
	sitemapTTL   = 24 * time.Hour
)

// Stats is the sitemap the server is currently serving.
type Stats struct {
	GeneratedAt time.Time `json:"generated_at,omitempty"`
	Profiles    int       `json:"profiles"`
	Tags        int       `json:"tags"`
	URLs        int       `json:"urls"`
}

type cacheEntry struct {
	at    time.Time
	body  []byte
	stats Stats
}

var (
	sitemapMu    sync.Mutex
	sitemapCache cacheEntry
)

// Start rebuilds the sitemap at boot (when the previous copy is older than
// a day) and then once every 24 hours. Requests keep serving the last copy.
func Start(ctx context.Context) {
	go func() {
		if _, err := Refresh(ctx, false); err != nil {
			log.Warn("sitemap: %v", err)
		}
		ticker := time.NewTicker(sitemapTTL)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := Refresh(context.Background(), true); err != nil {
					log.Warn("sitemap: %v", err)
				}
			}
		}
	}()
}

// Snapshot returns the sitemap currently in memory. It does not rebuild.
func Snapshot() Stats {
	sitemapMu.Lock()
	defer sitemapMu.Unlock()
	return sitemapCache.stats
}

// Refresh returns the cached sitemap, or rebuilds it. force is the admin
// "rebuild now" path and the daily tick; a normal request reuses a copy
// newer than sitemapTTL.
func Refresh(ctx context.Context, force bool) (Stats, error) {
	sitemapMu.Lock()
	defer sitemapMu.Unlock()
	if !force && len(sitemapCache.body) > 0 && time.Since(sitemapCache.at) < sitemapTTL {
		return sitemapCache.stats, nil
	}
	body, stats, err := build(ctx, db.GetDB())
	if err != nil {
		return Stats{}, err
	}
	stats.GeneratedAt = time.Now().UTC()
	sitemapCache = cacheEntry{at: time.Now(), body: body, stats: stats}
	return stats, nil
}

// Body is the sitemap document, rebuilding when the daily copy is missing
// or older than a day.
func Body(ctx context.Context) ([]byte, error) {
	sitemapMu.Lock()
	defer sitemapMu.Unlock()
	if len(sitemapCache.body) > 0 && time.Since(sitemapCache.at) < sitemapTTL {
		return append([]byte(nil), sitemapCache.body...), nil
	}
	body, stats, err := build(ctx, db.GetDB())
	if err != nil {
		return nil, err
	}
	stats.GeneratedAt = time.Now().UTC()
	sitemapCache = cacheEntry{at: time.Now(), body: body, stats: stats}
	return append([]byte(nil), body...), nil
}

// Serve handles GET /sitemap.xml.
func Serve(c *gin.Context) {
	body, err := Body(c.Request.Context())
	if err != nil {
		c.Header("Cache-Control", "no-store")
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Header("Cache-Control", "public, max-age=300")
	c.Data(http.StatusOK, "application/xml; charset=utf-8", body)
}

// AdminStatus is GET /api/v1/admin/seo/sitemap.
func AdminStatus(c *gin.Context) {
	c.JSON(http.StatusOK, Snapshot())
}

// AdminRefresh is POST /api/v1/admin/seo/sitemap. It rebuilds immediately
// instead of waiting for the daily job.
func AdminRefresh(c *gin.Context) {
	stats, err := Refresh(c.Request.Context(), true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not rebuild the sitemap"})
		return
	}
	c.JSON(http.StatusOK, stats)
}

type urlset struct {
	XMLName xml.Name `xml:"urlset"`
	Xmlns   string   `xml:"xmlns,attr"`
	URLs    []locURL `xml:"url"`
}

type locURL struct {
	Loc string `xml:"loc"`
}

func build(ctx context.Context, conn *gorm.DB) ([]byte, Stats, error) {
	base := strings.TrimRight(systemsettings.GetBaseURL(), "/")
	locs := []string{base + "/", base + "/about"}
	stats := Stats{}
	// When visitors cannot see user content, listing profiles and tags would
	// send crawlers to sign-in walls (soft 404s). Homepage and about stay.
	if conn != nil && moderation.Current().UGCVisibilityForVisitor != moderation.UGCNone {
		names, err := publicProfiles(ctx, conn)
		if err != nil {
			return nil, Stats{}, err
		}
		stats.Profiles = len(names)
		for _, name := range names {
			locs = append(locs, profileLoc(base, name))
		}
		tagNames, err := recentTags(ctx, conn)
		if err != nil {
			return nil, Stats{}, err
		}
		stats.Tags = len(tagNames)
		for _, name := range tagNames {
			locs = append(locs, base+"/tags/"+url.PathEscape(name))
		}
	}
	stats.URLs = len(locs)
	body, err := marshalLocs(locs)
	if err != nil {
		return nil, Stats{}, err
	}
	return body, stats, nil
}

func profileLoc(base, username string) string {
	return base + "/@" + url.PathEscape(username)
}

func marshalLocs(locs []string) ([]byte, error) {
	set := urlset{Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: make([]locURL, len(locs))}
	for i, loc := range locs {
		set.URLs[i] = locURL{Loc: loc}
	}
	body, err := xml.MarshalIndent(set, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), body...), nil
}

// publicProfiles returns up to profileLimit local accounts that visitors
// may see, ordered by how many public posts they have written.
func publicProfiles(ctx context.Context, conn *gorm.DB) ([]string, error) {
	if profileLimit <= 0 {
		return nil, nil
	}
	type row struct {
		Username string
	}
	var rows []row
	err := conn.WithContext(ctx).Model(&models.Account{}).
		Select("accounts.username").
		Joins("JOIN statuses ON statuses.account_id = accounts.id AND statuses.deleted_at IS NULL AND statuses.scheduled_at IS NULL AND statuses.visibility = ? AND (statuses.reblog_id IS NULL OR statuses.reblog_id = 0) AND statuses.domain IS NULL", models.StatusVisibilityPublic).
		Where("accounts.domain IS NULL AND accounts.is_ban = ? AND accounts.no_index = ? AND accounts.require_signin_to_view = ? AND accounts.deletion_requested_at IS NULL", false, false, false).
		Group("accounts.id, accounts.username").
		Order("COUNT(statuses.id) DESC, accounts.id ASC").
		Limit(profileLimit).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Username != "" {
			names = append(names, row.Username)
		}
	}
	return names, nil
}

// recentTags returns up to tagLimit hashtags used on a public local post,
// most recently used first. Hidden tags are left out so the sitemap only
// lists pages that are allowed to be indexed.
//
// tags.last_status_used_at is never written: new posts and imports only
// bump status_count. Requiring it to be set dropped every tag. Recency is
// the newest public local status that still links the tag.
func recentTags(ctx context.Context, conn *gorm.DB) ([]string, error) {
	if tagLimit <= 0 {
		return nil, nil
	}
	type row struct {
		Name string
	}
	var rows []row
	err := conn.WithContext(ctx).Table("tags").
		Select("tags.name").
		Joins("JOIN status_tags ON status_tags.tag_id = tags.id").
		Joins("JOIN statuses ON statuses.id = status_tags.status_id AND statuses.deleted_at IS NULL AND statuses.scheduled_at IS NULL AND statuses.visibility = ? AND (statuses.reblog_id IS NULL OR statuses.reblog_id = 0) AND statuses.domain IS NULL", models.StatusVisibilityPublic).
		Joins("JOIN accounts ON accounts.id = statuses.account_id AND accounts.domain IS NULL AND accounts.deleted_at IS NULL AND accounts.is_ban = ? AND accounts.require_signin_to_view = ?", false, false).
		Group("tags.id, tags.name").
		Order("MAX(statuses.created_at) DESC, tags.id DESC").
		Limit(tagLimit + 100).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, tagLimit)
	policy := moderation.Current()
	for _, row := range rows {
		if row.Name == "" || policy.IsHiddenTag(row.Name) {
			continue
		}
		names = append(names, row.Name)
		if len(names) == tagLimit {
			break
		}
	}
	return names, nil
}
