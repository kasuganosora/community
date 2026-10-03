package activitypub

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var emojiName = regexp.MustCompile(`^:([a-zA-Z0-9_]{1,36}):$`)

// Import metadata only: media fetching, if requested later, still uses the
// existing SSRF-protected federation transport. Never fetch arbitrary tag IDs.
func safeEmojiURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || len(raw) > 255 {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && isBannedIP(ip) {
		return false
	}
	return true
}

func decodeEmojiTags(value any) []Tag {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		entries = []json.RawMessage{raw}
	}
	if len(entries) > 100 {
		entries = entries[:100]
	}
	tags := make([]Tag, 0, len(entries))
	for _, entry := range entries {
		var tag Tag
		if json.Unmarshal(entry, &tag) == nil && tag.Type == "Emoji" {
			tags = append(tags, tag)
		}
	}
	return tags
}

// importEmojiTags is bounded and keyed by the authenticated author's server,
// never by attacker-selected tag IDs or icon CDN hosts.
func importEmojiTags(tx *gorm.DB, domain, text string, value any) (models.Emojis, error) {
	result := make(models.Emojis, 0)
	if domain == "" {
		return result, nil
	}
	used := map[string]bool{}
	for _, n := range models.MatchEmojiTags(models.EmojiText(text)) {
		used[n] = true
	}
	seen := map[string]bool{}
	tags := decodeEmojiTags(value)
	if len(tags) > 100 {
		tags = tags[:100]
	}
	for _, tag := range tags {
		match := emojiName.FindStringSubmatch(tag.Name)
		if tag.Type != "Emoji" || match == nil || !used[tag.Name] || seen[tag.Name] || tag.Icon == nil || !safeEmojiURL(tag.Icon.URL) {
			continue
		}
		seen[tag.Name] = true
		mediaType := tag.Icon.MediaType
		if mediaType != "" && !strings.HasPrefix(mediaType, "image/") {
			continue
		}
		license := ""
		if tag.MisskeyLicense != nil {
			license = tag.MisskeyLicense.FreeText
		}
		if len(license) > 10000 {
			license = license[:10000]
		}
		e := models.Emoji{Shortcode: match[1], Domain: domain, Url: tag.Icon.URL, StaticUrl: tag.Icon.URL, ContentType: mediaType, License: license, VisibleInPicker: false}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "shortcode"}, {Name: "domain"}}, DoUpdates: clause.AssignmentColumns([]string{"url", "static_url", "content_type", "license", "updated_at"})}).Create(&e).Error; err != nil {
			return nil, err
		}
		if err := tx.Where("shortcode = ? AND domain = ?", e.Shortcode, domain).First(&e).Error; err != nil {
			return nil, err
		}
		if !e.Disabled {
			result = append(result, &e)
		}
	}
	return result, nil
}

func importAccountEmojiTags(tx *gorm.DB, account *models.Account, tags any) error {
	domain := ""
	if account.Domain != nil {
		domain = *account.Domain
	}
	emojis, err := importEmojiTags(tx, domain, models.AccountEmojiText(account), tags)
	if err != nil {
		return err
	}
	// Missing tags means an empty authoritative set, not a shortcode lookup.
	if err := tx.Model(account).Association("Emojis").Replace(emojis); err != nil {
		return err
	}
	account.Emojis = emojis
	return nil
}

func emojiTags(emojis []*models.Emoji, text, baseURL string) []Tag {
	tags := make([]Tag, 0)
	for _, e := range models.EmojisInText(emojis, text, "", true) {
		if !safeEmojiURL(e.Url) {
			continue
		}
		tags = append(tags, Tag{Type: "Emoji", ID: fmt.Sprintf("%s/emojis/%s", strings.TrimRight(baseURL, "/"), e.Shortcode), Name: ":" + e.Shortcode + ":", Updated: time.Unix(e.UpdatedAt, 0).UTC().Format(time.RFC3339), Icon: &Image{Type: "Image", URL: e.Url, MediaType: e.ContentType}, MisskeyLicense: &EmojiLicense{FreeText: e.License}})
	}
	return tags
}

func localAccountEmojiTags(account *models.Account, baseURL string) []Tag {
	text := models.AccountEmojiText(account)
	emojis := account.Emojis
	if len(models.MatchEmojiTags(models.EmojiText(text))) > 0 && db.GetDB() != nil {
		if found, err := models.GetEmojiFromText(db.GetDB(), text); err == nil {
			for _, e := range found {
				emojis = append(emojis, e)
			}
		}
	}
	return emojiTags(emojis, text, baseURL)
}

func emojiContext() []any {
	return []any{"https://www.w3.org/ns/activitystreams", "https://w3id.org/security/v1", map[string]any{"toot": "http://joinmastodon.org/ns#", "Emoji": "toot:Emoji", "_misskey_license": map[string]any{"@id": "https://misskey-hub.net/ns#_misskey_license", "@type": "@json"}}}
}

func persistIncomingStatusWithEmoji(ctx context.Context, conn *gorm.DB, status *models.Status, tags any) error {
	return conn.Transaction(func(tx *gorm.DB) error {
		domain := ""
		if status.Domain != nil {
			domain = *status.Domain
		}
		emojis, err := importEmojiTags(tx, domain, status.Content.Content+" "+status.Content.SpoilerText, tags)
		if err != nil {
			return err
		}
		status.Emojis = emojis
		return persistIncomingStatus(models.WithStructuredEmojiIDs(ctx, emojis.GetIds()), tx, status)
	})
}
