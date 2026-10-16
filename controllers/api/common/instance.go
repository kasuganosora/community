package common

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	mediamanagement "github.com/kasuganosora/community/controllers/api/media"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/systemsettings"
)

// InstanceResponse is the Mastodon v2 instance document (GET /api/v2/instance).
type InstanceResponse struct {
	Domain      string `json:"domain"`
	Title       string `json:"title"`
	Version     string `json:"version"`
	SourceUrl   string `json:"source_url"`
	Description string `json:"description"`
	Usage       struct {
		Users struct {
			ActiveMonth int `json:"active_month"`
		} `json:"users"`
	} `json:"usage"`

	// Stats is a v1 field kept on v2 for the web frontend; clients ignore it.
	Stats InstanceStats `json:"stats"`

	Thumbnail InstanceThumbnail `json:"thumbnail"`
	Icon      []InstanceIcon    `json:"icon"`

	Languages     []string `json:"languages"`
	// DefaultLocale is the instance UI language chosen in the setup wizard;
	// the web frontend uses it when the browser matches no supported one.
	DefaultLocale string `json:"default_locale,omitempty"`
	Configuration struct {
		Urls struct {
			Streaming string  `json:"streaming"`
			Status    *string `json:"status"`
		} `json:"urls"`
		Vapid struct {
			PublicKey string `json:"public_key"`
		} `json:"vapid"`
		Accounts struct {
			MaxFeaturedTags   int `json:"max_featured_tags"`
			MaxPinnedStatuses int `json:"max_pinned_statuses"`
		} `json:"accounts"`

		Statuses         InstanceStatusesConfig `json:"statuses"`
		MediaAttachments InstanceMediaConfig    `json:"media_attachments"`
		Polls            InstancePollsConfig    `json:"polls"`

		Translation struct {
			Enabled bool `json:"enabled"`
		} `json:"translation"`
		TimelinesAccess map[string]map[string]string `json:"timelines_access"`
	} `json:"configuration"`

	Registrations struct {
		MinAge           *int    `json:"min_age"`
		Enabled          bool    `json:"enabled"`
		ApprovalRequired bool    `json:"approval_required"`
		InviteRequired   bool    `json:"invite_required"`
		Message          *string `json:"message"`
		Url              *string `json:"url"`
	} `json:"registrations"`

	APIVersions struct {
		Mastodon int `json:"mastodon"`
	} `json:"api_versions"`

	Contact struct {
		Email   string              `json:"email"`
		Account *serializer.Account `json:"account"`
	} `json:"contact"`

	Rules []InstanceRule `json:"rules"`

	// Branding is a non-Mastodon extension carrying Misskey's branding meta
	// (iconUrl, themeColor, entrancePageStyle, ...); clients ignore it.
	Branding systemsettings.Branding `json:"branding"`
}

// InstanceThumbnail is the v2 thumbnail object.
type InstanceThumbnail struct {
	Url      string            `json:"url"`
	Blurhash string            `json:"blurhash,omitempty"`
	Versions map[string]string `json:"versions,omitempty"`
}

// InstanceIcon is one entry of the v2 icon array.
type InstanceIcon struct {
	Src  string `json:"src"`
	Size string `json:"size"`
}

type InstanceStatusesConfig struct {
	MaxCharacters            int      `json:"max_characters"`
	MaxMediaAttachments      int      `json:"max_media_attachments"`
	CharactersReservedPerUrl int      `json:"characters_reserved_per_url"`
	SupportedMimeTypes       []string `json:"supported_mime_types"`
}

type InstanceMediaConfig struct {
	DescriptionLimit    int      `json:"description_limit"`
	SupportedMimeTypes  []string `json:"supported_mime_types"`
	ImageSizeLimit      int      `json:"image_size_limit"`
	ImageMatrixLimit    int      `json:"image_matrix_limit"`
	VideoSizeLimit      int      `json:"video_size_limit"`
	VideoFrameRateLimit int      `json:"video_frame_rate_limit"`
	VideoMatrixLimit    int      `json:"video_matrix_limit"`
}

type InstancePollsConfig struct {
	MaxOptions             int `json:"max_options"`
	MaxCharactersPerOption int `json:"max_characters_per_option"`
	MinExpiration          int `json:"min_expiration"`
	MaxExpiration          int `json:"max_expiration"`
}

// InstanceRule is a server rule; ids are strings like every Mastodon id.
type InstanceRule struct {
	Id   string `json:"id"`
	Text string `json:"text"`
	Hint string `json:"hint"`
}

// InstanceV1Response is the legacy Mastodon v1 instance document
// (GET /api/v1/instance) that older clients (Toot!, Tusky, ...) still use to
// validate a server.
type InstanceV1Response struct {
	Uri              string `json:"uri"`
	Title            string `json:"title"`
	ShortDescription string `json:"short_description"`
	Description      string `json:"description"`
	Email            string `json:"email"`
	Version          string `json:"version"`
	Urls             struct {
		StreamingApi string `json:"streaming_api"`
	} `json:"urls"`
	Stats         InstanceStats `json:"stats"`
	Thumbnail     *string       `json:"thumbnail"`
	Languages     []string      `json:"languages"`
	Registrations bool          `json:"registrations"`
	// ApprovalRequired and InvitesEnabled follow Mastodon v1 naming.
	ApprovalRequired bool `json:"approval_required"`
	InvitesEnabled   bool `json:"invites_enabled"`
	Configuration    struct {
		Accounts struct {
			MaxFeaturedTags   int `json:"max_featured_tags"`
			MaxPinnedStatuses int `json:"max_pinned_statuses"`
		} `json:"accounts"`
		Statuses         InstanceStatusesConfig `json:"statuses"`
		MediaAttachments InstanceMediaConfig    `json:"media_attachments"`
		Polls            InstancePollsConfig    `json:"polls"`
	} `json:"configuration"`
	ContactAccount *serializer.Account `json:"contact_account"`
	Rules          []InstanceRule      `json:"rules"`
}

// StreamingURL derives the public websocket base URL (ws/wss) from the
// site's public base URL. It never returns an empty string.
func StreamingURL() string {
	base := strings.TrimRight(systemsettings.GetBaseURL(), "/")

	switch {
	case strings.HasPrefix(base, "https://"):
		return "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		return "ws://" + strings.TrimPrefix(base, "http://")
	default:
		return "wss://" + systemsettings.GetDomain()
	}
}

func loadInstanceRules() []InstanceRule {
	out := make([]InstanceRule, 0)

	var rules []*models.Rule

	err := db.GetDB().Model(&models.Rule{}).Order("priority desc, created_at asc").Find(&rules).Error
	if err != nil {
		return out
	}

	for _, r := range rules {
		out = append(out, InstanceRule{Id: strconv.FormatUint(r.Id, 10), Text: r.Text})
	}

	return out
}

// loadContactAccount returns the first local admin account, or nil.
func loadContactAccount() *serializer.Account {
	var found []*models.Account

	err := db.GetDB().Joins("JOIN users ON users.id = accounts.user_id").
		Where("accounts.domain IS NULL AND users.is_admin = ?", true).
		Order("accounts.id asc").Limit(1).Find(&found).Error
	if err != nil || len(found) == 0 {
		return nil
	}

	acc := found[0]

	acc.Extra = &models.AccountExtra{}
	_ = db.GetDB().Model(acc).Association("Extra").Find(acc.Extra)

	return serializer.ToAccount(acc)
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}

	return in
}

func fillStatuses(c *InstanceStatusesConfig) {
	c.MaxCharacters = systemsettings.GetInt(systemsettings.KeyStatusMaxCharacters, nil)
	c.MaxMediaAttachments = systemsettings.GetInt(systemsettings.KeyStatusMaxMedia, nil)
	c.CharactersReservedPerUrl = systemsettings.GetInt(systemsettings.KeyStatusCharactersPerURL, nil)
	c.SupportedMimeTypes = []string{"text/plain"}
}

func fillMedia(c *InstanceMediaConfig) {
	c.DescriptionLimit = 1500
	c.SupportedMimeTypes = nonNilStrings(mediamanagement.SupportedMimeTypes())
	c.ImageSizeLimit = int(systemsettings.MediaImageSizeLimit())
	c.ImageMatrixLimit = systemsettings.GetInt(systemsettings.KeyMediaImageMatrixLimit, nil)
	c.VideoSizeLimit = int(systemsettings.MediaVideoSizeLimit())
	c.VideoFrameRateLimit = systemsettings.GetInt(systemsettings.KeyMediaVideoFrameRate, nil)
	c.VideoMatrixLimit = systemsettings.GetInt(systemsettings.KeyMediaVideoMatrixLimit, nil)
}

func fillPolls(c *InstancePollsConfig) {
	c.MaxOptions = systemsettings.GetInt(systemsettings.KeyPollMaxOptions, nil)
	c.MaxCharactersPerOption = systemsettings.GetInt(systemsettings.KeyPollMaxCharactersPerOption, nil)
	c.MinExpiration = systemsettings.GetInt(systemsettings.KeyPollMinExpiration, nil)
	c.MaxExpiration = systemsettings.GetInt(systemsettings.KeyPollMaxExpiration, nil)
}

// customThumbnail returns the admin-configured thumbnail when it is a
// raster image (iOS clients cannot render SVG), else "".
func customThumbnail() string {
	t := strings.TrimSpace(systemsettings.GetString(systemsettings.KeyThumbnail, ""))
	if t == "" || strings.HasSuffix(strings.ToLower(strings.SplitN(t, "?", 2)[0]), ".svg") {
		return ""
	}

	return t
}

// thumbnailURL returns the configured thumbnail or the bundled PNG; always
// an absolute URL (Swift clients decode this field as URL).
func thumbnailURL() string {
	if t := customThumbnail(); t != "" {
		return t
	}

	return serializer.DefaultAssetURL("thumbnail@2x.png")
}

func instanceThumbnail() InstanceThumbnail {
	if t := customThumbnail(); t != "" {
		return InstanceThumbnail{Url: t}
	}

	return InstanceThumbnail{
		Url: serializer.DefaultAssetURL("thumbnail@2x.png"),
		Versions: map[string]string{
			"@1x": serializer.DefaultAssetURL("thumbnail.png"),
			"@2x": serializer.DefaultAssetURL("thumbnail@2x.png"),
		},
	}
}

func instanceIcons() []InstanceIcon {
	b := systemsettings.GetBranding()
	if b.App192IconURL != "" || b.App512IconURL != "" {
		var icons []InstanceIcon
		if b.App192IconURL != "" {
			icons = append(icons, InstanceIcon{Src: b.App192IconURL, Size: "192x192"})
		}
		if b.App512IconURL != "" {
			icons = append(icons, InstanceIcon{Src: b.App512IconURL, Size: "512x512"})
		}

		return icons
	}

	return []InstanceIcon{
		{Src: serializer.DefaultAssetURL("icon-64.png"), Size: "64x64"},
		{Src: serializer.DefaultAssetURL("icon-192.png"), Size: "192x192"},
		{Src: serializer.DefaultAssetURL("icon-512.png"), Size: "512x512"},
	}
}

// cleanText trims and normalises exotic whitespace (U+3000, NBSP) that
// admins paste into the title/description.
func cleanText(s string) string {
	s = strings.NewReplacer("\u3000", " ", "\u00a0", " ").Replace(s)

	return strings.TrimSpace(s)
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}

func contactEmail() string {
	return systemsettings.GetString(systemsettings.KeySiteContactEmail, "admin@admin.com")
}

// GetInstanceDetail handles GET /api/v2/instance.
func GetInstanceDetail(c *gin.Context) {
	resp := InstanceResponse{}
	resp.Domain = systemsettings.GetDomain()
	resp.Title = cleanText(systemsettings.GetString(systemsettings.KeyTitle, ""))
	resp.Version = config.GetInstanceVersion()
	resp.SourceUrl = "https://github.com/kasuganosora/community"
	resp.Description = cleanText(systemsettings.GetString(systemsettings.KeyDescription, ""))
	resp.Usage.Users.ActiveMonth = getActiveUsersCount()
	resp.Stats = getInstanceStats()
	resp.Thumbnail = instanceThumbnail()
	resp.Icon = instanceIcons()
	resp.Languages = nonNilStrings(systemsettings.GetStrings(systemsettings.KeyLanguages, []string{"zh-CN", "en-US"}))
	resp.DefaultLocale = systemsettings.GetString(systemsettings.KeyDefaultLocale, "")
	resp.Configuration.Urls.Streaming = StreamingURL()
	resp.Configuration.Urls.Status = nil
	resp.Configuration.Vapid.PublicKey = ""
	resp.Configuration.Accounts.MaxFeaturedTags = systemsettings.GetInt(systemsettings.KeyAccountMaxFeaturedTags, nil)
	resp.Configuration.Accounts.MaxPinnedStatuses = 5
	fillStatuses(&resp.Configuration.Statuses)
	fillMedia(&resp.Configuration.MediaAttachments)
	fillPolls(&resp.Configuration.Polls)
	resp.Configuration.TimelinesAccess = timelinesAccess()
	resp.Configuration.Translation.Enabled = systemsettings.GetBool(systemsettings.KeyIsEnableTranslation, nil)
	resp.Registrations.Enabled = systemsettings.GetBool(systemsettings.KeyIsEnableRegistration, false)
	resp.Registrations.ApprovalRequired = systemsettings.GetBool(systemsettings.KeyRegistrationApprovalRequired, false)
	resp.Registrations.InviteRequired = systemsettings.GetBool(systemsettings.KeyRegistrationInviteRequired, false)
	resp.Registrations.Message = strPtr(systemsettings.GetString(systemsettings.KeyRegistrationMessage, ""))
	resp.APIVersions.Mastodon = config.MastodonAPIVersionNumber
	resp.Contact.Email = contactEmail()
	resp.Contact.Account = loadContactAccount()
	resp.Rules = loadInstanceRules()
	resp.Branding = systemsettings.GetBranding()
	resp.SourceUrl = resp.Branding.RepositoryURL

	c.JSON(200, resp)
}

// GetInstanceV1 handles GET /api/v1/instance.
func GetInstanceV1(c *gin.Context) {
	resp := InstanceV1Response{}
	resp.Uri = systemsettings.GetDomain()
	resp.Title = cleanText(systemsettings.GetString(systemsettings.KeyTitle, ""))
	resp.Description = cleanText(systemsettings.GetString(systemsettings.KeyDescription, ""))
	resp.ShortDescription = resp.Description
	resp.Email = contactEmail()
	resp.Version = config.GetInstanceVersion()
	resp.Urls.StreamingApi = StreamingURL()
	resp.Stats = getInstanceStats()
	resp.Thumbnail = strPtr(thumbnailURL())
	resp.Languages = nonNilStrings(systemsettings.GetStrings(systemsettings.KeyLanguages, []string{"zh-CN", "en-US"}))
	resp.Registrations = systemsettings.GetBool(systemsettings.KeyIsEnableRegistration, false)
	resp.ApprovalRequired = systemsettings.GetBool(systemsettings.KeyRegistrationApprovalRequired, false)
	resp.InvitesEnabled = systemsettings.GetBool(systemsettings.KeyRegistrationInviteRequired, false)
	resp.Configuration.Accounts.MaxFeaturedTags = systemsettings.GetInt(systemsettings.KeyAccountMaxFeaturedTags, nil)
	resp.Configuration.Accounts.MaxPinnedStatuses = 5
	fillStatuses(&resp.Configuration.Statuses)
	fillMedia(&resp.Configuration.MediaAttachments)
	fillPolls(&resp.Configuration.Polls)
	resp.ContactAccount = loadContactAccount()
	resp.Rules = loadInstanceRules()

	c.JSON(200, resp)
}

// getActiveUsersCount returns the number of active users in the last 30 days.
func getActiveUsersCount() int {
	// Count users who have logged in or posted in the last 30 days
	var count int64

	thirtyDaysAgo := time.Now().AddDate(0, 0, -30)

	// Count local accounts with recent activity
	db.GetDB().Model(&models.Account{}).
		Where("domain IS NULL").
		Where("last_activity_at >= ? OR last_status_at >= ? OR created_at >= ?",
			thirtyDaysAgo, thirtyDaysAgo, thirtyDaysAgo).
		Count(&count)

	return int(count)
}

// InstanceStats mirrors Mastodon's v1 Instance#stats.
type InstanceStats struct {
	UserCount   int64 `json:"user_count"`
	StatusCount int64 `json:"status_count"`
	DomainCount int64 `json:"domain_count"`
}

const instanceStatsTTL = 5 * time.Minute

var instanceStatsCache struct {
	sync.Mutex
	value InstanceStats
	at    time.Time
}

// getInstanceStats counts local accounts, local statuses and known remote
// domains. The counts are cached briefly because the endpoint is public and
// requested on every landing page view.
func getInstanceStats() InstanceStats {
	instanceStatsCache.Lock()
	defer instanceStatsCache.Unlock()

	if !instanceStatsCache.at.IsZero() && time.Since(instanceStatsCache.at) < instanceStatsTTL {
		return instanceStatsCache.value
	}

	var stats InstanceStats

	gdb := db.GetDB()
	gdb.Model(&models.Account{}).Where("domain IS NULL").Count(&stats.UserCount)
	gdb.Model(&models.Status{}).Where("domain IS NULL AND deleted_at IS NULL").Count(&stats.StatusCount)
	gdb.Model(&models.Account{}).Where("domain IS NOT NULL").Distinct("domain").Count(&stats.DomainCount)

	instanceStatsCache.value = stats
	instanceStatsCache.at = time.Now()

	return stats
}

func timelinesAccess() map[string]map[string]string {
	pub := map[string]string{"local": "public", "remote": "public"}

	return map[string]map[string]string{
		"live_feeds":     pub,
		"hashtag_feeds":  pub,
		"trending_links": pub,
	}
}
