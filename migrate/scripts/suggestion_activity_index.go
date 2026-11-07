package scripts

import "gorm.io/gorm"

// SuggestionActivityIndexScript adds idx_statuses_account_created_at on
// statuses(account_id, created_at). Follow suggestions probe "does this
// account have a public post since the cutoff?" before LIMIT, both for an
// IN-list of graph candidates and for the correlated cold-start EXISTS.
// The existing single-column account_id index still walks an author's whole
// history, and the created_at index walks every recent row in the table.
// The pair serves the per-account time range; visibility, deletion, reblog,
// and the backfill marker stay residual predicates on that short slice.
// Idempotent and online, same as the other hot-table indexes. No wider
// column list: those filters are not selective inside a 14-day slice.
type SuggestionActivityIndexScript struct{}

func (SuggestionActivityIndexScript) NonTransactional() bool { return true }

func (SuggestionActivityIndexScript) Up(db *gorm.DB) error {
	return createOnlineIndex(db, onlineIndex{
		name:   "idx_statuses_account_created_at",
		table:  "statuses",
		pg:     "(account_id, created_at)",
		mysql:  "(account_id, created_at)",
		sqlite: "(account_id, created_at)",
	})
}

func (SuggestionActivityIndexScript) Down(*gorm.DB) error { return nil }
