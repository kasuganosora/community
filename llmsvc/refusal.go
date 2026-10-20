package llmsvc

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Stable reason codes for an unusable LLM translation (logged on fallback).
const (
	ReasonEmptyOutput = "llm_empty_output"
	ReasonRefusal     = "llm_refusal"
	ReasonLengthRatio = "llm_length_ratio"
	ReasonUnchanged   = "llm_unchanged_output"
	ReasonBadOutput   = "llm_bad_output"
)

// OutputError is an unusable model output with a stable reason code. It
// matches ErrBadOutput through errors.Is.
type OutputError struct{ Reason string }

func (e *OutputError) Error() string        { return "llm returned an unusable translation: " + e.Reason }
func (e *OutputError) Is(target error) bool { return target == ErrBadOutput }

// OutputReason returns the reason code of err if it is an unusable output.
func OutputReason(err error) string {
	if oe, ok := err.(*OutputError); ok {
		return oe.Reason
	}
	if err == ErrBadOutput {
		return ReasonBadOutput
	}
	return ""
}

var refusalPattern = regexp.MustCompile(`(?i)(\bI\s*(can(?:'|’)?t|cannot|can not|am unable to|'m unable to|won(?:'|’)t)\b|\bI(?:'|’)m sorry\b|\bas an ai\b|抱歉|对不起|對不起|无法翻译|無法翻譯|无法提供|無法提供|不能翻译|不能翻譯|申し訳|翻訳できません|お手伝いできません|Извините|не могу|je ne peux pas|désolé|ich kann (?:das |dies )?nicht|es tut mir leid)`)

// looksLikeRefusal reports a refusal phrase in out that the source does not
// contain itself (so translating "I can't wait" is not a refusal).
func looksLikeRefusal(out, src string) bool {
	m := refusalPattern.FindString(out)
	return m != "" && !strings.Contains(strings.ToLower(src), strings.ToLower(m)) && !refusalPattern.MatchString(src)
}

var tagStrip = regexp.MustCompile(`<[^>]*>|\{\{\d+\}\}`)

func visibleText(s string) string { return strings.TrimSpace(tagStrip.ReplaceAllString(s, " ")) }

// CheckTranslation validates a parsed translation of src (both may contain
// HTML and placeholders) and returns a reason code, or "" when usable.
// sourceLang/targetLang are primary language codes ("" when unknown); an
// output identical to the input only counts as a refusal when they differ.
func CheckTranslation(src, out, sourceLang, targetLang string) string {
	s, o := visibleText(src), visibleText(out)
	if s == "" {
		return ""
	}
	if o == "" {
		return ReasonEmptyOutput
	}
	if looksLikeRefusal(o, s) {
		return ReasonRefusal
	}
	if o == s && sourceLang != "" && targetLang != "" && sourceLang != targetLang {
		return ReasonUnchanged
	}
	sn, on := utf8.RuneCountInString(s), utf8.RuneCountInString(o)
	if sn >= 20 || on >= 20 { // short texts vary too much (CJK vs Latin ~1:3)
		ratio := float64(on) / float64(sn)
		if ratio < 0.15 || ratio > 6 {
			return ReasonLengthRatio
		}
	}
	return ""
}
