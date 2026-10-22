package discovery

import (
	"context"
	"sync"
	"time"

	"gorm.io/gorm"
)

// anonymousTagTrendTTL bounds how long the signed-out /trends/tags result is
// reused. Every request otherwise re-ran the 7-day status_tags/tags/statuses
// aggregation; signed-out results do not depend on the viewer, so a short
// shared TTL removes almost all of that load while staying fresh.
var anonymousTagTrendTTL = 30 * time.Second

type anonymousTagTrendEntry struct {
	at      time.Time
	rows    []TagTrend
	partial bool
}

var anonymousTagTrends = struct {
	sync.Mutex
	entries map[string]anonymousTagTrendEntry
}{entries: map[string]anonymousTagTrendEntry{}}

// AnonymousTagTrendsPartial is AggregateTagTrendsPartial for a signed-out
// viewer with a short in-process cache. Callers must not mutate the rows.
func AnonymousTagTrendsPartial(ctx context.Context, conn *gorm.DB, window, scope string, now time.Time) ([]TagTrend, bool, error) {
	key := window + "|" + scope
	anonymousTagTrends.Lock()
	entry, ok := anonymousTagTrends.entries[key]
	anonymousTagTrends.Unlock()
	if ok && now.Sub(entry.at) >= 0 && now.Sub(entry.at) < anonymousTagTrendTTL {
		return entry.rows, entry.partial, nil
	}
	rows, partial, err := AggregateTagTrendsPartial(ctx, conn, nil, window, scope, now, nil)
	if err != nil {
		return nil, false, err
	}
	anonymousTagTrends.Lock()
	anonymousTagTrends.entries[key] = anonymousTagTrendEntry{at: now, rows: rows, partial: partial}
	anonymousTagTrends.Unlock()
	return rows, partial, nil
}

// ResetAnonymousTagTrendCacheForTest clears the cache.
func ResetAnonymousTagTrendCacheForTest() {
	anonymousTagTrends.Lock()
	anonymousTagTrends.entries = map[string]anonymousTagTrendEntry{}
	anonymousTagTrends.Unlock()
}
