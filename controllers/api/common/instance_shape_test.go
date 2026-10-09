package common

import (
	"encoding/json"
	"net/http"
	"regexp"
	"testing"

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
