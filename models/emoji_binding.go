package models

import (
	"context"
	"golang.org/x/net/html"
	"strings"
)

type structuredEmojiKey struct{}

// WithStructuredEmojiIDs makes authenticated federation tags authoritative.
func WithStructuredEmojiIDs(ctx context.Context, ids []uint64) context.Context {
	return context.WithValue(ctx, structuredEmojiKey{}, append([]uint64{}, ids...))
}
func StructuredEmojiIDs(ctx context.Context) ([]uint64, bool) {
	ids, ok := ctx.Value(structuredEmojiKey{}).([]uint64)
	return ids, ok
}

// EmojiText excludes HTML attributes and code examples from display shortcodes.
func EmojiText(text string) string {
	z := html.NewTokenizer(strings.NewReader(text))
	var out strings.Builder
	skip := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return out.String()
		case html.StartTagToken:
			t := z.Token()
			if t.Data == "code" || t.Data == "pre" || t.Data == "script" || t.Data == "style" {
				skip++
			}
		case html.EndTagToken:
			t := z.Token()
			if (t.Data == "code" || t.Data == "pre" || t.Data == "script" || t.Data == "style") && skip > 0 {
				skip--
			}
			out.WriteByte(' ')
		case html.TextToken:
			if skip == 0 {
				out.Write(z.Text())
			}
		}
	}
}

// EmojisInText filters a scoped association, never resolving a shortcode in a
// different server's namespace. Bare shortcodes stay local to their object.
func EmojisInText(emojis []*Emoji, text, domain string, outbound bool) []*Emoji {
	names := map[string]bool{}
	for _, n := range MatchEmojiTags(EmojiText(text)) {
		names[strings.Trim(n, ":")] = true
	}
	result := make([]*Emoji, 0)
	seen := map[string]bool{}
	for _, e := range emojis {
		if e == nil || e.Disabled || e.Domain != domain || !names[e.Shortcode] || seen[e.Shortcode] || (outbound && (e.LocalOnly || e.Domain != "")) {
			continue
		}
		seen[e.Shortcode] = true
		result = append(result, e)
	}
	return result
}

func AccountEmojiText(account *Account) string {
	text := account.DisplayName + " " + account.Note
	if account.Extra != nil {
		for _, f := range account.Extra.Fields {
			text += " " + f.Name + " " + f.Val
		}
	}
	return text
}
