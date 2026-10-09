package activitypub

import (
	"context"
	"encoding/json"
	"mime"
	"net"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
)

// Incoming media remains on its origin server. Resolve and validate its address,
// but never download potentially large files while accepting an ActivityPub event.
func safeAttachmentURL(ctx context.Context, raw string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(raw) > 500 || remoteMediaURL(raw) != nil {
		return false, nil
	}
	u, _ := url.Parse(raw)
	peers, err := configuredLabPeers()
	if err != nil {
		return false, err
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	var ips []net.IPAddr
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		ips = []net.IPAddr{{IP: ip}}
	} else {
		ips, err = net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
		if err != nil {
			return false, err
		}
	}
	_, err = selectDialIP(u.Hostname(), port, ips, peers)
	return err == nil, nil
}

func incomingAttachments(ctx context.Context, value any, accountID uint64) (models.Attachments, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		entries = []json.RawMessage{raw}
	}
	if len(entries) > 16 {
		entries = entries[:16]
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result := models.Attachments{}
	for _, entry := range entries {
		var a struct {
			Type      string `json:"type"`
			MediaType string `json:"mediaType"`
			URL       any    `json:"url"`
			Name      string `json:"name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			Blurhash  string `json:"blurhash"`
			// Misskey and Mastodon mark single files as sensitive.
			Sensitive bool `json:"sensitive"`
		}
		if json.Unmarshal(entry, &a) != nil {
			continue
		}
		switch a.Type {
		case "Document", "Image", "Video", "Audio":
		default:
			continue
		}
		rawURL, _ := a.URL.(string)
		if link, ok := a.URL.(map[string]any); ok {
			rawURL, _ = link["href"].(string)
		}
		allowed, err := safeAttachmentURL(ctx, rawURL)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		mediaType, _, err := mime.ParseMediaType(a.MediaType)
		if err != nil {
			continue
		}
		// Never expose active document formats as embedded media.
		switch mediaType {
		case "image/png", "image/jpeg", "image/gif", "image/webp", "image/apng", "video/mp4", "video/webm", "video/quicktime", "audio/mpeg", "audio/ogg", "audio/wav", "audio/mp4", "application/pdf", "text/plain", "application/octet-stream":
		default:
			continue
		}
		u, _ := url.Parse(rawURL)
		filename := path.Base(u.Path)
		if filename == "." || filename == "/" || len(filename) > 255 {
			filename = "attachment"
		}
		desc := htmlsafe.StripTags(a.Name)
		if len(desc) > 10000 {
			desc = string([]rune(desc)[:min(len([]rune(desc)), 2500)])
		}
		att := &models.Attachment{Sensitive: a.Sensitive || urlMarkedSensitive(rawURL), AccountId: accountID, FileName: filename, RemoteUrl: rawURL, ContentType: mediaType, Type: models.GetMediaType(mediaType), Description: desc}
		if mediaType == "image/apng" {
			att.Type = models.MediaTypeImage
		}
		if a.Width > 0 && a.Width <= 100000 {
			att.Width = &a.Width
		}
		if a.Height > 0 && a.Height <= 100000 {
			att.Height = &a.Height
		}
		if len(a.Blurhash) <= 50 && !strings.ContainsAny(a.Blurhash, "\r\n") {
			att.Blurhash = a.Blurhash
		}
		result = append(result, att)
	}
	return result, nil
}

// urlMarkedSensitive reports Misskey's "?sensitive=true" media URL marker.
func urlMarkedSensitive(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	v := strings.ToLower(u.Query().Get("sensitive"))

	return v == "true" || v == "1"
}

// deriveSensitive is a remote status's effective sensitive flag: the
// Note's own flag, a content warning (like Mastodon) or any sensitive file.
func deriveSensitive(noteSensitive bool, summary string, attachments models.Attachments) bool {
	if noteSensitive || strings.TrimSpace(summary) != "" {
		return true
	}
	for _, a := range attachments {
		if a != nil && a.Sensitive {
			return true
		}
	}

	return false
}
