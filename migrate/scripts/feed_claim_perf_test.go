package scripts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestFeedClaimPerfPostgres is an opt-in benchmark: FEED_PERF_ROWS=300000 plus
// TEST_DATABASE_URL. FEED_PERF_APPLY=1 applies the claim-index migration first,
// so the same test prints "before" and "after" numbers.
func TestFeedClaimPerfPostgres(t *testing.T) {
	rows := 0
	fmt.Sscan(os.Getenv("FEED_PERF_ROWS"), &rows)
	if rows == 0 || !pgtest.Enabled() {
		t.Skip("set FEED_PERF_ROWS and TEST_DATABASE_URL")
	}
	var notFound atomic.Int64
	conn := pgtest.Open(t, &gorm.Config{Logger: countingLogger{&notFound}, DisableForeignKeyConstraintWhenMigrating: true})
	sqlDB, _ := conn.DB()
	sqlDB.SetMaxOpenConns(16)
	require.NoError(t, conn.AutoMigrate(&models.FeedEvent{}, &models.FeedBatch{}))
	if os.Getenv("FEED_PERF_APPLY") == "1" {
		require.NoError(t, feedClaimIndexesUp(conn))
	}
	for _, table := range []string{"feed_events", "feed_batches"} {
		extra, cols, vals := "", "", ""
		if table == "feed_events" {
			cols, vals = "event_key,kind,object_type,object_id,actor_id,object_version,metadata,", "'e'||g,'status_create','status',g,1,1,'{}',"
		} else {
			cols, vals = "event_id,target_kind,batch_no,recipients,", "g,'home',1,'[]',"
		}
		// 99% complete history; ~1000 due pending; ~500 leased by live workers;
		// ~500 retry-scheduled in the future.
		require.NoError(t, conn.Exec(fmt.Sprintf(`INSERT INTO %s (%s state,lease_generation,lease_token,lease_expires_at,next_attempt_at,attempts,last_error_code,created_at,updated_at)
SELECT %s CASE WHEN g%%300<1 THEN 'pending' WHEN g%%3000=1 THEN 'processing' WHEN g%%300=2 THEN 'pending' ELSE 'complete' END,
 1,'', CASE WHEN g%%3000=1 THEN now()+interval '25 seconds' END,
 CASE WHEN g%%300=2 THEN now()+interval '1 hour' END,0,'',now()-(interval '1 second')*(%d-g)/50,now()
FROM generate_series(1,%d) g %s`, table, cols, vals, rows, rows, extra)).Error)
	}
	require.NoError(t, conn.Exec("ANALYZE feed_events").Error)
	require.NoError(t, conn.Exec("ANALYZE feed_batches").Error)

	// Idle poll: drain due rows first.
	drain := func(claim func() (int, error)) int {
		n := 0
		for {
			c, err := claim()
			require.NoError(t, err)
			if c == 0 {
				return n
			}
			n += c
		}
	}
	events := func() (int, error) { r, err := models.ClaimFeedEvents(conn, 1, 30*time.Second); return len(r), err }
	batches := func() (int, error) { r, err := models.ClaimFeedBatches(conn, 1, 30*time.Second); return len(r), err }

	notFound.Store(0)
	start := time.Now()
	var wg sync.WaitGroup
	var claimed atomic.Int64
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f := events
			if i >= 2 {
				f = batches
			}
			claimed.Add(int64(drain(f)))
		}(i)
	}
	wg.Wait()
	t.Logf("rows=%d concurrent drain: claimed=%d in %s (%.2f ms/claim), gorm record-not-found lines=%d", rows, claimed.Load(), time.Since(start), float64(time.Since(start).Milliseconds())/float64(claimed.Load()), notFound.Load())

	// Idle: nothing due any more (finish what the drain leased, like real workers do).
	require.NoError(t, conn.Exec("UPDATE feed_events SET state='complete', lease_expires_at=NULL WHERE state='processing' AND lease_expires_at > now()").Error)
	require.NoError(t, conn.Exec("UPDATE feed_batches SET state='complete', lease_expires_at=NULL WHERE state='processing' AND lease_expires_at > now()").Error)
	require.NoError(t, conn.Exec("ANALYZE feed_events").Error)
	require.NoError(t, conn.Exec("ANALYZE feed_batches").Error)
	notFound.Store(0)
	const polls = 300
	start = time.Now()
	for i := 0; i < polls; i++ {
		_, _ = events()
		_, _ = batches()
	}
	t.Logf("idle poll: %d polls (events+batches) avg %.3f ms each, record-not-found lines=%d", 2*polls, float64(time.Since(start).Microseconds())/1000/float64(2*polls), notFound.Load())

	// A single lease UPDATE by primary key (what Renew/Complete/Retry do).
	var plan []string
	require.NoError(t, conn.Raw("EXPLAIN (ANALYZE, BUFFERS) SELECT id FROM feed_events WHERE state IN ('pending','processing') AND (next_attempt_at IS NULL OR next_attempt_at <= now()) AND (lease_expires_at IS NULL OR lease_expires_at <= now()) AND COALESCE(next_attempt_at, created_at) <= now() + interval '5 minutes' ORDER BY COALESCE(next_attempt_at, created_at) ASC, id ASC LIMIT 1 FOR UPDATE SKIP LOCKED").Scan(&plan).Error)
	t.Logf("plan:\n%s", strings.Join(plan, "\n"))
}

type countingLogger struct{ n *atomic.Int64 }

func (l countingLogger) LogMode(logger.LogLevel) logger.Interface { return l }
func (countingLogger) Info(context.Context, string, ...any)       {}
func (countingLogger) Warn(context.Context, string, ...any)       {}
func (countingLogger) Error(context.Context, string, ...any)      {}
func (l countingLogger) Trace(_ context.Context, _ time.Time, _ func() (string, int64), err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		l.n.Add(1)
	}
}
