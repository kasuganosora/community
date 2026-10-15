package activitypub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/mediaproxy"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

// Misskey only delivers reactions to the note author's server and the
// reactors' followers, so reactions from third servers never reach us. For a
// remote status detail we ask the origin server for its counts and merge them
// with local reactions. The result is display-only and never persisted.

const (
	remoteReactionTimeout  = 3 * time.Second
	remoteReactionTTL      = 5 * time.Minute
	remoteSoftwareTTL      = 6 * time.Hour
	remoteSoftwareFailTTL  = 10 * time.Minute
	remoteReactionMaxBytes = 1 << 20
	remoteReactionCacheMax = 2048
)

// Software families with a reaction API we know how to read.
const (
	softwareMisskey  = "misskey"
	softwarePleroma  = "pleroma"
	softwareFedibird = "fedibird"
)

var (
	remoteReactionClient = NewSafeHTTPClient(remoteReactionTimeout, 2)
	// remoteReactionBase builds the origin for a host; tests point it at httptest.
	remoteReactionBase = func(host string) string { return "https://" + host }
)

// softwareFamily maps a nodeinfo software.name to a reaction API family.
func softwareFamily(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "misskey", "sharkey", "firefish", "calckey", "cherrypick", "foundkey", "iceshrimp", "meisskey", "catodon", "magnetar", "rosekey":
		return softwareMisskey
	case "pleroma", "akkoma":
		return softwarePleroma
	case "fedibird", "kmyblue", "hometown":
		return softwareFedibird
	}
	return ""
}

type ttlEntry struct {
	value   any
	expires time.Time
}

type ttlCache struct {
	sync.Mutex
	m map[string]ttlEntry
}

func (c *ttlCache) get(key string) (any, bool) {
	c.Lock()
	defer c.Unlock()
	e, ok := c.m[key]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.value, true
}

func (c *ttlCache) set(key string, v any, ttl time.Duration) {
	c.Lock()
	defer c.Unlock()
	if c.m == nil || len(c.m) >= remoteReactionCacheMax {
		c.m = map[string]ttlEntry{}
	}
	c.m[key] = ttlEntry{value: v, expires: time.Now().Add(ttl)}
}

var (
	softwareCache       ttlCache
	remoteReactionCache ttlCache
)

func resetRemoteReactionCaches() {
	softwareCache = ttlCache{}
	remoteReactionCache = ttlCache{}
}

func remoteGetJSON(ctx context.Context, method, rawURL string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = strings.NewReader(string(b))
	}
	ctx, cancel := context.WithTimeout(ctx, remoteReactionTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Community/1.0 ActivityPub Client")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := remoteReactionClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return &url.Error{Op: method, URL: rawURL, Err: io.ErrUnexpectedEOF}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, remoteReactionMaxBytes)).Decode(out)
}

// remoteSoftware returns the reaction API family of host ("" if unknown),
// cached so a status detail costs at most one nodeinfo lookup per host.
func remoteSoftware(ctx context.Context, host string) string {
	host = strings.ToLower(host)
	if v, ok := softwareCache.get(host); ok {
		return v.(string)
	}
	base := remoteReactionBase(host)
	var wk struct {
		Links []struct {
			Rel  string `json:"rel"`
			Href string `json:"href"`
		} `json:"links"`
	}
	family := ""
	err := remoteGetJSON(ctx, http.MethodGet, base+"/.well-known/nodeinfo", nil, &wk)
	if err == nil {
		href := ""
		for _, l := range wk.Links {
			if strings.HasPrefix(l.Rel, "http://nodeinfo.diaspora.software/ns/schema/") {
				href = l.Href
			}
		}
		b, _ := url.Parse(base)
		if u, perr := url.Parse(href); perr == nil && href != "" && strings.EqualFold(u.Host, b.Host) {
			var ni struct {
				Software struct {
					Name string `json:"name"`
				} `json:"software"`
			}
			if err = remoteGetJSON(ctx, http.MethodGet, href, nil, &ni); err == nil {
				family = softwareFamily(ni.Software.Name)
			}
		}
	}
	ttl := remoteSoftwareTTL
	if err != nil {
		ttl = remoteSoftwareFailTTL
	}
	softwareCache.set(host, family, ttl)
	return family
}

// remoteNoteID extracts the origin's own status id from the stored URI/URL.
func remoteNoteID(family, host string, status *models.Status) string {
	cands := []string{}
	if status.OriginUri != nil {
		cands = append(cands, *status.OriginUri)
	}
	if status.OriginUrl != nil {
		cands = append(cands, *status.OriginUrl)
	}
	for _, raw := range cands {
		u, err := url.Parse(raw)
		if err != nil || !strings.EqualFold(u.Hostname(), host) {
			continue
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 2 {
			continue
		}
		last := parts[len(parts)-1]
		prev := parts[len(parts)-2]
		ok := false
		switch family {
		case softwareMisskey:
			ok = prev == "notes"
		case softwarePleroma:
			ok = prev == "notice"
		case softwareFedibird:
			ok = prev == "statuses" || strings.HasPrefix(prev, "@")
		}
		if ok && last != "" && len(last) <= 64 && !strings.ContainsAny(last, "?#%") {
			return last
		}
	}
	return ""
}

// customReactionName normalises a remote custom emoji to our stored form
// ":shortcode@host:" (host defaults to the origin server).
func customReactionName(name, origin string) string {
	name = strings.Trim(name, ":")
	short, host, found := strings.Cut(name, "@")
	if !found || host == "" || host == "." {
		host = origin
	}
	return ":" + short + "@" + strings.ToLower(host) + ":"
}

func remoteReaction(name string, count int64, iconURL string) *serializer.Reaction {
	r := &serializer.Reaction{Name: name, Count: count}
	if iconURL != "" && safeEmojiURL(iconURL) {
		r.URL = mediaproxy.URL(mediaproxy.KindEmoji, iconURL)
		r.StaticURL = r.URL
	}
	return r
}

func fetchMisskeyReactions(ctx context.Context, host, id string) ([]*serializer.Reaction, error) {
	base := remoteReactionBase(host)
	var note struct {
		Reactions      map[string]int64  `json:"reactions"`
		ReactionEmojis map[string]string `json:"reactionEmojis"`
	}
	if err := remoteGetJSON(ctx, http.MethodPost, base+"/api/notes/show", map[string]string{"noteId": id}, &note); err != nil {
		return nil, err
	}
	out := make([]*serializer.Reaction, 0, len(note.Reactions))
	for key, n := range note.Reactions {
		if n <= 0 {
			continue
		}
		if !strings.HasPrefix(key, ":") {
			out = append(out, remoteReaction(key, n, ""))
			continue
		}
		m := customReaction.FindStringSubmatch(key)
		if m == nil {
			continue
		}
		short, h := m[1], strings.ToLower(m[2])
		icon := ""
		origin := "https://" + host
		if h == "" || h == "." {
			icon = origin + "/emoji/" + short + ".webp"
		} else {
			icon = note.ReactionEmojis[short+"@"+h]
			if icon == "" {
				icon = origin + "/emoji/" + short + "@" + h + ".webp"
			}
		}
		out = append(out, remoteReaction(customReactionName(key, host), n, icon))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

type mastoReaction struct {
	Name   string  `json:"name"`
	Count  int64   `json:"count"`
	URL    string  `json:"url"`
	Domain *string `json:"domain"`
}

func convertMastoReactions(host string, list []mastoReaction) []*serializer.Reaction {
	out := make([]*serializer.Reaction, 0, len(list))
	for _, r := range list {
		if r.Count <= 0 || r.Name == "" {
			continue
		}
		if r.URL == "" {
			out = append(out, remoteReaction(r.Name, r.Count, ""))
			continue
		}
		name := r.Name
		if r.Domain != nil && *r.Domain != "" && !strings.Contains(name, "@") {
			name += "@" + *r.Domain
		}
		out = append(out, remoteReaction(customReactionName(name, host), r.Count, r.URL))
	}
	return out
}

func fetchPleromaReactions(ctx context.Context, host, id string) ([]*serializer.Reaction, error) {
	var list []mastoReaction
	if err := remoteGetJSON(ctx, http.MethodGet, remoteReactionBase(host)+"/api/v1/pleroma/statuses/"+url.PathEscape(id)+"/reactions", nil, &list); err != nil {
		return nil, err
	}
	return convertMastoReactions(host, list), nil
}

func fetchFedibirdReactions(ctx context.Context, host, id string) ([]*serializer.Reaction, error) {
	var st struct {
		EmojiReactions []mastoReaction `json:"emoji_reactions"`
	}
	if err := remoteGetJSON(ctx, http.MethodGet, remoteReactionBase(host)+"/api/v1/statuses/"+url.PathEscape(id), nil, &st); err != nil {
		return nil, err
	}
	return convertMastoReactions(host, st.EmojiReactions), nil
}

// RemoteStatusReactions fetches the origin server's reaction counts for a
// remote status. It returns nil on any failure or unsupported software.
func RemoteStatusReactions(ctx context.Context, status *models.Status) []*serializer.Reaction {
	if status == nil || status.IsLocal() || status.OriginUri == nil {
		return nil
	}
	u, err := url.Parse(*status.OriginUri)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	host := strings.ToLower(u.Host)
	key := *status.OriginUri
	if v, ok := remoteReactionCache.get(key); ok {
		r, _ := v.([]*serializer.Reaction)
		return r
	}
	family := remoteSoftware(ctx, host)
	id := remoteNoteID(family, u.Hostname(), status)
	var out []*serializer.Reaction
	if id != "" {
		switch family {
		case softwareMisskey:
			out, err = fetchMisskeyReactions(ctx, host, id)
		case softwarePleroma:
			out, err = fetchPleromaReactions(ctx, host, id)
		case softwareFedibird:
			out, err = fetchFedibirdReactions(ctx, host, id)
		}
		if err != nil {
			out = nil
		}
	}
	remoteReactionCache.set(key, out, remoteReactionTTL)
	return out
}

// MergeReactions combines local and remote counts per reaction name. Remote
// counts already include reactions our users federated to the origin, so
// the larger count wins instead of summing.
func MergeReactions(local, remote []*serializer.Reaction) []*serializer.Reaction {
	if len(remote) == 0 {
		return local
	}
	out := make([]*serializer.Reaction, 0, len(local)+len(remote))
	index := map[string]*serializer.Reaction{}
	for _, r := range local {
		cp := *r
		out = append(out, &cp)
		index[r.Name] = &cp
	}
	for _, r := range remote {
		if l := index[r.Name]; l != nil {
			if r.Count > l.Count {
				l.Count = r.Count
			}
			if l.URL == "" {
				l.URL, l.StaticURL = r.URL, r.StaticURL
			}
			continue
		}
		if len(out) >= 100 {
			break
		}
		cp := *r
		out = append(out, &cp)
		index[r.Name] = &cp
	}
	return out
}

// WithRemoteReactions merges origin-server reactions into a status detail.
func WithRemoteReactions(ctx context.Context, status *models.Status, local []*serializer.Reaction) []*serializer.Reaction {
	return MergeReactions(local, RemoteStatusReactions(ctx, status))
}
