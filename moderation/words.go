package moderation

import (
	"regexp"
	"strings"
)

// WordMatcher implements Misskey's UtilityService.isKeyWordIncluded with
// the patterns compiled once.
//
// Each entry is one line of the admin textarea:
//   - "/pattern/flags" is a regular expression (RE2, like Misskey's re2);
//   - anything else is split on single spaces and matches when the text
//     contains every part (AND).
//
// Matching is case-sensitive, like Misskey's String.includes. Invalid
// regular expressions never match.
type WordMatcher struct {
	entries []wordEntry
}

type wordEntry struct {
	re    *regexp.Regexp
	words []string
	bad   bool
}

var regexpEntry = regexp.MustCompile(`^/(.+)/(.*)$`)

// CompileWords builds a matcher. Empty entries are ignored (Misskey's
// update-meta drops them with filter(Boolean)).
func CompileWords(lines []string) *WordMatcher {
	m := &WordMatcher{}
	for _, line := range lines {
		if line == "" {
			continue
		}
		if sub := regexpEntry.FindStringSubmatch(line); sub != nil {
			m.entries = append(m.entries, compileRegexpEntry(sub[1], sub[2]))
			continue
		}
		m.entries = append(m.entries, wordEntry{words: strings.Split(line, " ")})
	}
	return m
}

// compileRegexpEntry translates JavaScript flags: i, m and s map to Go
// inline flags; g, y, u, d and v do not change whether a match exists.
// Unknown flags make the expression invalid, as in JavaScript.
func compileRegexpEntry(pattern, flags string) wordEntry {
	inline := ""
	for _, f := range flags {
		switch f {
		case 'i', 'm', 's':
			if !strings.ContainsRune(inline, f) {
				inline += string(f)
			}
		case 'g', 'y', 'u', 'd', 'v':
		default:
			return wordEntry{bad: true}
		}
	}
	if inline != "" {
		pattern = "(?" + inline + ")" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return wordEntry{bad: true}
	}
	return wordEntry{re: re}
}

// Empty reports whether the matcher has no entries.
func (m *WordMatcher) Empty() bool { return m == nil || len(m.entries) == 0 }

// Match reports whether text is matched by any entry.
func (m *WordMatcher) Match(text string) bool {
	if m.Empty() || text == "" {
		return false
	}
	for _, e := range m.entries {
		switch {
		case e.bad:
		case e.re != nil:
			if e.re.MatchString(text) {
				return true
			}
		default:
			all := true
			for _, w := range e.words {
				if !strings.Contains(text, w) {
					all = false
					break
				}
			}
			if all {
				return true
			}
		}
	}
	return false
}

// ValidateWords returns the first entry that looks like a regular
// expression but does not compile, so the admin form can reject it
// (Misskey's client validates the same way).
func ValidateWords(lines []string) (string, bool) {
	for _, line := range lines {
		if sub := regexpEntry.FindStringSubmatch(line); sub != nil {
			if compileRegexpEntry(sub[1], sub[2]).bad {
				return line, false
			}
		}
	}
	return "", true
}

// NoteTextForKeywordCheck mirrors concatNoteContentsForKeyWordCheck: cw
// and text are joined without a separator, then poll choices and other
// strings on their own lines.
func NoteTextForKeywordCheck(cw, text string, pollChoices, others []string) string {
	return cw + text + "\n" + strings.Join(pollChoices, "\n") + "\n" + strings.Join(others, "\n")
}
