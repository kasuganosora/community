package history

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redisdb "github.com/kasuganosora/community/db/redis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestHistoryBatchAsyncSyncPauseAndClear(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		Close()
		redisdb.SetUniversalRedis(nil)
		_ = client.Close()
	})
	redisdb.SetUniversalRedis(client)

	ctx := context.Background()
	require.NoError(t, RecordSync(ctx, 1, 100))
	time.Sleep(5 * time.Millisecond)
	RecordAsync(1, 200)
	RecordAsync(1, 300)
	// Close forces immediate flush of sharded pipeline micro-batches.
	Close()

	entries, err := List(ctx, 1, 0, 10)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	require.Equal(t, uint64(100), entries[len(entries)-1].StatusID)

	// Pause stops new recordings.
	require.NoError(t, SetPaused(ctx, 1, true))
	paused, err := IsPaused(ctx, 1)
	require.NoError(t, err)
	require.True(t, paused)
	require.NoError(t, RecordSync(ctx, 1, 400))
	entriesAfterPause, err := List(ctx, 1, 0, 10)
	require.NoError(t, err)
	require.Len(t, entriesAfterPause, 3)

	// Remove single item & clear all.
	require.NoError(t, Remove(ctx, 1, 200))
	entriesAfterRemove, err := List(ctx, 1, 0, 10)
	require.NoError(t, err)
	require.Len(t, entriesAfterRemove, 2)

	require.NoError(t, Clear(ctx, 1))
	entriesAfterClear, err := List(ctx, 1, 0, 10)
	require.NoError(t, err)
	require.Empty(t, entriesAfterClear)
}

func TestHistoryMemoryFallbackTieCursorAndClearWatermark(t *testing.T) {
	redisdb.SetUniversalRedis(nil)
	ResetForTest()
	t.Cleanup(ResetForTest)

	ctx := context.Background()
	// Enqueue async item, immediately Clear with zero sleep, then immediately RecordAsync a new item:
	// pre-Clear 901 must be dropped while post-Clear 902 must be kept.
	RecordAsync(7, 901)
	require.NoError(t, Clear(ctx, 7))
	RecordAsync(7, 902)
	Close()
	afterClear, err := List(ctx, 7, 0, 10)
	require.NoError(t, err)
	require.Len(t, afterClear, 1)
	require.Equal(t, uint64(902), afterClear[0].StatusID)

	// Same-millisecond tie pagination via ListCursor.
	memMu.Lock()
	acct := getOrCreateMemAccountLocked(8)
	acct.items[10] = 1700000000500
	acct.items[20] = 1700000000500
	acct.items[30] = 1700000000400
	memMu.Unlock()

	p1, err := ListCursor(ctx, 8, 0, 0, 1)
	require.NoError(t, err)
	require.Len(t, p1, 1)
	require.Equal(t, uint64(20), p1[0].StatusID)

	ms1, sid1 := ParseCursor(FormatCursor(p1[0]))
	p2, err := ListCursor(ctx, 8, ms1, sid1, 1)
	require.NoError(t, err)
	require.Len(t, p2, 1)
	require.Equal(t, uint64(10), p2[0].StatusID)

	ms2, sid2 := ParseCursor(FormatCursor(p2[0]))
	p3, err := ListCursor(ctx, 8, ms2, sid2, 2)
	require.NoError(t, err)
	require.Len(t, p3, 1)
	require.Equal(t, uint64(30), p3[0].StatusID)
}
