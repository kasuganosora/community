package datatransfer

import (
	"context"
	"encoding/json"
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
// Sentinels in the cross-chunk known map for posts that are not published.
const (
	knownArchived uint8 = 250
	knownDead     uint8 = 251
)

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
	// Posts committed (or archived) by an earlier run are skipped by receipt
	// at import: they are never narrowed again, and their stored visibility
	// is what their replies are checked against.
	done := map[string]bool{}
	archivedDone := map[string]bool{}
	if conn.Migrator().HasTable(&ImportReceipt{}) && len(b.Posts) > 0 {
		namespace := transferNamespace(b, o)
		hashes := make([]string, 0, len(b.Posts))
		for _, p := range b.Posts {
			hashes = append(hashes, digestBytes([]byte(p.ID)))
		}
		for start := 0; start < len(hashes); start += 500 {
			var rows []ImportReceipt
			if err := conn.WithContext(ctx).Select("kind", "source_id", "target_id").Where("namespace = ? AND kind IN ? AND source_hash IN ?", namespace, []string{"post", ArchivedPostKind}, hashes[start:min(start+500, len(hashes))]).Find(&rows).Error; err != nil {
				return nil, err
			}
			var live []uint64
			for _, row := range rows {
				if row.Kind == ArchivedPostKind {
					archivedDone[row.SourceID] = true
				} else if row.TargetID > 0 {
					done[row.SourceID] = true
					live = append(live, row.TargetID)
					byTarget[row.TargetID] = row.SourceID
				}
			}
			if len(live) > 0 {
				var vis []struct {
					Id         uint64
					Visibility uint8
				}
				if err := conn.WithContext(ctx).Model(&models.Status{}).Select("id, visibility").Where("id IN ?", live).Find(&vis).Error; err != nil {
					return nil, err
				}
				for _, r := range vis {
					fromDB[byTarget[r.Id]] = r.Visibility
				}
			}
		}
	}
	// The importer's outcomes for a post: published (with an effective
	// visibility), archived (its replies publish unlinked, never narrowed),
	// or dead: its parent can never be published (cycle, missing parent),
	// so the importer archives it and every descendant waiting on it.
	const (
		live = iota
		archived
		dead
	)
	type outcome struct {
		state int
		v     uint8
	}
	eff := map[string]outcome{}
	visiting := map[string]bool{}
	var narrowed []string
	var resolve func(id string) outcome
	resolve = func(id string) outcome {
		if out, ok := eff[id]; ok {
			return out
		}
		if archivedDone[id] {
			return outcome{state: archived}
		}
		if v, ok := fromDB[id]; ok && (done[id] || posts[id] == nil) {
			return outcome{v: v}
		}
		p := posts[id]
		if p == nil {
			if v, ok := known[id]; ok {
				if v == knownArchived {
					return outcome{state: archived}
				}
				if v == knownDead {
					return outcome{state: dead}
				}
				return outcome{v: v}
			}
			if o.Seed[ArchivedPostKind][id] != 0 {
				return outcome{state: archived}
			}
			return outcome{state: dead}
		}
		if p.ArchiveOnly {
			eff[id] = outcome{state: archived}
			return eff[id]
		}
		if visiting[id] {
			return outcome{state: dead}
		}
		visiting[id] = true
		out := outcome{v: visibility(p.Visibility)}
		if p.ReplyID != "" {
			switch parent := resolve(p.ReplyID); parent.state {
			case live:
				var n bool
				if out.v, n = narrowVisibility(out.v, parent.v); n {
					narrowed = append(narrowed, id)
				}
			case dead:
				out = outcome{state: dead}
			}
		}
		if p.ReblogID != "" && out.state == live {
			if parent := resolve(p.ReblogID); parent.state == dead {
				out = outcome{state: dead}
			} else if parent.state == archived {
				out = outcome{state: archived}
			}
		}
		delete(visiting, id)
		eff[id] = out
		return out
	}
	for _, p := range b.Posts {
		resolve(p.ID)
	}
	// A cycle member resolved first sees its partner as dead and so is dead
	// itself, but its partner was memoised as live; settle to a fixpoint.
	for changed := true; changed; {
		changed = false
		for _, p := range b.Posts {
			out := eff[p.ID]
			if out.state == dead || p.ArchiveOnly {
				continue
			}
			for _, parent := range []string{p.ReplyID, p.ReblogID} {
				if parent != "" && posts[parent] != nil && eff[parent].state == dead {
					eff[p.ID] = outcome{state: dead}
					changed = true
				}
			}
		}
	}
	keep := narrowed[:0]
	for _, id := range narrowed {
		if eff[id].state == live && !done[id] {
			keep = append(keep, id)
		}
	}
	narrowed = keep
	if known != nil {
		for id, out := range eff {
			switch out.state {
			case archived:
				known[id] = knownArchived
			case dead:
				known[id] = knownDead
			default:
				known[id] = out.v
			}
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

// ArchivedRecordsCode is the informational breakdown of records kept only in
// the private archive, per kind and reason, so intentional archiving (quote
// or channel notes, chats) can be told apart from data that failed to link.
const ArchivedRecordsCode = "archived_records"

func archivedIssue(bucket, reason string, count int, samples, violators []string) Issue {
	return Issue{Severity: "info", Code: ArchivedRecordsCode, Kind: bucket + ":" + reason, SourceID: firstOr(samples),
		Message: fmt.Sprintf("%d %s record(s) archived privately (reason %s); sample source ids: %s", count, bucket, reason, strings.Join(samples, ",")),
		Count:   count, SampleIDs: samples, violators: violators}
}

// archivePostReason names why archivePost keeps p private.
func (i *importer) archivePostReason(p Post) string {
	if i.personal() && !p.ArchiveOnly {
		if p.AuthorID != i.bundle.OwnerID {
			return "personal_foreign_author"
		}
		return "personal_unverified_reference"
	}
	if strings.HasPrefix(p.ID, "chat_message:") || strings.HasPrefix(p.ID, "messaging_message:") {
		return "chat_archive_only"
	}
	var ext struct {
		Source map[string]any `json:"source"`
	}
	if json.Unmarshal(p.Extensions, &ext) == nil && ext.Source != nil {
		src := ext.Source
		switch {
		case src["channelId"] != nil && src["channelId"] != "":
			return "channel_note"
		case src["renoteId"] != nil && src["renoteId"] != "":
			return "quote_note"
		}
		switch src["visibility"] {
		case "public", "home", "followers", "specified":
		default:
			return "unknown_visibility"
		}
	}
	return "source_archive_only"
}
