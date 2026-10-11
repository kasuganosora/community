package models

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// recordingLogger captures statements and GORM errors for assertions.
type recordingLogger struct {
	mu       *sync.Mutex
	sql      *[]string
	notFound *int
}

func newRecordingLogger() recordingLogger {
	return recordingLogger{&sync.Mutex{}, &[]string{}, new(int)}
}
func (l recordingLogger) LogMode(logger.LogLevel) logger.Interface { return l }
func (recordingLogger) Info(context.Context, string, ...any)       {}
func (recordingLogger) Warn(context.Context, string, ...any)       {}
func (recordingLogger) Error(context.Context, string, ...any)      {}
func (l recordingLogger) Trace(_ context.Context, _ time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	l.mu.Lock()
	defer l.mu.Unlock()
	*l.sql = append(*l.sql, sql)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		*l.notFound++
	}
}
func (l recordingLogger) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	*l.sql, *l.notFound = nil, 0
}
func (l recordingLogger) updates() (n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range *l.sql {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(s)), "UPDATE") {
			n++
		}
	}
	return
}

func feedClaimDBs(t *testing.T) map[string]*gorm.DB {
	t.Helper()
	dbs := map[string]*gorm.DB{"default": feedTestDB(t)}
	if pgtest.Enabled() {
		conn := pgtest.Open(t, &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
		require.NoError(t, conn.AutoMigrate(&FeedEvent{}, &FeedBatch{}))
		dbs["postgres"] = conn
	}
	return dbs
}

func TestFeedClaimNoWorkIsQuietAndWritesNothing(t *testing.T) {
	for name, base := range feedClaimDBs(t) {
		t.Run(name, func(t *testing.T) {
			rec := newRecordingLogger()
			conn := base.Session(&gorm.Session{Logger: rec})
			for i := 0; i < 5; i++ {
				events, err := ClaimFeedEvents(conn, 1, 30*time.Second)
				require.NoError(t, err)
				require.Empty(t, events)
				batches, err := ClaimFeedBatches(conn, 1, 30*time.Second)
				require.NoError(t, err)
				require.Empty(t, batches)
			}
			require.Zero(t, rec.updates(), "an empty poll must not issue UPDATE")
			require.Zero(t, *rec.notFound, "an empty poll must not log record not found")

			// Rows that exist but are not claimable (future retry, live lease) are also "no work".
			future := time.Now().UTC().Add(time.Hour)
			require.NoError(t, base.Create(&FeedEvent{EventKey: "later", Kind: FeedStatusCreate, ObjectType: "status", ObjectID: 1, ActorID: 1, ObjectVersion: 1, FeedLease: FeedLease{NextAttemptAt: &future}}).Error)
			rec.reset()
			events, err := ClaimFeedEvents(conn, 1, 30*time.Second)
			require.NoError(t, err)
			require.Empty(t, events)
			require.Zero(t, rec.updates())
			require.Zero(t, *rec.notFound)
		})
	}
}

func TestFeedClaimContentionNeverLogsNotFoundOrDoubleClaims(t *testing.T) {
	for name, base := range feedClaimDBs(t) {
		t.Run(name, func(t *testing.T) {
			if name == "default" {
				// feedTestDB pins SQLite to one connection, so claimers serialize.
				t.Log("default engine serializes claimers; contention is exercised on PostgreSQL")
			}
			rec := newRecordingLogger()
			conn := base.Session(&gorm.Session{Logger: rec})
			const total = 40
			for i := 0; i < total; i++ {
				require.NoError(t, base.Create(&FeedBatch{EventID: uint64(i + 1), TargetKind: "home", BatchNo: 1, Recipients: []FeedRecipient{{AccountID: 1}}}).Error)
			}
			rec.reset()
			var wg sync.WaitGroup
			var mu sync.Mutex
			seen := map[uint64]int{}
			for w := 0; w < 6; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						rows, err := ClaimFeedBatches(conn, 1, 30*time.Second)
						if err != nil {
							t.Error(err)
							return
						}
						if len(rows) == 0 {
							return
						}
						mu.Lock()
						seen[rows[0].ID]++
						mu.Unlock()
					}
				}()
			}
			wg.Wait()
			require.Len(t, seen, total)
			for id, n := range seen {
				require.Equal(t, 1, n, "batch %d claimed twice", id)
			}
			require.Zero(t, *rec.notFound, "losing a claim race must not log record not found")
		})
	}
}

func TestFeedClaimOrdersByDueTimeAndHonorsLimit(t *testing.T) {
	for name, base := range feedClaimDBs(t) {
		t.Run(name, func(t *testing.T) {
			past := time.Now().UTC().Add(-time.Minute)
			older := time.Now().UTC().Add(-time.Hour)
			mk := func(key string, next *time.Time, created time.Time) {
				e := FeedEvent{EventKey: key, Kind: FeedStatusCreate, ObjectType: "status", ObjectID: 1, ActorID: 1, ObjectVersion: 1, FeedLease: FeedLease{NextAttemptAt: next, CreatedAt: created}}
				require.NoError(t, base.Create(&e).Error)
			}
			mk("retry-due", &past, time.Now().UTC().Add(-2*time.Hour))
			mk("oldest", nil, older)
			mk("newest", nil, time.Now().UTC())
			claims, err := ClaimFeedEvents(base, 2, 30*time.Second)
			require.NoError(t, err)
			require.Len(t, claims, 2)
			require.Equal(t, []string{"oldest", "retry-due"}, []string{claims[0].EventKey, claims[1].EventKey})
			require.Equal(t, "processing", claims[0].State)
			require.Equal(t, uint64(1), claims[0].LeaseGeneration)
		})
	}
}
