package datatransfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// StreamOptions controls a chunked import from a Spool.
type StreamOptions struct {
	// ChunkSize is the number of primary records per transaction (default 1000).
	ChunkSize int
	// Apply commits each chunk; false only previews (validation).
	Apply bool
	// MaxIssues caps issues kept in the aggregated report (default 10000).
	MaxIssues int
	// OnProgress is called after each chunk.
	OnProgress func(StreamProgress)
}

// StreamProgress reports chunked import progress for one stage.
type StreamProgress struct {
	Stage string `json:"stage"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

// streamStages is the dependency order of a chunked import. Each stage pulls
// the accounts, media and emojis its records reference into the same chunk
// (already committed copies are skipped by receipt), while references to
// posts, polls and lists committed by earlier chunks are seeded from receipts.
var streamStages = []struct{ stage, section string }{
	{"emojis", "emojis"}, {"accounts", "accounts"}, {"credentials", "credentials"}, {"keys", "keys"}, {"media", "media"}, {"posts", "posts"}, {"polls", "polls"},
	{"relations", "relations"}, {"lists", "lists"}, {"reactions", "reactions"}, {"bookmarks", "bookmarks"},
	{"poll_votes", "poll_votes"}, {"records", "records"},
}

// StreamStageNames lists the chunked import stages in order.
func StreamStageNames() []string {
	out := make([]string, len(streamStages))
	for i, s := range streamStages {
		out[i] = s.stage
	}
	return out
}

type streamRun struct {
	ctx        context.Context
	conn       *gorm.DB
	sp         *Spool
	o          Options
	so         StreamOptions
	namespace  string
	total      *Report
	dropped    int
	firstDone  bool
	seenIssues map[string]bool
	widths     widthTally
}

// ImportSpool imports (or previews) a spooled site transfer chunk by chunk.
// Each chunk is its own transaction; receipts make every chunk idempotent, so
// an interrupted run resumes from the last committed checkpoint and a rerun of
// a finished import only reports skipped records.
func ImportSpool(ctx context.Context, conn *gorm.DB, sp *Spool, o Options, so StreamOptions) (*Report, error) {
	if sp.Meta("complete") != "1" {
		return nil, errors.New("spool is incomplete; rerun the export or spool step")
	}
	if o.Scope != "site" {
		return nil, errors.New("streamed import supports site scope only")
	}
	if so.ChunkSize <= 0 {
		so.ChunkSize = 1000
	}
	if so.MaxIssues <= 0 {
		so.MaxIssues = 10000
	}
	conn = conn.Session(&gorm.Session{Logger: logger.Discard})
	if o.Widths == nil {
		o.Widths = NewWidthCache()
	}
	if o.ParentVisibility == nil {
		o.ParentVisibility = map[string]uint8{}
	}
	hdr := sp.Header
	r := &streamRun{seenIssues: map[string]bool{}, ctx: ctx, conn: conn, sp: sp, o: o, so: so, namespace: transferNamespace(&hdr, o),
		total: &Report{CanExecute: true, Source: newReport(&hdr).Source, Counts: map[string]int{}, Created: map[string]int{}, Skipped: map[string]int{}, Archived: map[string]int{}}}
	if !so.Apply {
		if _, err := sp.db.Exec(`CREATE TABLE IF NOT EXISTS seen (kind TEXT NOT NULL, id TEXT NOT NULL, PRIMARY KEY(kind, id)); DELETE FROM seen;`); err != nil {
			return nil, err
		}
	}
	r.firstDone = so.Apply && sp.Meta("issues_done") == "1"
	dups, err := sp.DuplicateSourceIDs()
	if err != nil {
		return nil, err
	}
	if len(dups) > 0 {
		// Two records sharing a source id would make receipts collide mid-run
		// (round 3: "import conflict for archive:<misskey:poll>"). Refuse up
		// front; such a spool must be re-fetched, not reused.
		r.total.CanExecute = false
		r.total.Issues = append(r.total.Issues, dups...)
		if so.Apply {
			return r.total, errors.New("spool has duplicate source ids; re-run the export without --reuse-spool (see report issues)")
		}
	}
	for _, st := range streamStages {
		if err := r.stage(st.stage, st.section); err != nil {
			return r.total, err
		}
		if !r.total.CanExecute && !so.Apply {
			// keep previewing so the report lists every problem
			continue
		}
	}
	for _, kv := range [][2]string{{"accounts", "account"}, {"media", "media"}, {"emojis", "emoji"}, {"posts", "post"}, {"relations", "relation"},
		{"reactions", "reaction"}, {"bookmarks", "bookmark"}, {"polls", "poll"}, {"poll_votes", "poll_vote"}, {"lists", "list"}, {"records", "record"}} {
		r.total.Counts[kv[1]] = sp.Count(kv[0])
	}
	// One total per aggregated issue, ahead of the per-record issues.
	if w := r.widths.list(); len(w) > 0 {
		r.total.Issues = append(w, r.total.Issues...)
		for _, is := range w {
			if is.Severity == "error" {
				r.total.CanExecute = false
			}
		}
	}
	if r.dropped > 0 {
		r.total.Issues = append(r.total.Issues, Issue{Severity: "info", Code: "issues_truncated", Message: fmt.Sprintf("%d further issues were omitted from this report", r.dropped)})
	}
	if so.Apply && r.total.CanExecute {
		_ = sp.SetMeta("imported", time.Now().UTC().Format(time.RFC3339))
	}
	return r.total, nil
}

func (r *streamRun) stage(name, section string) error {
	total := r.sp.Count(section)
	checkpointKey := "checkpoint:" + name
	var after int64
	if r.so.Apply {
		after, _ = strconv.ParseInt(r.sp.Meta(checkpointKey), 10, 64)
	}
	done := 0
	if after > 0 {
		var n int
		_ = r.sp.db.QueryRow(`SELECT count(*) FROM rec WHERE section = ? AND seq <= ?`, section, after).Scan(&n)
		done = n
	}
	r.progress(name, done, total)
	var carry []json.RawMessage
	var carrySeq []int64
	for {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		seqs, raws, err := r.sp.page(section, after, r.so.ChunkSize)
		if err != nil {
			return err
		}
		last := len(raws) == 0
		if last && len(carry) == 0 && r.firstDone {
			break
		}
		items := append(carry, raws...)
		itemSeqs := append(carrySeq, seqs...)
		carry, carrySeq = nil, nil
		chunk, err := r.chunk(section, items)
		if err != nil {
			return err
		}
		if section == "posts" && !last {
			chunk.Posts, carry, carrySeq, err = r.deferPosts(chunk.Posts, items, itemSeqs)
			if err != nil {
				return err
			}
		}
		if err := r.run(chunk); err != nil {
			return err
		}
		if len(seqs) > 0 {
			after = seqs[len(seqs)-1]
		}
		done += len(seqs)
		if r.so.Apply && r.total.CanExecute {
			cp := after
			if len(carrySeq) > 0 {
				cp = carrySeq[0] - 1
			}
			_ = r.sp.SetMeta(checkpointKey, strconv.FormatInt(cp, 10))
		}
		r.progress(name, done, total)
		if last {
			break
		}
	}
	return nil
}

func (r *streamRun) progress(stage string, done, total int) {
	if r.so.OnProgress != nil {
		r.so.OnProgress(StreamProgress{Stage: stage, Done: done, Total: total})
	}
}

func (r *streamRun) header() *Bundle {
	h := r.sp.Header
	return &Bundle{Schema: h.Schema, Scope: h.Scope, Source: h.Source, OwnerID: h.OwnerID}
}

// chunk decodes primary records and closes the chunk over referenced accounts,
// media, emojis and local account credentials/keys.
func (r *streamRun) chunk(section string, items []json.RawMessage) (*Bundle, error) {
	b := r.header()
	if !r.firstDone {
		rows, err := r.sp.db.Query(`SELECT raw FROM rec WHERE section = 'issues' ORDER BY seq`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var raw []byte
			var is Issue
			if rows.Scan(&raw) == nil && json.Unmarshal(raw, &is) == nil {
				b.Issues = append(b.Issues, is)
			}
		}
		rows.Close()
	}
	for _, raw := range items {
		if err := appendRecord(b, section, raw); err != nil {
			return nil, err
		}
	}
	return b, r.close(b)
}

func appendRecord(b *Bundle, section string, raw json.RawMessage) error {
	var err error
	switch section {
	case "accounts":
		var v Account
		if err = json.Unmarshal(raw, &v); err == nil {
			b.Accounts = append(b.Accounts, v)
		}
	case "credentials":
		var v Credential
		if err = json.Unmarshal(raw, &v); err == nil {
			b.Credentials = append(b.Credentials, v)
		}
	case "keys":
		var v FederationKey
		if err = json.Unmarshal(raw, &v); err == nil {
			b.Keys = append(b.Keys, v)
		}
	case "posts":
		var v Post
		if err = json.Unmarshal(raw, &v); err == nil {
			b.Posts = append(b.Posts, v)
		}
	case "media":
		var v Media
		if err = json.Unmarshal(raw, &v); err == nil {
			b.Media = append(b.Media, v)
		}
	case "relations":
		var v Relation
		if err = json.Unmarshal(raw, &v); err == nil {
			b.Relations = append(b.Relations, v)
		}
	case "lists":
		var v List
		if err = json.Unmarshal(raw, &v); err == nil {
			b.Lists = append(b.Lists, v)
		}
	case "reactions":
		var v Reaction
		if err = json.Unmarshal(raw, &v); err == nil {
			b.Reactions = append(b.Reactions, v)
		}
	case "bookmarks":
		var v Bookmark
		if err = json.Unmarshal(raw, &v); err == nil {
			b.Bookmarks = append(b.Bookmarks, v)
		}
	case "polls":
		var v Poll
		if err = json.Unmarshal(raw, &v); err == nil {
			b.Polls = append(b.Polls, v)
		}
	case "poll_votes":
		var v PollVote
		if err = json.Unmarshal(raw, &v); err == nil {
			b.PollVotes = append(b.PollVotes, v)
		}
	case "emojis":
		var v Emoji
		if err = json.Unmarshal(raw, &v); err == nil {
			b.Emojis = append(b.Emojis, v)
		}
	case "records":
		var v PreservedRecord
		if err = json.Unmarshal(raw, &v); err == nil {
			b.Records = append(b.Records, v)
		}
	case "issues":
		var v Issue
		if err = json.Unmarshal(raw, &v); err == nil {
			b.Issues = append(b.Issues, v)
		}
	default:
		return fmt.Errorf("unknown section %s", section)
	}
	if err != nil {
		return fmt.Errorf("invalid %s record: %w", section, err)
	}
	return nil
}

type idset map[string]bool

func (s idset) add(ids ...string) {
	for _, id := range ids {
		if id != "" {
			s[id] = true
		}
	}
}

// close pulls referenced accounts/media/emojis into the chunk and seeds every
// reference committed by earlier chunks.
func (r *streamRun) close(b *Bundle) error {
	r.o.Seed = map[string]map[string]uint64{}
	for round := 0; round < 8; round++ {
		have := map[string]idset{"account": {}, "media": {}, "emoji": {}, "post": {}, "poll": {}, "list": {}}
		for _, a := range b.Accounts {
			have["account"].add(a.ID)
		}
		for _, m := range b.Media {
			have["media"].add(m.ID)
		}
		for _, e := range b.Emojis {
			have["emoji"].add(e.ID)
		}
		for _, p := range b.Posts {
			have["post"].add(p.ID)
		}
		for _, p := range b.Polls {
			have["poll"].add(p.ID)
		}
		for _, l := range b.Lists {
			have["list"].add(l.ID)
		}
		need := map[string]idset{"account": {}, "media": {}, "emoji": {}, "post": {}, "poll": {}, "list": {}}
		for _, a := range b.Accounts {
			need["media"].add(a.AvatarID, a.HeaderID)
			need["emoji"].add(a.EmojiIDs...)
		}
		for _, m := range b.Media {
			need["account"].add(m.OwnerID)
		}
		for _, e := range b.Emojis {
			need["media"].add(e.MediaID)
		}
		for _, p := range b.Posts {
			need["account"].add(p.AuthorID)
			need["account"].add(p.RecipientIDs...)
			need["media"].add(p.MediaIDs...)
			need["emoji"].add(p.EmojiIDs...)
			need["post"].add(p.ReplyID, p.ReblogID)
		}
		for _, v := range b.Relations {
			need["account"].add(v.AccountID, v.TargetID)
		}
		for _, l := range b.Lists {
			need["account"].add(l.OwnerID)
			need["account"].add(l.MemberIDs...)
		}
		for _, v := range b.Reactions {
			need["account"].add(v.AccountID)
			need["post"].add(v.PostID)
		}
		for _, v := range b.Bookmarks {
			need["account"].add(v.AccountID)
			need["post"].add(v.PostID)
		}
		for _, p := range b.Polls {
			need["post"].add(p.PostID)
		}
		for _, v := range b.PollVotes {
			need["account"].add(v.AccountID)
			need["poll"].add(v.PollID)
		}
		for _, c := range b.Credentials {
			need["account"].add(c.AccountID)
		}
		for _, k := range b.Keys {
			need["account"].add(k.AccountID)
		}
		for _, rec := range b.Records {
			need["account"].add(rec.OwnerID)
		}
		added := false
		for kind, ids := range need {
			var missing []string
			for id := range ids {
				if !have[kind][id] && r.o.Seed[kind][id] == 0 {
					missing = append(missing, id)
				}
			}
			if len(missing) == 0 {
				continue
			}
			seeded, err := r.seed(kind, missing)
			if err != nil {
				return err
			}
			if kind == "post" {
				var unresolved []string
				for _, id := range missing {
					if seeded[id] == 0 {
						unresolved = append(unresolved, id)
					}
				}
				if len(unresolved) > 0 {
					if _, err := r.seed(ArchivedPostKind, unresolved); err != nil {
						return err
					}
				}
			}
			// Accounts are always carried in full: posts and keys consult the
			// source account (local or remote) even when it was committed before.
			var pull []string
			for _, id := range missing {
				if kind == "account" || seeded[id] == 0 {
					pull = append(pull, id)
				}
			}
			section := map[string]string{"account": "accounts", "media": "media", "emoji": "emojis"}[kind]
			if section == "" || len(pull) == 0 {
				continue
			}
			raws, err := r.sp.lookup(section, "id", pull)
			if err != nil {
				return err
			}
			for _, raw := range raws {
				if err := appendRecord(b, section, raw); err != nil {
					return err
				}
				added = true
			}
		}
		if !added {
			break
		}
	}
	// Local accounts need their credentials and signing keys in the same bundle.
	var local []string
	for _, a := range b.Accounts {
		if a.Local {
			local = append(local, a.ID)
		}
	}
	haveCred, haveKey := idset{}, idset{}
	for _, c := range b.Credentials {
		haveCred.add(c.AccountID)
	}
	for _, k := range b.Keys {
		haveKey.add(k.AccountID + "\n" + k.KeyID)
	}
	creds, err := r.sp.lookup("credentials", "acct", local)
	if err != nil {
		return err
	}
	for _, raw := range creds {
		var c Credential
		if json.Unmarshal(raw, &c) == nil && !haveCred[c.AccountID] {
			haveCred.add(c.AccountID)
			b.Credentials = append(b.Credentials, c)
		}
	}
	keys, err := r.sp.lookup("keys", "acct", local)
	if err != nil {
		return err
	}
	for _, raw := range keys {
		var k FederationKey
		if json.Unmarshal(raw, &k) == nil && !haveKey[k.AccountID+"\n"+k.KeyID] {
			haveKey.add(k.AccountID + "\n" + k.KeyID)
			b.Keys = append(b.Keys, k)
		}
	}
	dedupe(b)
	return nil
}

func dedupe(b *Bundle) {
	seenA, seenM, seenE := idset{}, idset{}, idset{}
	accounts := b.Accounts[:0]
	for _, a := range b.Accounts {
		if !seenA[a.ID] {
			seenA.add(a.ID)
			accounts = append(accounts, a)
		}
	}
	b.Accounts = accounts
	media := b.Media[:0]
	for _, m := range b.Media {
		if !seenM[m.ID] {
			seenM.add(m.ID)
			media = append(media, m)
		}
	}
	b.Media = media
	emojis := b.Emojis[:0]
	for _, e := range b.Emojis {
		if !seenE[e.ID] {
			seenE.add(e.ID)
			emojis = append(emojis, e)
		}
	}
	b.Emojis = emojis
}

// seed resolves already committed source IDs (receipts) and, in preview
// mode, IDs that passed preview in an earlier chunk.
func (r *streamRun) seed(kind string, ids []string) (map[string]uint64, error) {
	out := map[string]uint64{}
	if r.o.Seed[kind] == nil {
		r.o.Seed[kind] = map[string]uint64{}
	}
	if r.conn.Migrator().HasTable(&ImportReceipt{}) {
		for start := 0; start < len(ids); start += 500 {
			part := ids[start:min(start+500, len(ids))]
			hashes := make([]string, len(part))
			for i, id := range part {
				hashes[i] = digestBytes([]byte(id))
			}
			var rows []ImportReceipt
			if err := r.conn.WithContext(r.ctx).Select("source_id", "target_id").Where("namespace = ? AND kind = ? AND source_hash IN ? AND target_id > 0", r.namespace, kind, hashes).Find(&rows).Error; err != nil {
				return nil, err
			}
			for _, row := range rows {
				out[row.SourceID] = row.TargetID
				r.o.Seed[kind][row.SourceID] = row.TargetID
			}
		}
	}
	if !r.so.Apply {
		for start := 0; start < len(ids); start += 500 {
			part := ids[start:min(start+500, len(ids))]
			args := []any{kind}
			for _, id := range part {
				args = append(args, id)
			}
			rows, err := r.sp.db.Query(`SELECT id FROM seen WHERE kind = ? AND id IN (?`+strings.Repeat(",?", len(part)-1)+`)`, args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil && out[id] == 0 {
					out[id] = 1 // placeholder: validated, not committed
					r.o.Seed[kind][id] = 1
				}
			}
			rows.Close()
		}
	}
	return out, nil
}

// deferPosts moves posts whose reply/reblog parent appears later in the spool
// into the next chunk, so parents are always committed first.
func (r *streamRun) deferPosts(posts []Post, raws []json.RawMessage, seqs []int64) ([]Post, []json.RawMessage, []int64, error) {
	inChunk := idset{}
	for _, p := range posts {
		inChunk.add(p.ID)
	}
	var keep []Post
	var carry []json.RawMessage
	var carrySeq []int64
	byID := map[string]int{}
	for i, raw := range raws {
		var k spoolKeys
		_ = json.Unmarshal(raw, &k)
		byID[k.ID] = i
	}
	for _, p := range posts {
		later := false
		for _, parent := range []string{p.ReplyID, p.ReblogID} {
			if parent == "" || inChunk[parent] || r.o.Seed["post"][parent] != 0 || r.o.Seed[ArchivedPostKind][parent] != 0 {
				continue
			}
			var n int
			_ = r.sp.db.QueryRow(`SELECT count(*) FROM rec WHERE section = 'posts' AND id = ?`, parent).Scan(&n)
			if n > 0 {
				later = true
			}
		}
		if i, ok := byID[p.ID]; ok && later {
			carry = append(carry, raws[i])
			carrySeq = append(carrySeq, seqs[i])
			continue
		}
		keep = append(keep, p)
	}
	// A deferred post's own children must wait too.
	for changed := true; changed; {
		changed = false
		deferred := idset{}
		for _, raw := range carry {
			var k spoolKeys
			_ = json.Unmarshal(raw, &k)
			deferred.add(k.ID)
		}
		var next []Post
		for _, p := range keep {
			if deferred[p.ReplyID] || deferred[p.ReblogID] {
				i := byID[p.ID]
				carry = append(carry, raws[i])
				carrySeq = append(carrySeq, seqs[i])
				changed = true
				continue
			}
			next = append(next, p)
		}
		keep = next
	}
	sortSeqPairs(carry, carrySeq)
	return keep, carry, carrySeq, nil
}

func sortSeqPairs(raws []json.RawMessage, seqs []int64) {
	for i := 1; i < len(seqs); i++ {
		for j := i; j > 0 && seqs[j] < seqs[j-1]; j-- {
			seqs[j], seqs[j-1] = seqs[j-1], seqs[j]
			raws[j], raws[j-1] = raws[j-1], raws[j]
		}
	}
}

func (r *streamRun) run(b *Bundle) error {
	if len(b.Accounts)+len(b.Posts)+len(b.Media)+len(b.Emojis)+len(b.Relations)+len(b.Lists)+len(b.Reactions)+len(b.Bookmarks)+len(b.Polls)+len(b.PollVotes)+len(b.Records)+len(b.Issues) == 0 {
		return nil
	}
	var rep *Report
	var err error
	if r.so.Apply {
		rep, err = Import(r.ctx, r.conn, b, r.o, nil)
	} else {
		rep, err = Preview(r.ctx, r.conn, b, r.o)
	}
	if rep != nil {
		r.merge(rep, b, !r.firstDone)
	}
	if err != nil {
		return err
	}
	r.firstDone = true
	if r.so.Apply {
		_ = r.sp.SetMeta("issues_done", "1")
	} else {
		// Records that exist satisfy later references even when this chunk has
		// its own blocking issues; otherwise one error cascades into thousands.
		r.markSeen(b)
	}
	return nil
}

func (r *streamRun) markSeen(b *Bundle) {
	tx, err := r.sp.db.Begin()
	if err != nil {
		return
	}
	ins := func(kind, id string) { _, _ = tx.Exec(`INSERT OR IGNORE INTO seen(kind, id) VALUES(?, ?)`, kind, id) }
	for _, a := range b.Accounts {
		ins("account", a.ID)
	}
	for _, m := range b.Media {
		ins("media", m.ID)
	}
	for _, e := range b.Emojis {
		ins("emoji", e.ID)
	}
	for _, p := range b.Posts {
		ins("post", p.ID)
		if p.ArchiveOnly {
			ins(ArchivedPostKind, p.ID)
		}
	}
	for _, p := range b.Polls {
		ins("poll", p.ID)
	}
	for _, l := range b.Lists {
		ins("list", l.ID)
	}
	_ = tx.Commit()
}

func (r *streamRun) merge(rep *Report, b *Bundle, first bool) {
	t := r.total
	if !rep.CanExecute {
		t.CanExecute = false
	}
	add := func(dst, src map[string]int) {
		for k, v := range src {
			dst[k] += v
		}
	}
	add(t.Created, rep.Created)
	add(t.Skipped, rep.Skipped)
	add(t.Archived, rep.Archived)
	// Counts reflect primary records only; closure copies are not double counted.
	primary := map[string]int{"post": len(b.Posts), "relation": len(b.Relations), "reaction": len(b.Reactions), "bookmark": len(b.Bookmarks),
		"poll": len(b.Polls), "poll_vote": len(b.PollVotes), "list": len(b.Lists), "record": len(b.Records)}
	for k, v := range primary {
		t.Counts[k] += v
	}
	for _, is := range rep.Issues {
		if aggregatedIssue(is.Code) {
			r.widths.add(is)
			continue
		}
		// Accounts pulled into several chunks would repeat the same issue.
		key := is.Severity + "\x00" + is.Code + "\x00" + is.Kind + "\x00" + is.SourceID + "\x00" + is.Message
		if r.seenIssues[key] && !first {
			continue
		}
		if len(r.seenIssues) < 4*r.so.MaxIssues {
			r.seenIssues[key] = true
		}
		if len(t.Issues) >= r.so.MaxIssues {
			r.dropped++
			continue
		}
		t.Issues = append(t.Issues, is)
	}
}
