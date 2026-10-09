package serializer

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A tiny JSON-schema-like checker modelling what a strict Swift Codable
// decoder (Toot!, Ivory, ...) accepts for the Mastodon 4.3 entities.

type kind int

const (
	kStr  kind = iota // any string (may be empty)
	kNStr             // non-empty string
	kID               // string of digits
	kURL              // absolute http(s) URL, non-empty
	kDate             // 2025-01-01T00:00:00.000Z
	kDay              // 2025-01-01
	kInt              // non-negative integer
	kNum              // any number
	kBool
	kArr
	kObj
	kEnum
	kLang // ISO language code
	kAny  // present, any type (not checked)
)

type sch struct {
	k        kind
	null     bool           // null allowed
	optional bool           // key may be absent
	elem     *sch           // kArr element
	obj      map[string]sch // kObj
	enum     []string
}

var (
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
	dayRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	idRe   = regexp.MustCompile(`^\d+$`)
	langRe = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]+)*$`)
)

func nullable(s sch) sch { s.null = true; return s }
func opt(s sch) sch      { s.optional = true; return s }
func arr(e sch) sch      { return sch{k: kArr, elem: &e} }
func obj(m map[string]sch) sch {
	return sch{k: kObj, obj: m}
}
func enum(v ...string) sch { return sch{k: kEnum, enum: v} }

func check(path string, v any, s sch, errs *[]string) {
	if v == nil {
		if !s.null {
			*errs = append(*errs, path+": null not allowed")
		}

		return
	}

	bad := func(f string, a ...any) { *errs = append(*errs, path+": "+fmt.Sprintf(f, a...)) }

	switch s.k {
	case kAny:
	case kStr, kNStr, kID, kURL, kDate, kDay, kEnum, kLang:
		str, ok := v.(string)
		if !ok {
			bad("want string, got %T (%v)", v, v)

			return
		}

		switch s.k {
		case kNStr:
			if str == "" {
				bad("empty string")
			}
		case kID:
			if !idRe.MatchString(str) {
				bad("id %q is not a numeric string", str)
			}
		case kURL:
			u, err := url.Parse(str)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				bad("%q is not an absolute http(s) URL", str)
			}
		case kDate:
			if !dateRe.MatchString(str) {
				bad("date %q is not ISO8601 with 3-digit ms and Z", str)
			}
		case kDay:
			if !dayRe.MatchString(str) {
				bad("%q is not a YYYY-MM-DD date", str)
			}
		case kLang:
			if !langRe.MatchString(str) {
				bad("language %q invalid", str)
			}
		case kEnum:
			found := false

			for _, e := range s.enum {
				found = found || e == str
			}

			if !found {
				bad("%q not in %v", str, s.enum)
			}
		}
	case kInt:
		f, ok := v.(float64)
		if !ok || f != float64(int64(f)) || f < 0 {
			bad("want non-negative int, got %v", v)
		}
	case kNum:
		if _, ok := v.(float64); !ok {
			bad("want number, got %T", v)
		}
	case kBool:
		if _, ok := v.(bool); !ok {
			bad("want bool, got %T", v)
		}
	case kArr:
		a, ok := v.([]any)
		if !ok {
			bad("want array, got %T", v)

			return
		}

		for i, e := range a {
			check(fmt.Sprintf("%s[%d]", path, i), e, *s.elem, errs)
		}
	case kObj:
		m, ok := v.(map[string]any)
		if !ok {
			bad("want object, got %T", v)

			return
		}

		keys := make([]string, 0, len(s.obj))
		for k := range s.obj {
			keys = append(keys, k)
		}

		sort.Strings(keys)

		for _, k := range keys {
			fs := s.obj[k]
			val, present := m[k]

			if !present {
				if !fs.optional {
					*errs = append(*errs, path+"."+k+": missing")
				}

				continue
			}

			check(path+"."+k, val, fs, errs)
		}
	}
}

func requireValid(t *testing.T, name string, raw any, s sch) {
	t.Helper()

	b, err := json.Marshal(raw)
	require.NoError(t, err)

	var v any
	require.NoError(t, json.Unmarshal(b, &v))

	var errs []string
	check(name, v, s, &errs)
	require.Empty(t, errs, "%s\n%s", strings.Join(errs, "\n"), b)
}

// ---- schemas ----

var (
	idS   = sch{k: kID}
	strS  = sch{k: kStr}
	urlS  = sch{k: kURL}
	intS  = sch{k: kInt}
	boolS = sch{k: kBool}
	dateS = sch{k: kDate}
)

var emojiS = obj(map[string]sch{
	"shortcode": {k: kNStr}, "url": urlS, "static_url": urlS, "visible_in_picker": boolS,
	"category": nullable(strS),
})

var fieldS = obj(map[string]sch{"name": strS, "value": strS, "verified_at": nullable(dateS)})

var accountS = obj(map[string]sch{
	"id": idS, "username": {k: kNStr}, "acct": {k: kNStr}, "url": urlS, "uri": urlS,
	"display_name": strS, "note": strS, "avatar": urlS, "avatar_static": urlS,
	"header": urlS, "header_static": urlS, "locked": boolS, "bot": boolS, "group": boolS,
	"discoverable": boolS, "indexable": boolS, "hide_collections": boolS,
	"created_at": dateS, "last_status_at": nullable(sch{k: kDay}),
	"followers_count": intS, "following_count": intS, "statuses_count": intS,
	"emojis": arr(emojiS), "fields": arr(fieldS), "roles": arr(sch{k: kAny}),
})

var mediaMetaSize = obj(map[string]sch{
	"width": {k: kInt, optional: true}, "height": {k: kInt, optional: true},
	"size": {k: kNStr, optional: true}, "aspect": {k: kNum, optional: true},
	"duration": {k: kNum, optional: true}, "bitrate": {k: kInt, optional: true},
})

var mediaS = obj(map[string]sch{
	"id": idS, "type": enum("image", "video", "gifv", "audio", "unknown"),
	"url": urlS, "preview_url": nullable(urlS), "remote_url": nullable(urlS),
	"preview_remote_url": nullable(urlS), "text_url": nullable(urlS),
	"description": nullable(sch{k: kNStr}), "blurhash": nullable(sch{k: kNStr}),
	"meta": nullable(obj(map[string]sch{
		"focus":    opt(obj(map[string]sch{"x": {k: kNum}, "y": {k: kNum}})),
		"original": opt(mediaMetaSize), "small": opt(mediaMetaSize),
	})),
})

var mentionS = obj(map[string]sch{"id": idS, "username": {k: kNStr}, "url": urlS, "acct": {k: kNStr}})
var tagS = obj(map[string]sch{"name": {k: kNStr}, "url": urlS})

var cardS = obj(map[string]sch{
	"url": urlS, "title": strS, "description": strS, "type": enum("link", "photo", "video", "rich"),
	"author_name": strS, "author_url": strS, "provider_name": strS, "provider_url": strS,
	"html": strS, "width": intS, "height": intS, "image": nullable(urlS), "embed_url": strS,
	"blurhash": nullable(strS),
})

var pollS = obj(map[string]sch{
	"id": idS, "expires_at": nullable(dateS), "expired": boolS, "multiple": boolS,
	"votes_count": intS, "voters_count": nullable(intS), "voted": opt(boolS),
	"own_votes": opt(nullable(arr(intS))), "emojis": arr(emojiS),
	"options": arr(obj(map[string]sch{"title": {k: kNStr}, "votes_count": nullable(intS)})),
})

func statusSchema(depth int) sch {
	m := map[string]sch{
		"id": idS, "uri": urlS, "url": urlS, "created_at": dateS,
		"account":                accountS,
		"content":                strS,
		"visibility":             enum("public", "unlisted", "private", "direct"),
		"sensitive":              boolS,
		"spoiler_text":           strS,
		"media_attachments":      arr(mediaS),
		"application":            nullable(obj(map[string]sch{"name": {k: kNStr}, "website": nullable(urlS)})),
		"mentions":               arr(mentionS),
		"tags":                   arr(tagS),
		"emojis":                 arr(emojiS),
		"reblogs_count":          intS,
		"favourites_count":       intS,
		"replies_count":          intS,
		"in_reply_to_id":         nullable(idS),
		"in_reply_to_account_id": nullable(idS),
		"language":               nullable(sch{k: kLang}),
		"edited_at":              nullable(dateS),
		"card":                   nullable(cardS),
		"poll":                   nullable(pollS),
		"favourited":             opt(boolS), "reblogged": opt(boolS), "muted": opt(boolS),
		"bookmarked": opt(boolS), "pinned": opt(boolS),
		"filtered": arr(obj(map[string]sch{
			"filter":          obj(map[string]sch{"id": idS, "title": strS, "context": arr(strS), "expires_at": nullable(dateS), "filter_action": enum("warn", "hide"), "keywords": arr(sch{k: kAny}), "statuses": arr(sch{k: kAny})}),
			"keyword_matches": nullable(arr(strS)), "status_matches": nullable(arr(strS)),
		})),
	}
	if depth > 0 {
		m["reblog"] = nullable(statusSchema(depth - 1))
	} else {
		m["reblog"] = nullable(sch{k: kAny})
	}

	return obj(m)
}
