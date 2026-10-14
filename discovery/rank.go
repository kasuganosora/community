package discovery

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sort"
	"strconv"
	"time"
)

// ReciprocalRankFusion has fixed family weights and a fixed normalizer. A
// repeated query cannot increase a family's weight; missing families add zero.
// k=60 is a starting configuration, not a claim of optimality.
func ReciprocalRankFusion(sources []Source) float64 {
	weights := map[string]float64{"exact": .4, "lexical": .4, "semantic": .2}
	best := map[string]int{}
	for _, source := range sources {
		if source.Rank <= 0 || source.Rank > 80 {
			continue
		}
		if _, ok := weights[source.Family]; !ok {
			continue
		}
		if best[source.Family] == 0 || source.Rank < best[source.Family] {
			best[source.Family] = source.Rank
		}
	}
	score := 0.0
	for family, rank := range best {
		score += weights[family] * 61 / float64(60+rank)
	}
	return score
}

func candidateScore(candidate Candidate, asOf time.Time, personalized bool, seed string) float64 {
	age := asOf.Sub(candidate.CreatedAt).Hours()
	if age < 0 || age >= CandidateWindow.Hours() {
		return -1
	}
	fresh := math.Exp(-age / 36)
	interest, social := 0.0, 0.0
	if personalized {
		interest, social = candidate.Interest, candidate.Social
	}
	heat := math.Max(0, math.Min(1, candidate.Heat))
	explore := 0.0
	if candidate.Explore {
		hash := sha256.Sum256([]byte(seed + ":" + strconv.FormatUint(candidate.StatusID, 10)))
		explore = float64(binary.BigEndian.Uint32(hash[:4])) / float64(math.MaxUint32)
	}
	return .35*interest + .25*social + .20*fresh + .15*heat + .05*explore
}

// RankCandidates freezes diversity at generation time, not at page size. There
// are at most ten logical blocks and two hundred slots, including short blocks.
func RankCandidates(candidates []Candidate, asOf time.Time, personalized bool, seed string) [][]RankedItem {
	if len(candidates) > CandidateLimit {
		candidates = candidates[:CandidateLimit]
	}
	type scored struct {
		candidate Candidate
		score     float64
	}
	rows := make([]scored, 0, len(candidates))
	seen := map[uint64]bool{}
	for _, candidate := range candidates {
		if candidate.StatusID == 0 || seen[candidate.StatusID] {
			continue
		}
		seen[candidate.StatusID] = true
		score := candidateScore(candidate, asOf, personalized, seed)
		if score >= 0 {
			rows = append(rows, scored{candidate, score})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].score == rows[j].score {
			return rows[i].candidate.StatusID > rows[j].candidate.StatusID
		}
		return rows[i].score > rows[j].score
	})
	used := make([]bool, len(rows))
	blocks := make([][]RankedItem, 0, 10)
	for block := 0; block < 10; block++ {
		authors, domains := map[uint64]int{}, map[string]int{}
		selected := make([]Candidate, 0, BlockSize)
		pick := func(exploration bool) bool {
			for index, row := range rows {
				candidate := row.candidate
				if used[index] || (exploration && !candidate.Explore) || authors[candidate.AuthorID] >= 2 || (candidate.Domain != "" && domains[candidate.Domain] >= 8) {
					continue
				}
				used[index] = true
				authors[candidate.AuthorID]++
				domains[candidate.Domain]++
				selected = append(selected, candidate)
				return true
			}
			return false
		}
		// Reserve four exploration slots first, so ordinary picks cannot exhaust
		// the author/domain allowance of all exploration candidates.
		for n := 0; n < 4; n++ {
			if !pick(true) {
				break
			}
		}
		explorationCount := len(selected)
		for len(selected) < BlockSize {
			if !pick(false) {
				break
			}
		}
		if len(selected) == 0 {
			break
		}
		// Spread reserved slots through the block while preserving each stream.
		ordered := make([]Candidate, 0, len(selected))
		e, ordinary := 0, explorationCount
		for len(ordered) < len(selected) {
			if e < explorationCount && (len(ordered)%5 == 0 || ordinary >= len(selected)) {
				ordered = append(ordered, selected[e])
				e++
			} else if ordinary < len(selected) {
				ordered = append(ordered, selected[ordinary])
				ordinary++
			}
		}
		items := make([]RankedItem, 0, len(ordered))
		for _, candidate := range ordered {
			item := RankedItem{ID: candidate.StatusID}
			if personalized && candidate.Interest > 0 && len(candidate.Sources) > 0 {
				item.ReasonCode = "followed_topic"
				item.ReasonTag = candidate.Sources[0].Key
			} else if candidate.Explore {
				item.ReasonCode = "discover_author"
			} else {
				item.ReasonCode = "recent_public"
			}
			items = append(items, item)
		}
		blocks = append(blocks, items)
	}
	return blocks
}
