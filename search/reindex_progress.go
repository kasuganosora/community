package search

import "sync"

var (
	reindexProgressMu sync.RWMutex
	reindexProgress   func() any
)

// SetReindexProgress registers the index backfill progress reporter
// (search/indexsync), so callers can read it without importing indexsync and
// its event hooks.
func SetReindexProgress(fn func() any) {
	reindexProgressMu.Lock()
	reindexProgress = fn
	reindexProgressMu.Unlock()
}

// ReindexProgress returns the backfill progress ("search index done/total"),
// or nil when no indexer is linked into this binary.
func ReindexProgress() any {
	reindexProgressMu.RLock()
	fn := reindexProgress
	reindexProgressMu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn()
}
