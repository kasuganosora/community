package datatransfer

import (
	"context"
	"fmt"
	"strings"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// ReplyNarrowedCode is the (non-blocking) issue for replies whose source
// audience is wider than their parent's. The maid.lat rehearsal had local
// "home" (unlisted) replies to remote followers-only posts; publishing them
// as unlisted would expose a followers-only thread, and failing the whole
// run on them is worse. They are imported narrowed to the parent's level.
const ReplyNarrowedCode = "reply_visibility_narrowed"

// narrowVisibility returns the visibility a reply may use under a parent:
// never wider than the parent's (public < unlisted < private < direct).
func narrowVisibility(own, parent uint8) (uint8, bool) {
	if own < parent {
		return parent, true
	}
	return own, false
}

func replyNarrowedIssue(count int, samples, violators []string) Issue {
	return Issue{Severity: "warning", Code: ReplyNarrowedCode, Kind: "post", SourceID: firstOr(samples),
		Message: fmt.Sprintf("%d repl(y/ies) had a wider audience than their parent and are imported narrowed to the parent's visibility (e.g. unlisted -> followers-only; direct parents keep only the parent's participants); sample source ids: %s",
			count, strings.Join(samples, ",")),
		Count: count, SampleIDs: samples, violators: violators}
}

// replyAudiences computes the effective (possibly narrowed) visibility of
// every live post in b, following reply chains through the bundle, through
// known (effective visibilities of posts outside the bundle, e.g. earlier
// chunks of a streamed run) and through already imported parents (seed ->
// statuses.visibility). It returns the narrowed post ids. When known is not
// nil it is updated with this bundle's results.
func replyAudiences(ctx context.Context, conn *gorm.DB, b *Bundle, o Options, known map[string]uint8) ([]string, error) {
	posts := map[string]*Post{}
	for idx := range b.Posts {
		posts[b.Posts[idx].ID] = &b.Posts[idx]
	}
	// Imported parents outside the bundle and outside known.
	var lookup []uint64
	byTarget := map[uint64]string{}
	for _, p := range b.Posts {
		if p.ReplyID == "" || posts[p.ReplyID] != nil {
			continue
		}
		if _, ok := known[p.ReplyID]; ok {
			continue
		}
		if t := o.Seed["post"][p.ReplyID]; t != 0 && byTarget[t] == "" {
			byTarget[t] = p.ReplyID
			lookup = append(lookup, t)
		}
	}
	fromDB := map[string]uint8{}
	for start := 0; start < len(lookup); start += 500 {
		end := min(start+500, len(lookup))
		var rows []struct {
			Id         uint64
			Visibility uint8
		}
		if err := conn.WithContext(ctx).Model(&models.Status{}).Select("id, visibility").Where("id IN ?", lookup[start:end]).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			fromDB[byTarget[r.Id]] = r.Visibility
		}
	}
	eff := map[string]uint8{}
	visiting := map[string]bool{}
	var narrowed []string
	var resolve func(id string) (uint8, bool)
	resolve = func(id string) (uint8, bool) {
		if v, ok := eff[id]; ok {
			return v, true
		}
		p := posts[id]
		if p == nil {
			if v, ok := known[id]; ok {
				return v, true
			}
			v, ok := fromDB[id]
			return v, ok
		}
		if p.ArchiveOnly || visiting[id] {
			return 0, false
		}
		visiting[id] = true
		v := visibility(p.Visibility)
		if p.ReplyID != "" && o.Seed[ArchivedPostKind][p.ReplyID] == 0 {
			if pv, ok := resolve(p.ReplyID); ok {
				var n bool
				if v, n = narrowVisibility(v, pv); n {
					narrowed = append(narrowed, id)
				}
			}
		}
		delete(visiting, id)
		eff[id] = v
		return v, true
	}
	for _, p := range b.Posts {
		resolve(p.ID)
	}
	if known != nil {
		for id, v := range eff {
			known[id] = v
		}
	}
	return narrowed, nil
}

// directAudience lists the accounts that can see a direct parent: its author
// and its explicit recipients.
func directAudience(tx *gorm.DB, parent *models.Status) (map[uint64]bool, error) {
	allowed := map[uint64]bool{parent.AccountId: true}
	var ids []uint64
	if err := tx.Model(&models.StatusToAccount{}).Where("status_id = ?", parent.Id).Pluck("account_id", &ids).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		allowed[id] = true
	}
	return allowed, nil
}
