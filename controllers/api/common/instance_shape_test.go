package common

import (
	"encoding/json"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var semverRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

func fetchInstance(t *testing.T, v1 bool) map[string]any {
	t.Helper()

	path, fn := "/api/v2/instance", GetInstanceDetail
	if v1 {
		path, fn = "/api/v1/instance", GetInstanceV1
	}

	c, w := newGetContext(t, path)
	callHandler(fn, c)
	require.Equal(t, http.StatusOK, w.Code)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))

	return raw
}

func requireKeys(t *testing.T, m map[string]any, keys ...string) {
	t.Helper()

	for _, k := range keys {
		assert.Contains(t, m, k)
	}
}

func sub(t *testing.T, m map[string]any, k string) map[string]any {
	t.Helper()

	v, ok := m[k].(map[string]any)
	require.True(t, ok, "%s must be an object, got %#v", k, m[k])

	return v
}

func arr(t *testing.T, m map[string]any, k string) []any {
	t.Helper()

	v, ok := m[k].([]any)
	require.True(t, ok, "%s must be a non-null array, got %#v", k, m[k])

	return v
}

func setupInstanceShape(t *testing.T) {
	t.Helper()
	setupTest(t)
	systemsettings.Set(systemsettings.KeyDomain, "social.example.org")
}

func TestInstanceV1Shape(t *testing.T) {
	setupInstanceShape(t)

	raw := fetchInstance(t, true)
	requireKeys(t, raw, "uri", "title", "short_description", "description", "email", "version",
		"urls", "stats", "thumbnail", "languages", "registrations", "approval_required",
		"invites_enabled", "configuration", "contact_account", "rules")

	assert.Equal(t, "social.example.org", raw["uri"])
	assert.Regexp(t, semverRe, raw["version"])

	streaming, _ := sub(t, raw, "urls")["streaming_api"].(string)
	assert.Regexp(t, `^wss?://[^/]+$`, streaming)

	requireKeys(t, sub(t, raw, "stats"), "user_count", "status_count", "domain_count")
	assert.IsType(t, true, raw["registrations"])
	assert.NotNil(t, raw["thumbnail"])
	assert.NotNil(t, arr(t, raw, "languages"))
	assert.Empty(t, arr(t, raw, "rules"))

	cfg := sub(t, raw, "configuration")
	requireKeys(t, sub(t, cfg, "statuses"), "max_characters", "max_media_attachments", "characters_reserved_per_url")
	requireKeys(t, sub(t, cfg, "media_attachments"), "supported_mime_types", "image_size_limit", "image_matrix_limit",
		"video_size_limit", "video_frame_rate_limit", "video_matrix_limit")
	arr(t, sub(t, cfg, "media_attachments"), "supported_mime_types")
	requireKeys(t, sub(t, cfg, "polls"), "max_options", "max_characters_per_option", "min_expiration", "max_expiration")
}

func TestInstanceV2Shape(t *testing.T) {
	setupInstanceShape(t)

	raw := fetchInstance(t, false)
	requireKeys(t, raw, "domain", "title", "version", "source_url", "description", "usage", "thumbnail",
		"icon", "languages", "configuration", "registrations", "api_versions", "contact", "rules")

	assert.Regexp(t, semverRe, raw["version"])
	assert.Equal(t, float64(2), sub(t, raw, "api_versions")["mastodon"])
	requireKeys(t, sub(t, sub(t, raw, "usage"), "users"), "active_month")
	assert.NotEmpty(t, sub(t, raw, "thumbnail")["url"])
	arr(t, raw, "icon")
	arr(t, raw, "languages")
	assert.Empty(t, arr(t, raw, "rules"))

	cfg := sub(t, raw, "configuration")
	urls := sub(t, cfg, "urls")
	assert.Regexp(t, `^wss?://[^/]+$`, urls["streaming"])
	assert.Contains(t, urls, "status")
	assert.Nil(t, urls["status"])
	requireKeys(t, sub(t, cfg, "vapid"), "public_key")
	requireKeys(t, sub(t, cfg, "accounts"), "max_featured_tags")
	requireKeys(t, sub(t, cfg, "translation"), "enabled")
	arr(t, sub(t, cfg, "media_attachments"), "supported_mime_types")

	requireKeys(t, sub(t, raw, "registrations"), "enabled", "approval_required", "message", "url")
	requireKeys(t, sub(t, raw, "contact"), "email", "account")
}

func TestInstanceRulesAreStringIDs(t *testing.T) {
	setupInstanceShape(t)
	require.NoError(t, db.GetDB().Create(&models.Rule{Text: "Be kind"}).Error)

	for _, v1 := range []bool{true, false} {
		rules := arr(t, fetchInstance(t, v1), "rules")
		require.Len(t, rules, 1)

		r, _ := rules[0].(map[string]any)
		assert.IsType(t, "", r["id"])
		assert.Equal(t, "Be kind", r["text"])
	}
}

func TestStreamingURLFromBaseURL(t *testing.T) {
	setupInstanceShape(t)

	assert.Equal(t, "wss://social.example.org", StreamingURL())
}

func TestInstanceDefaultImagesArePNG(t *testing.T) {
	setupInstanceShape(t)

	v2 := fetchInstance(t, false)
	thumb := sub(t, v2, "thumbnail")
	assert.Regexp(t, `\.png$`, thumb["url"])
	versions := sub(t, thumb, "versions")
	assert.Regexp(t, `\.png$`, versions["@1x"])
	assert.Regexp(t, `\.png$`, versions["@2x"])

	icons := arr(t, v2, "icon")
	require.NotEmpty(t, icons)

	for _, i := range icons {
		m, _ := i.(map[string]any)
		assert.Regexp(t, `\.png$`, m["src"])
		assert.Regexp(t, `^\d+x\d+$`, m["size"])
	}

	assert.Regexp(t, `\.png$`, fetchInstance(t, true)["thumbnail"])

	// An SVG thumbnail configured by the admin is ignored (iOS cannot render it).
	systemsettings.Set(systemsettings.KeyThumbnail, "https://x.example/t.svg")
	assert.Regexp(t, `\.png$`, sub(t, fetchInstance(t, false), "thumbnail")["url"])
}

func TestInstanceTextIsTrimmed(t *testing.T) {
	setupInstanceShape(t)
	systemsettings.Set(systemsettings.KeyTitle, "Community \u3000")
	systemsettings.Set(systemsettings.KeyDescription, "\u3000hello ")

	for _, v1 := range []bool{true, false} {
		raw := fetchInstance(t, v1)
		assert.Equal(t, "Community", raw["title"])
		assert.Equal(t, "hello", raw["description"])
	}
}

func TestInstanceContactAccountIsFullAccount(t *testing.T) {
	setupInstanceShape(t)

	gdb := db.GetDB()
	user := &models.User{UUID: "u-1", UserName: "luna", EmailHash: "h1", IsAdmin: true}
	require.NoError(t, gdb.Create(user).Error)

	created := time.Date(2026, 10, 9, 0, 16, 37, 131129000, time.UTC)
	acc := &models.Account{Username: "luna", UserId: user.Id, CreatedAt: &created, LastStatusAt: &created}
	require.NoError(t, gdb.Create(acc).Error)

	for _, v1 := range []bool{true, false} {
		raw := fetchInstance(t, v1)

		var a map[string]any
		if v1 {
			a = sub(t, raw, "contact_account")
		} else {
			a = sub(t, sub(t, raw, "contact"), "account")
		}

		requireKeys(t, a, "id", "username", "acct", "url", "uri", "display_name", "note", "avatar", "avatar_static",
			"header", "header_static", "locked", "fields", "emojis", "bot", "group", "discoverable", "indexable",
			"created_at", "last_status_at", "statuses_count", "followers_count", "following_count", "roles")
		assert.Equal(t, "2026-10-09T00:00:00.000Z", a["created_at"])
		assert.Equal(t, "2026-10-09", a["last_status_at"])
		assert.Regexp(t, `^https?://.+\.png$`, a["avatar"])
		assert.Regexp(t, `^https?://.+\.png$`, a["header_static"])
		assert.NotContains(t, a, "avatar_decoration_id")
		assert.IsType(t, "", a["id"])
		arr(t, a, "fields")
		arr(t, a, "emojis")
		arr(t, a, "roles")
	}
}
