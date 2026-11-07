package discovery

import (
	"strings"
	"time"

	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

// suggestionActivityWindow is the closed interval of authored public posts
// that can qualify an account for follow suggestions.
const suggestionActivityWindow = 14 * 24 * time.Hour

// suggestionAsOf is the caller's clock, or the current UTC time.
func suggestionAsOf(opt SuggestOptions) time.Time {
	if opt.Now.IsZero() {
		return time.Now().UTC()
	}
	return opt.Now.UTC()
}

// authorActivityQuery is the stored-post evidence for follow suggestions.
// Local and remote accounts use the same rows. Nothing is fetched from a
// remote outbox, and no inactivity flag is written.
//
// A qualifying row is an authored public post already stored here:
//   - visibility public, not deleted, not scheduled, and created_at inside
//     [asOf-14d, asOf] (the cutoff and the clock instant are included;
//     a later created_at is future and does not count)
//   - not backfilled: profile imports and other late copies never enter the
//     public timeline, so an old post ingested now does not count even when
//     its row id or updated_at is new. created_at is the authored time
//   - not a reblog. A boost is the booster's announce, not an authored post,
//     and it does not move the original author's created_at
//   - not a mirror of a post on this instance. Those rows duplicate a local
//     status; the author's own row is the evidence
//
// Replies count. A public reply is authored and is eligible for the public
// timeline, unlike recommendation roots, which drop replies. Private,
// unlisted, and direct posts do not count, so a suggestion cannot reveal
// that a non-public post exists. A public local-only post counts: the public
// timeline candidate query does not remove it, and this check does not
// return the post.
func authorActivityQuery(conn *gorm.DB, asOf time.Time) *gorm.DB {
	asOf = asOf.UTC()
	backfill := dialect.JSONText(conn, "statuses.properties", models.StatusPropBackfilled)
	if dialect.IsMySQL(conn) {
		backfill = "JSON_UNQUOTE(" + backfill + ")"
	}
	q := conn.Session(&gorm.Session{NewDB: true}).Model(&models.Status{}).
		Where("statuses.visibility = ?", models.StatusVisibilityPublic).
		Where("statuses.deleted_at IS NULL AND statuses.scheduled_at IS NULL").
		Where("(statuses.reblog_id IS NULL OR statuses.reblog_id = 0)").
		Where("statuses.created_at >= ? AND statuses.created_at <= ?", asOf.Add(-suggestionActivityWindow), asOf).
		Where("COALESCE("+backfill+", '') <> ?", "1")
	return excludeLocalMirrorOrigins(conn, q)
}

// excludeLocalMirrorOrigins drops remote copies whose origin points at this
// instance. A genuine remote post keeps its own origin host. Rows with an
// empty origin are local posts and stay eligible.
func excludeLocalMirrorOrigins(conn *gorm.DB, q *gorm.DB) *gorm.DB {
	base := strings.TrimRight(systemsettings.GetBaseURL(), "/")
	if base == "" {
		return q
	}
	prefix := escapeLike(base) + "/%"
	esc := dialect.LikeEscape(conn)
	return q.Where(
		"(COALESCE(statuses.origin_uri, '') = '' OR (statuses.origin_uri NOT LIKE ? "+esc+" AND statuses.origin_uri <> ?)) AND (COALESCE(statuses.origin_url, '') = '' OR (statuses.origin_url NOT LIKE ? "+esc+" AND statuses.origin_url <> ?))",
		prefix, base, prefix, base,
	)
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

// suggestionCandidateQuery is the cold-start and refill pool. Activity is
// required before LIMIT so inactive accounts, including ones with a fresh
// last_status_at, cannot fill the cap.
func suggestionCandidateQuery(conn *gorm.DB, asOf time.Time) *gorm.DB {
	return customAvatarQuery(conn).Where("EXISTS (?)",
		authorActivityQuery(conn, asOf).Select("1").Where("statuses.account_id = accounts.id"))
}

// recentlyActiveAuthorIDs rechecks a graph or cached id set against current
// rows. Deletion, visibility, and the moving cutoff are read here; a miss
// only drops the id for this call.
func recentlyActiveAuthorIDs(conn *gorm.DB, ids []uint64, asOf time.Time) (map[uint64]bool, error) {
	active := make(map[uint64]bool, len(ids))
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		end := min(start+chunk, len(ids))
		var found []uint64
		err := authorActivityQuery(conn, asOf).
			Where("statuses.account_id IN ?", ids[start:end]).
			Distinct("statuses.account_id").
			Pluck("statuses.account_id", &found).Error
		if err != nil {
			return nil, err
		}
		for _, id := range found {
			active[id] = true
		}
	}
	return active, nil
}
