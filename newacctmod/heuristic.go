package newacctmod

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

var (
	urlRe   = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
	spaceRe = regexp.MustCompile(`\s+`)
)

// Fingerprint normalizes status text for duplicate detection.
func Fingerprint(text string) string {
	n := normalizeText(text)
	if n == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(n))
	return hex.EncodeToString(sum[:16])
}

func normalizeText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = urlRe.ReplaceAllString(text, " ")
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return spaceRe.ReplaceAllString(strings.TrimSpace(b.String()), " ")
}

// ExtractLinks returns unique http(s) URLs from text.
func ExtractLinks(text string) []string {
	raw := urlRe.FindAllString(text, -1)
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, u := range raw {
		u = strings.TrimRight(u, ".,);]")
		parsed, err := url.Parse(u)
		if err != nil || parsed.Host == "" {
			continue
		}
		key := strings.ToLower(parsed.Host) + strings.ToLower(parsed.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, u)
	}
	return out
}

// HeuristicVerdict is a cheap prefilter before calling the LLM.
type HeuristicVerdict struct {
	Reject bool
	Reason string
}

// RunHeuristic flags obvious same-text / same-link spam among the author's
// recent local posts. Empty text with links still fingerprints by link set.
func RunHeuristic(tx *gorm.DB, accountID, statusID uint64, text string) HeuristicVerdict {
	fp := Fingerprint(text)
	links := ExtractLinks(text)
	if fp == "" && len(links) == 0 {
		return HeuristicVerdict{}
	}

	var recent []models.Status
	q := tx.Where("account_id = ? AND id <> ? AND deleted_at IS NULL", accountID, statusID).
		Order("id DESC").Limit(30)
	if err := q.Find(&recent).Error; err != nil || len(recent) == 0 {
		return HeuristicVerdict{}
	}

	ids := make([]uint64, 0, len(recent))
	for _, s := range recent {
		ids = append(ids, s.Id)
	}
	var contents []models.StatusContent
	_ = tx.Where("status_id IN ?", ids).Find(&contents).Error
	byID := map[uint64]string{}
	for _, c := range contents {
		byID[c.StatusId] = c.Content
	}

	sameText := 0
	sameLink := 0
	linkSet := map[string]bool{}
	for _, l := range links {
		linkSet[strings.ToLower(l)] = true
	}
	for _, s := range recent {
		body := byID[s.Id]
		if fp != "" && Fingerprint(body) == fp {
			sameText++
		}
		for _, l := range ExtractLinks(body) {
			if linkSet[strings.ToLower(l)] {
				sameLink++
				break
			}
		}
	}
	if sameText >= 2 {
		return HeuristicVerdict{Reject: true, Reason: "repeated identical text"}
	}
	if len(links) > 0 && sameLink >= 3 {
		return HeuristicVerdict{Reject: true, Reason: "repeated promotional links"}
	}
	return HeuristicVerdict{}
}
