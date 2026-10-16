package activitypub

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// Instance icon discovery (background worker only). Sources, in order:
//  1. Misskey family: nodeinfo metadata iconUrl, else POST /api/meta iconUrl
//  2. Mastodon API: /api/v2/instance icon[] (smallest size >= 64)
//  3. Homepage <link rel="icon"> / apple-touch-icon
//  4. /favicon.ico, verified to be an image
//
// All requests go through the SSRF-safe remoteReactionClient with its
// per-request timeout; bodies are size-limited.
const (
	instanceIconMinSize  = 64
	instanceIconHTMLMax  = 512 << 10
	instanceIconProbeMax = 64 << 10
	instanceIconURLMax   = 1024
)

// validIconURL resolves ref against base and returns an absolute http(s)
// URL, or "" when unusable.
func validIconURL(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "data:") {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	u.Fragment = ""
	s := u.String()
	if len(s) > instanceIconURLMax {
		return ""
	}
	return s
}

// isSVGIcon reports icons the avatar proxy cannot rasterise.
func isSVGIcon(typ, href string) bool {
	if strings.Contains(strings.ToLower(typ), "svg") {
		return true
	}
	u, err := url.Parse(href)
	return err == nil && strings.EqualFold(path.Ext(u.Path), ".svg")
}

// iconSize parses "64x64" / "any" style size tokens and returns the largest
// width found (0 when unknown).
func iconSize(sizes string) int {
	best := 0
	for _, tok := range strings.Fields(strings.ToLower(sizes)) {
		w, _, ok := strings.Cut(tok, "x")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(w); err == nil && n > best {
			best = n
		}
	}
	return best
}

type iconCandidate struct {
	href  string
	size  int
	apple bool
	order int
}

// pickIcon chooses the smallest candidate >= instanceIconMinSize; otherwise
// an apple-touch-icon (typically 180px); otherwise the largest known size;
// otherwise the first declared.
func pickIcon(cands []iconCandidate) string {
	var best *iconCandidate
	for i := range cands {
		c := &cands[i]
		if c.size >= instanceIconMinSize && (best == nil || c.size < best.size) {
			best = c
		}
	}
	if best == nil {
		for i := range cands {
			if cands[i].apple && cands[i].size == 0 {
				best = &cands[i]
				break
			}
		}
	}
	if best == nil {
		for i := range cands {
			c := &cands[i]
			if best == nil || c.size > best.size {
				best = c
			}
		}
	}
	if best == nil {
		return ""
	}
	return best.href
}

// parseHTMLIcon extracts the best icon link from a homepage. <base href>
// is honoured for relative URLs; SVG icons are skipped.
func parseHTMLIcon(page string, body []byte) string {
	base, err := url.Parse(page)
	if err != nil {
		return ""
	}
	z := html.NewTokenizer(bytes.NewReader(body))
	var cands []iconCandidate
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		tok := z.Token()
		switch tok.Data {
		case "body":
			return pickIcon(cands)
		case "base":
			for _, a := range tok.Attr {
				if a.Key == "href" {
					if b, e := url.Parse(strings.TrimSpace(a.Val)); e == nil {
						base = base.ResolveReference(b)
					}
				}
			}
		case "link":
			var rel, href, sizes, typ string
			for _, a := range tok.Attr {
				switch a.Key {
				case "rel":
					rel = strings.ToLower(a.Val)
				case "href":
					href = a.Val
				case "sizes":
					sizes = a.Val
				case "type":
					typ = a.Val
				}
			}
			isIcon, apple := false, false
			for _, r := range strings.Fields(rel) {
				switch r {
				case "icon":
					isIcon = true
				case "apple-touch-icon", "apple-touch-icon-precomposed":
					isIcon, apple = true, true
				}
			}
			if !isIcon {
				continue
			}
			abs := validIconURL(base, href)
			if abs == "" || isSVGIcon(typ, abs) {
				continue
			}
			size := iconSize(sizes)
			cands = append(cands, iconCandidate{href: abs, size: size, apple: apple, order: len(cands)})
		}
	}
	return pickIcon(cands)
}

// remoteFetch performs a bounded request and returns status, content type
// and up to max bytes of body.
func remoteFetch(ctx context.Context, method, rawURL, accept string, max int64) (int, string, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, remoteReactionTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return 0, "", nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "Community/1.0 ActivityPub Client")
	resp, err := remoteReactionClient.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()
	var body []byte
	if method != http.MethodHead {
		body, _ = io.ReadAll(io.LimitReader(resp.Body, max))
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), body, nil
}

// mastodonIcon reads /api/v2/instance icon[] and picks the smallest >= 64.
func mastodonIcon(ctx context.Context, base string) string {
	var inst struct {
		Icon []struct {
			Src  string `json:"src"`
			Size string `json:"size"`
		} `json:"icon"`
	}
	if remoteGetJSON(ctx, http.MethodGet, base+"/api/v2/instance", nil, &inst) != nil {
		return ""
	}
	b, _ := url.Parse(base)
	var cands []iconCandidate
	for i, ic := range inst.Icon {
		if abs := validIconURL(b, ic.Src); abs != "" {
			cands = append(cands, iconCandidate{href: abs, size: iconSize(ic.Size), order: i})
		}
	}
	return pickIcon(cands)
}

func homepageIcon(ctx context.Context, base string) string {
	status, ct, body, err := remoteFetch(ctx, http.MethodGet, base+"/", "text/html,application/xhtml+xml", instanceIconHTMLMax)
	if err != nil || status/100 != 2 || (ct != "" && !strings.Contains(strings.ToLower(ct), "html")) {
		return ""
	}
	return parseHTMLIcon(base+"/", body)
}

// looksLikeImage accepts image/* (except SVG) or recognised raster magic.
func looksLikeImage(ct string, head []byte) bool {
	ct = strings.ToLower(ct)
	if strings.HasPrefix(ct, "image/") {
		return !strings.Contains(ct, "svg")
	}
	if len(head) == 0 {
		return false
	}
	sniff := http.DetectContentType(head)
	return strings.HasPrefix(sniff, "image/") && !strings.Contains(sniff, "svg")
}

func faviconIcon(ctx context.Context, base string) string {
	u := base + "/favicon.ico"
	if status, ct, _, err := remoteFetch(ctx, http.MethodHead, u, "image/*", 0); err == nil && status/100 == 2 && looksLikeImage(ct, nil) {
		return u
	}
	status, ct, body, err := remoteFetch(ctx, http.MethodGet, u, "image/*", instanceIconProbeMax)
	if err == nil && status/100 == 2 && looksLikeImage(ct, body) {
		return u
	}
	return ""
}

// discoverInstanceIcon runs the discovery chain for an instance whose
// nodeinfo has already been read. It also returns the Misskey /api/meta name
// when that endpoint was consulted (used when nodeinfo has no nodeName).
func discoverInstanceIcon(ctx context.Context, base, software string, nodeinfoMeta map[string]any) (icon, name string) {
	b, _ := url.Parse(base)
	if softwareFamily(software) == softwareMisskey {
		if s, ok := nodeinfoMeta["iconUrl"].(string); ok {
			icon = validIconURL(b, s)
		}
		var m struct {
			IconURL string `json:"iconUrl"`
			Name    string `json:"name"`
		}
		if remoteGetJSON(ctx, http.MethodPost, base+"/api/meta", map[string]any{"detail": false}, &m) == nil {
			name = m.Name
			if icon == "" {
				icon = validIconURL(b, m.IconURL)
			}
		}
	} else {
		icon = mastodonIcon(ctx, base)
	}
	if icon == "" && ctx.Err() == nil {
		icon = homepageIcon(ctx, base)
	}
	if icon == "" && ctx.Err() == nil {
		icon = faviconIcon(ctx, base)
	}
	return icon, name
}
