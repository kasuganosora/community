package translation

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Tokens that legitimately survive translation unchanged.
var passthroughToken = regexp.MustCompile(`(?i)(https?://\S+|www\.\S+|[@#][\p{L}\p{N}_.\-]+(@[\p{L}\p{N}_.\-]+)?)`)

// comparable reduces text to the letters and digits that carry its meaning:
// NFKC (fullwidth and halfwidth forms fold together), lower-cased, with
// mentions, URLs and hashtags removed and every punctuation mark, symbol,
// emoji and space dropped. Two texts that compare equal after this were not
// translated, even if a model swapped "," for "，".
func comparable(s string) string {
	s = norm.NFKC.String(s)
	s = passthroughToken.ReplaceAllString(s, " ")
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// scriptShare is the fraction of letters in s matched by in (0 when s has
// fewer than minLetters letters, so short or symbol-only texts never count).
func scriptShare(s string, in func(rune) bool) (float64, int) {
	s = passthroughToken.ReplaceAllString(norm.NFKC.String(s), " ")
	letters, hits := 0, 0
	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if in(r) {
			hits++
		}
	}
	if letters == 0 {
		return 0, 0
	}
	return float64(hits) / float64(letters), letters
}

// targetScript returns the script test for a target language, or nil when
// the target's script is not cheap to check.
func targetScript(target string) func(rune) bool {
	switch primary(target) {
	case "zh":
		return func(r rune) bool { return unicode.Is(unicode.Han, r) }
	case "ja":
		return func(r rune) bool { return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana) }
	case "ko":
		return func(r rune) bool { return unicode.In(r, unicode.Hangul, unicode.Han) }
	case "ru", "uk", "bg", "be", "sr", "mk":
		return func(r rune) bool { return unicode.Is(unicode.Cyrillic, r) }
	case "en", "fr", "de", "es", "pt", "it", "nl", "pl", "tr", "id", "vi", "sv", "da", "nb", "fi", "cs", "ro", "hu", "ca":
		return func(r rune) bool { return unicode.Is(unicode.Latin, r) }
	}
	return nil
}

const (
	minScriptLetters = 8   // ignore very short texts (names, "ok", "lol")
	minScriptShare   = 0.2 // below this the output is not in the target script
)

// unusableText explains why out cannot be a translation of in into target,
// or returns "". source is a hint only (remote language tags are untrusted):
// the script test looks at the texts themselves.
//   - empty_output: input has text, output has none.
//   - unchanged_output: same letters and digits after normalisation.
//   - wrong_script: the target's script is cheap to check, the input is
//     mostly not in it, and the output is not either.
func unusableText(in, out, source, target string) string {
	if strings.TrimSpace(in) == "" {
		return ""
	}
	if strings.TrimSpace(out) == "" {
		return "empty_output"
	}
	sameLang := source != "" && source != "auto" && primary(source) == primary(target)
	ci, co := comparable(in), comparable(out)
	if !sameLang && len([]rune(ci)) > 3 && ci == co {
		return "unchanged_output"
	}
	if test := targetScript(target); test != nil {
		srcShare, _ := scriptShare(in, test)
		outShare, letters := scriptShare(out, test)
		if srcShare < minScriptShare && letters >= minScriptLetters && outShare < minScriptShare {
			return "wrong_script"
		}
	}
	return ""
}
