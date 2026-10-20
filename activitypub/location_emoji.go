package activitypub

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kasuganosora/community/models"
)

const (
	maxLocationEmoji     = 5
	locationEmojiTimeout = 8 * time.Second
)

// fetchMisskeyEmoji resolves one custom emoji of a Misskey-compatible server
// by name (POST /api/emoji). Overridable in tests.
var fetchMisskeyEmoji = func(ctx context.Context, host, name string) (string, error) {
	body, _ := json.Marshal(map[string]string{"name": name})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+host+"/api/emoji", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := NewSafeHTTPClientWithConnectTimeout(locationEmojiTimeout, 2, 5*time.Second).Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", nil
	}
	var out struct {
		Name      string `json:"name"`
		URL       string `json:"url"`
		LocalOnly bool   `json:"localOnly"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil || out.Name != name || out.LocalOnly {
		return "", nil //nolint:nilerr // not a Misskey emoji answer
	}
	return out.URL, nil
}

// addLocationEmojiTags appends Emoji tags for custom emoji used only in the
// actor's Misskey location (vcard:Address). Misskey does not list those in
// the actor's tag array, so they are looked up on the actor's own server.
// Failures are ignored: the shortcode then stays plain text.
func addLocationEmojiTags(ctx context.Context, actor *Actor) {
	if actor == nil || strings.TrimSpace(actor.VcardAddress) == "" {
		return
	}
	u, err := url.Parse(actor.ID)
	if err != nil || u.Hostname() == "" {
		return
	}
	known := map[string]bool{}
	for _, t := range decodeEmojiTags(actor.Tag) {
		known[t.Name] = true
	}
	var extra []any
	for _, code := range models.MatchEmojiTags(remoteLocation(actor)) {
		if known[code] || len(extra) >= maxLocationEmoji || emojiName.FindStringSubmatch(code) == nil {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, locationEmojiTimeout)
		icon, err := fetchMisskeyEmoji(cctx, u.Host, strings.Trim(code, ":"))
		cancel()
		if err != nil || icon == "" || !safeEmojiURL(icon) {
			continue
		}
		known[code] = true
		extra = append(extra, Tag{Type: "Emoji", Name: code, Icon: &Image{Type: "Image", URL: icon}})
	}
	if len(extra) == 0 {
		return
	}
	var tags []any
	switch t := actor.Tag.(type) {
	case nil:
	case []any:
		tags = append(tags, t...)
	default:
		tags = append(tags, t)
	}
	actor.Tag = append(tags, extra...)
}
