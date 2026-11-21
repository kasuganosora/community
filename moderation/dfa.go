package moderation

import (
	"strings"
	"unicode"
)

// FilterLevel defines the graded moderation action for a matched keyword
// (inspired by openBshi/app/service/main/filter).
type FilterLevel int

const (
	FilterLevelAllow  FilterLevel = iota
	FilterLevelMask               // Replace matched spans with asterisks
	FilterLevelReview             // Allow publication into review / unlisted queue
	FilterLevelBlock              // Reject content outright
)

// GradedRule describes a single keyword rule with severity level and optional area scope.
type GradedRule struct {
	Word  string
	Level FilterLevel
	Areas []string // Empty means global across all areas (status, reply, dm, profile)
}

// FilterResult is returned by GradedFilter.Evaluate.
type FilterResult struct {
	Level      FilterLevel
	Matched    []string
	MaskedText string
}

type dfaNode struct {
	children map[rune]*dfaNode
	fail     *dfaNode
	outputs  []dfaOutput
}

type dfaOutput struct {
	word  string
	runes int
	level FilterLevel
}

// DFATrie is an Aho-Corasick multi-pattern automaton that scans text in O(len(text))
// time regardless of how many keywords are configured.
type DFATrie struct {
	root  *dfaNode
	count int
}

// NewDFATrie builds an Aho-Corasick automaton from rules.
func NewDFATrie(rules []GradedRule) *DFATrie {
	root := &dfaNode{children: make(map[rune]*dfaNode)}
	count := 0
	for _, r := range rules {
		w := r.Word
		if w == "" {
			continue
		}
		rs := []rune(w)
		cur := root
		for _, ch := range rs {
			if cur.children == nil {
				cur.children = make(map[rune]*dfaNode)
			}
			next := cur.children[ch]
			if next == nil {
				next = &dfaNode{}
				cur.children[ch] = next
			}
			cur = next
		}
		cur.outputs = append(cur.outputs, dfaOutput{word: w, runes: len(rs), level: r.Level})
		count++
	}
	if count == 0 {
		return &DFATrie{root: root}
	}

	// Build failure links via BFS.
	queue := make([]*dfaNode, 0, len(root.children))
	for _, child := range root.children {
		child.fail = root
		queue = append(queue, child)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for ch, child := range cur.children {
			f := cur.fail
			for f != nil && (f.children == nil || f.children[ch] == nil) {
				f = f.fail
			}
			if f == nil {
				child.fail = root
			} else {
				child.fail = f.children[ch]
				if len(child.fail.outputs) > 0 {
					child.outputs = append(child.outputs, child.fail.outputs...)
				}
			}
			queue = append(queue, child)
		}
	}

	return &DFATrie{root: root, count: count}
}

// Empty reports whether the trie contains no words.
func (t *DFATrie) Empty() bool { return t == nil || t.count == 0 }

// Match reports whether text contains any word in the trie in O(len(text)).
func (t *DFATrie) Match(text string) bool {
	if t.Empty() || text == "" {
		return false
	}
	cur := t.root
	for _, ch := range text {
		for cur != t.root && (cur.children == nil || cur.children[ch] == nil) {
			cur = cur.fail
		}
		if cur.children != nil {
			if next := cur.children[ch]; next != nil {
				cur = next
			}
		}
		if len(cur.outputs) > 0 {
			return true
		}
	}
	return false
}

func (t *DFATrie) scanRunes(runes []rune, onMatch func(startIdx, endIdx int, out dfaOutput)) {
	if t.Empty() || len(runes) == 0 {
		return
	}
	cur := t.root
	for i, ch := range runes {
		for cur != t.root && (cur.children == nil || cur.children[ch] == nil) {
			cur = cur.fail
		}
		if cur.children != nil {
			if next := cur.children[ch]; next != nil {
				cur = next
			}
		}
		for _, out := range cur.outputs {
			start := max(0, i-out.runes+1)
			onMatch(start, i, out)
		}
	}
}

// MatchAll scans text and returns the highest matched FilterLevel, unique matched
// keywords, and a masked copy of text where matched spans are replaced by '*'.
func (t *DFATrie) MatchAll(text string) (FilterLevel, []string, string) {
	if t.Empty() || text == "" {
		return FilterLevelAllow, nil, text
	}
	runes := []rune(text)
	mask := make([]bool, len(runes))
	maxLevel := FilterLevelAllow
	seen := make(map[string]bool)
	matched := make([]string, 0)

	t.scanRunes(runes, func(start, end int, out dfaOutput) {
		if out.level > maxLevel {
			maxLevel = out.level
		}
		if !seen[out.word] {
			seen[out.word] = true
			matched = append(matched, out.word)
		}
		for j := start; j <= end; j++ {
			mask[j] = true
		}
	})
	if len(matched) == 0 {
		return FilterLevelAllow, nil, text
	}
	for i, m := range mask {
		if m {
			runes[i] = '*'
		}
	}
	return maxLevel, matched, string(runes)
}

func normalizeWithMap(text string) (rawRunes []rune, normRunes []rune, normToRaw []int) {
	rawRunes = []rune(text)
	normRunes = make([]rune, 0, len(rawRunes))
	normToRaw = make([]int, 0, len(rawRunes))
	for idx, r := range rawRunes {
		// Drop zero-width / format control characters.
		if unicode.Is(unicode.Cf, r) || r == '\u200b' || r == '\u200c' || r == '\u200d' || r == '\ufeff' {
			continue
		}
		// Full-width ASCII (！..～) -> half-width (!..~).
		if r >= 0xFF01 && r <= 0xFF5E {
			r -= 0xFEE0
		} else if r == 0x3000 {
			r = ' '
		}
		// Strip common obfuscation punctuation/separators inserted between characters.
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		normRunes = append(normRunes, unicode.ToLower(r))
		normToRaw = append(normToRaw, idx)
	}
	return rawRunes, normRunes, normToRaw
}

// NormalizeForFilter strips zero-width/obfuscation characters, folds full-width
// ASCII to half-width, and lowercases letters so split-word spam cannot evade
// keyword rules.
func NormalizeForFilter(text string) string {
	if text == "" {
		return ""
	}
	_, normRunes, _ := normalizeWithMap(text)
	return string(normRunes)
}

// GradedFilter evaluates content against area-scoped graded DFA rules with whitelist exemptions.
type GradedFilter struct {
	global    *DFATrie
	byArea    map[string]*DFATrie
	whitelist *DFATrie
}

// NewGradedFilter compiles graded keyword rules and optional whitelist phrases.
func NewGradedFilter(rules []GradedRule, whitelist []string) *GradedFilter {
	var globalRules []GradedRule
	areaBuckets := make(map[string][]GradedRule)
	for _, r := range rules {
		norm := NormalizeForFilter(r.Word)
		if norm == "" {
			continue
		}
		nr := GradedRule{Word: norm, Level: r.Level}
		if len(r.Areas) == 0 {
			globalRules = append(globalRules, nr)
			continue
		}
		for _, a := range r.Areas {
			a = strings.ToLower(strings.TrimSpace(a))
			if a != "" {
				areaBuckets[a] = append(areaBuckets[a], nr)
			}
		}
	}
	byArea := make(map[string]*DFATrie, len(areaBuckets))
	for a, rs := range areaBuckets {
		byArea[a] = NewDFATrie(rs)
	}
	whiteRules := make([]GradedRule, 0, len(whitelist))
	for _, w := range whitelist {
		if norm := NormalizeForFilter(w); norm != "" {
			whiteRules = append(whiteRules, GradedRule{Word: norm, Level: FilterLevelAllow})
		}
	}
	return &GradedFilter{
		global:    NewDFATrie(globalRules),
		byArea:    byArea,
		whitelist: NewDFATrie(whiteRules),
	}
}

// Evaluate checks text under the given business area (e.g. "status", "reply", "dm", "profile").
// Whitelisted spans are blanked in-place prior to evaluation so benign phrases are exempted
// while preserving exact rune-position alignment back to raw text for clean masking.
func (f *GradedFilter) Evaluate(area, text string) FilterResult {
	if f == nil || text == "" {
		return FilterResult{Level: FilterLevelAllow, MaskedText: text}
	}
	rawRunes, normRunes, normToRaw := normalizeWithMap(text)
	if len(normRunes) == 0 {
		return FilterResult{Level: FilterLevelAllow, MaskedText: text}
	}
	if !f.whitelist.Empty() {
		type span struct{ s, e int }
		var whiteSpans []span
		f.whitelist.scanRunes(normRunes, func(start, end int, _ dfaOutput) {
			whiteSpans = append(whiteSpans, span{s: start, e: end})
		})
		for _, sp := range whiteSpans {
			for j := sp.s; j <= sp.e; j++ {
				normRunes[j] = '\x00'
			}
		}
	}

	level := FilterLevelAllow
	seen := make(map[string]bool)
	var matched []string
	var rawMask []bool

	recordHit := func(start, end int, out dfaOutput) {
		if out.level > level {
			level = out.level
		}
		if !seen[out.word] {
			seen[out.word] = true
			matched = append(matched, out.word)
		}
		if out.level >= FilterLevelMask {
			if rawMask == nil {
				rawMask = make([]bool, len(rawRunes))
			}
			rawStart := normToRaw[start]
			rawEnd := normToRaw[end]
			for k := rawStart; k <= rawEnd; k++ {
				rawMask[k] = true
			}
		}
	}

	f.global.scanRunes(normRunes, recordHit)
	if area != "" {
		if trie := f.byArea[strings.ToLower(strings.TrimSpace(area))]; trie != nil {
			trie.scanRunes(normRunes, recordHit)
		}
	}

	masked := text
	if level == FilterLevelMask && rawMask != nil {
		outRunes := make([]rune, len(rawRunes))
		copy(outRunes, rawRunes)
		for i, m := range rawMask {
			if m {
				outRunes[i] = '*'
			}
		}
		masked = string(outRunes)
	}
	return FilterResult{
		Level:      level,
		Matched:    matched,
		MaskedText: masked,
	}
}
