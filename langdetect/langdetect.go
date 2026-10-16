// Package langdetect guesses a post's language from Unicode script ranges
// plus a cheap stopword/diacritic heuristic for Latin text. It is meant for
// ranking signals, not accuracy: too short or ambiguous text yields "".
package langdetect

import (
	"html"
	"regexp"
	"strings"
	"unicode"
)

var (
	tagRe       = regexp.MustCompile(`(?s)<[^>]*>`)
	urlRe       = regexp.MustCompile(`(?i)\b(?:https?|ftp)://\S+|\bwww\.\S+`)
	mentionRe   = regexp.MustCompile(`@[\p{L}\p{N}_.\-]+(?:@[\p{L}\p{N}_.\-]+)?`)
	hashtagRe   = regexp.MustCompile(`#[\p{L}\p{N}_]+`)
	shortcodeRe = regexp.MustCompile(`:[A-Za-z0-9_+\-]+:`)
	wordRe      = regexp.MustCompile(`\p{L}+`)
)

// Clean strips HTML, URLs, mentions, hashtags and emoji shortcodes.
func Clean(text string) string {
	text = tagRe.ReplaceAllString(text, " ")
	text = html.UnescapeString(text)
	text = urlRe.ReplaceAllString(text, " ")
	text = mentionRe.ReplaceAllString(text, " ")
	text = hashtagRe.ReplaceAllString(text, " ")
	text = shortcodeRe.ReplaceAllString(text, " ")
	return text
}

var deWords = map[string]bool{"der": true, "die": true, "und": true, "das": true, "ist": true, "nicht": true, "ich": true, "mit": true, "ein": true, "eine": true, "auf": true, "den": true, "zu": true, "es": true, "sich": true, "auch": true, "für": true, "von": true, "wir": true, "aber": true}
var frWords = map[string]bool{"le": true, "la": true, "les": true, "et": true, "est": true, "des": true, "une": true, "un": true, "du": true, "je": true, "pas": true, "que": true, "pour": true, "dans": true, "qui": true, "sur": true, "avec": true, "ce": true, "il": true, "nous": true, "mais": true}
var enWords = map[string]bool{"the": true, "and": true, "is": true, "of": true, "to": true, "in": true, "it": true, "you": true, "that": true, "for": true, "this": true, "with": true, "was": true, "are": true, "on": true, "my": true, "have": true, "not": true, "but": true, "be": true}

// Detect returns a BCP-47 base code (ja, ko, zh, ru, en, fr, de) or "".
func Detect(text string) string {
	text = Clean(text)
	var han, kana, hangul, cyr, latin int
	for _, r := range text {
		switch {
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			kana++
		case unicode.Is(unicode.Hangul, r):
			hangul++
		case unicode.Is(unicode.Han, r):
			han++
		case unicode.Is(unicode.Cyrillic, r):
			cyr++
		case unicode.Is(unicode.Latin, r):
			latin++
		}
	}
	cjk := han + kana + hangul
	// CJK characters carry much more information per rune than letters.
	switch {
	case kana >= 2 && kana*5 >= cjk:
		return "ja"
	case hangul >= 2 && hangul*2 >= cjk:
		return "ko"
	case han >= 2 && han*4 >= latin && han*4 >= cyr && kana == 0:
		return "zh"
	case kana >= 2 && kana*4 >= latin:
		return "ja"
	case cyr >= 8 && cyr >= latin:
		return "ru"
	case latin < 12:
		return ""
	}
	lower := strings.ToLower(text)
	de, fr, en := 0, 0, 0
	for _, w := range wordRe.FindAllString(lower, -1) {
		if deWords[w] {
			de++
		}
		if frWords[w] {
			fr++
		}
		if enWords[w] {
			en++
		}
	}
	de += 2 * strings.Count(lower, "ß")
	for _, r := range lower {
		switch r {
		case 'ä', 'ö', 'ü':
			de++
		case 'é', 'è', 'ê', 'à', 'ç', 'ù', 'â', 'ô', 'î', 'œ':
			fr++
		}
	}
	switch {
	case de >= 2 && de > fr && de > en:
		return "de"
	case fr >= 2 && fr > de && fr > en:
		return "fr"
	case en >= 1 && en >= de && en >= fr:
		return "en"
	case de == 0 && fr == 0:
		return "en"
	}
	return ""
}

// Base normalizes a language tag to its lowercase primary subtag, so zh-CN,
// zh-TW, zh_Hant and zh all compare equal.
func Base(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(tag, "-_"); i >= 0 {
		tag = tag[:i]
	}
	if len(tag) < 2 || len(tag) > 3 {
		return ""
	}
	for _, r := range tag {
		if r < 'a' || r > 'z' {
			return ""
		}
	}
	return tag
}

// FromContentMap returns the language key of an ActivityStreams contentMap
// (Mastodon sends {"en": "<p>…</p>"}); "" when absent or ambiguous.
func FromContentMap(object map[string]any) string {
	cm, _ := object["contentMap"].(map[string]any)
	if len(cm) == 0 {
		return ""
	}
	if content, _ := object["content"].(string); content != "" && len(cm) > 1 {
		for k, v := range cm {
			if s, _ := v.(string); s == content {
				return Base(k)
			}
		}
	}
	best := ""
	for k := range cm {
		if b := Base(k); b != "" && (best == "" || b < best) {
			best = b
		}
	}
	return best
}

// ParseAcceptLanguage returns base codes in header order (q ignored except 0).
func ParseAcceptLanguage(header string) []string {
	out := []string{}
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		if len(fields) > 1 && strings.TrimSpace(fields[1]) == "q=0" {
			continue
		}
		if b := Base(fields[0]); b != "" {
			out = append(out, b)
		}
	}
	return out
}
