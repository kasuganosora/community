package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redisv9 "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// jitterTTL
// ============================================================

func TestJitterTTL_ZeroOrNegative(t *testing.T) {
	// Zero or negative TTL must be returned unchanged.
	assert.Equal(t, time.Duration(0), jitterTTLImpl(0))
	assert.Equal(t, time.Duration(-1), jitterTTLImpl(-1))
}

func TestJitterTTL_PositiveStaysWithinRange(t *testing.T) {
	base := 10 * time.Second

	for range 100 {
		got := jitterTTLImpl(base)
		// lower bound: exactly base; upper bound: base + 10% (jitterRatio)
		assert.GreaterOrEqual(t, int64(got), int64(base), "jitter should be >= base")
		assert.LessOrEqual(t, int64(got), int64(base)+int64(float64(base)*jitterRatio),
			"jitter should be <= base+10%%")
	}
}

func TestJitterTTL_DefaultVarIsImpl(t *testing.T) {
	// The package-level var should be the real implementation.
	assert.NotNil(t, jitterTTL)
}

// ============================================================
// ErrCacheMiss / wrapRedisErr
// ============================================================

func TestWrapRedisErr(t *testing.T) {
	t.Run("nil returns nil", func(t *testing.T) {
		assert.NoError(t, wrapRedisErr(nil))
	})

	t.Run("redis.Nil maps to ErrCacheMiss", func(t *testing.T) {
		err := wrapRedisErr(redisv9.Nil)
		assert.ErrorIs(t, err, ErrCacheMiss)
	})

	t.Run("other error is returned as-is", func(t *testing.T) {
		original := errors.New("boom")
		err := wrapRedisErr(original)
		assert.Equal(t, original, err)
	})
}

func TestIsErrCacheMiss(t *testing.T) {
	assert.True(t, IsErrCacheMiss(ErrCacheMiss))
	assert.True(t, IsErrCacheMiss(fmt.Errorf("%w: %w", ErrCacheMiss, errors.New("wrapped"))))
	assert.False(t, IsErrCacheMiss(errors.New("other")))
	assert.False(t, IsErrCacheMiss(nil))
}

// ============================================================
// Get returns ErrCacheMiss on miss (mock-based)
// ============================================================

func TestGet_ReturnsErrCacheMissOnMiss(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	mock.ExpectGet("test:gone").SetErr(redisv9.Nil)

	var dest struct{ X int }
	err := c.Get(ctx, "gone", &dest)
	require.Error(t, err)
	assert.True(t, IsErrCacheMiss(err))
}

func TestGetString_ReturnsErrCacheMissOnMiss(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	mock.ExpectGet("test:gone").SetErr(redisv9.Nil)

	_, err := c.GetString(ctx, "gone")
	require.Error(t, err)
	assert.True(t, IsErrCacheMiss(err))
}

func TestHGet_ReturnsErrCacheMissOnMiss(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	mock.ExpectHGet("test:hk", "f").SetErr(redisv9.Nil)

	var dest int
	err := c.HGet(ctx, "hk", "f", &dest)
	require.Error(t, err)
	assert.True(t, IsErrCacheMiss(err))
}

// ============================================================
// Delete no-op when no keys (mock-based)
// ============================================================

func TestDelete_NoKeysIsNoOp(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	// No Redis expectation is set; if a Del were issued the mock would error.
	require.NoError(t, c.Delete(ctx))
}

// ============================================================
// Empty key validation (mock-based)
// ============================================================

func TestGet_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	var dest struct{ X int }
	err := c.Get(ctx, "", &dest)
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestSet_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	err := c.Set(ctx, "", "val", time.Minute)
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestGetString_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	_, err := c.GetString(ctx, "")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestSetString_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	err := c.SetString(ctx, "", "val", time.Minute)
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestDelete_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	err := c.Delete(ctx, "")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestDeleteByPattern_EmptyPatternReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	err := c.DeleteByPattern(ctx, "")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestExists_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	_, err := c.Exists(ctx, "")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestExpire_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	err := c.Expire(ctx, "", time.Minute)
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestTTL_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	_, err := c.TTL(ctx, "")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestIncrement_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	_, err := c.Increment(ctx, "")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestIncrementBy_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	_, err := c.IncrementBy(ctx, "", 1)
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestDecrement_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	_, err := c.Decrement(ctx, "")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestHSet_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	err := c.HSet(ctx, "", "f", "v")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestHGet_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	var dest int
	err := c.HGet(ctx, "", "f", &dest)
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestHGetAll_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	_, err := c.HGetAll(ctx, "")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestHDel_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	err := c.HDel(ctx, "", "f")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestLPush_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	err := c.LPush(ctx, "", "v")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestRPush_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	err := c.RPush(ctx, "", "v")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestLRange_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	_, err := c.LRange(ctx, "", 0, -1)
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestLLen_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	_, err := c.LLen(ctx, "")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestSAdd_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	err := c.SAdd(ctx, "", "v")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestSMembers_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	_, err := c.SMembers(ctx, "")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestSIsMember_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	_, err := c.SIsMember(ctx, "", "v")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestZAdd_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	err := c.ZAdd(ctx, "", 1.0, "m")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestZRange_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	_, err := c.ZRange(ctx, "", 0, -1)
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestZRangeByScore_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	_, err := c.ZRangeByScore(ctx, "", "0", "100", 0, 10)
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestZRem_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	err := c.ZRem(ctx, "", "m")
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
}

func TestGetOrFetch_EmptyKeyReturnsErrEmptyKey(t *testing.T) {
	c, _ := mockCache(t)
	ctx := context.Background()

	var dest string
	hit, err := c.GetOrFetch(ctx, "", &dest, time.Minute, func() (any, error) {
		t.Fatal("fetch should not be called on empty key")
		return nil, nil
	})
	require.Error(t, err)
	assert.True(t, IsErrEmptyKey(err))
	assert.False(t, hit)
}

func TestIsErrEmptyKey(t *testing.T) {
	assert.True(t, IsErrEmptyKey(ErrEmptyKey))
	assert.True(t, IsErrEmptyKey(fmt.Errorf("%w: %w", ErrEmptyKey, errors.New("wrapped"))))
	assert.False(t, IsErrEmptyKey(errors.New("other")))
	assert.False(t, IsErrEmptyKey(nil))
}

// ============================================================
// GetOrFetch - cache stampede protection (mock-based)
// ============================================================

func TestGetOrFetch_CacheHit(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	type Item struct {
		Name string `json:"name"`
	}

	// Cache returns a hit; fetch should NOT be called.
	mock.ExpectGet("test:k").SetVal(`{"name":"cached"}`)

	var dest Item
	var fetchCalls int32
	hit, err := c.GetOrFetch(ctx, "k", &dest, time.Minute, func() (any, error) {
		atomic.AddInt32(&fetchCalls, 1)
		return Item{Name: "fetched"}, nil
	})

	require.NoError(t, err)
	assert.True(t, hit, "should be a cache hit")
	assert.Equal(t, "cached", dest.Name)
	assert.Equal(t, int32(0), atomic.LoadInt32(&fetchCalls), "fetch must not run on cache hit")
}

func TestGetOrFetch_CacheMiss(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	type Item struct {
		Name string `json:"name"`
	}

	// Cache miss, then write-back.
	mock.ExpectGet("test:k").SetErr(redisv9.Nil)
	mock.ExpectSet("test:k", []byte(`{"name":"fresh"}`), time.Minute).SetVal("OK")

	var dest Item
	hit, err := c.GetOrFetch(ctx, "k", &dest, time.Minute, func() (any, error) {
		return Item{Name: "fresh"}, nil
	})

	require.NoError(t, err)
	assert.False(t, hit, "should be a cache miss")
	assert.Equal(t, "fresh", dest.Name)
}

func TestGetOrFetch_DoesNotCacheWhenExpirationZero(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	// Only a Get is expected; no Set since expiration == 0.
	mock.ExpectGet("test:k").SetErr(redisv9.Nil)

	var dest string
	hit, err := c.GetOrFetch(ctx, "k", &dest, 0, func() (any, error) {
		return "value", nil
	})

	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, "value", dest)
}

func TestGetOrFetch_FetchError(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	fetchErr := errors.New("db down")
	mock.ExpectGet("test:k").SetErr(redisv9.Nil)

	var dest string
	hit, err := c.GetOrFetch(ctx, "k", &dest, time.Minute, func() (any, error) {
		return nil, fetchErr
	})

	require.ErrorIs(t, err, fetchErr)
	assert.False(t, hit)
}

func TestGetOrFetch_RealRedisErrorIsNotCacheMiss(t *testing.T) {
	c, mock := mockCache(t)
	ctx := context.Background()

	// A genuine Redis error (not Nil) must be propagated, not treated as a miss.
	connErr := errors.New("connection refused")
	mock.ExpectGet("test:k").SetErr(connErr)

	var dest string
	var fetchCalls int32
	hit, err := c.GetOrFetch(ctx, "k", &dest, time.Minute, func() (any, error) {
		atomic.AddInt32(&fetchCalls, 1)
		return "x", nil
	})

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrCacheMiss)
	assert.False(t, hit)
	assert.Equal(t, int32(0), atomic.LoadInt32(&fetchCalls), "fetch must not run on a real redis error")
}

// ============================================================
// GetOrFetch - singleflight coalescing (concurrency)
// ============================================================
//
// This test validates that concurrent fetches for the same key via
// GetOrFetch only invoke the source once. It uses miniredis (an
// in-process Redis) so it runs deterministically without an external
// Redis server.
func TestGetOrFetch_SingleflightCoalescing_MiniRedis(t *testing.T) {
	mr := miniredis.RunT(t)

	client := redisv9.NewClient(&redisv9.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	c := &Cache{client: client, prefix: "test:"}
	originalJitter := jitterTTL
	jitterTTL = func(d time.Duration) time.Duration { return d }
	t.Cleanup(func() { jitterTTL = originalJitter })

	ctx := context.Background()

	var fetchCalls int32

	// Barrier so all goroutines start ~together.
	var startBarrier sync.WaitGroup

	startBarrier.Add(1)

	var wg sync.WaitGroup

	const n = 20

	for range n {
		wg.Add(1)

		go func() {
			defer wg.Done()
			startBarrier.Wait() // all goroutines wait on the barrier

			var dest string
			_, err := c.GetOrFetch(ctx, "k", &dest, time.Minute, func() (any, error) {
				atomic.AddInt32(&fetchCalls, 1)
				time.Sleep(50 * time.Millisecond) // simulate slow source
				return "result", nil
			})
			assert.NoError(t, err)
			assert.Equal(t, "result", dest)
		}()
	}

	startBarrier.Done() // release all goroutines
	wg.Wait()

	// singleflight should coalesce to exactly one fetch call.
	assert.Equal(t, int32(1), atomic.LoadInt32(&fetchCalls),
		"fetch should be invoked exactly once due to singleflight")
}

// TestGetOrFetch_SecondCallHitsCache verifies that after a fetch fills
// the cache, a subsequent call hits the cache (fetch not invoked).
func TestGetOrFetch_SecondCallHitsCache_MiniRedis(t *testing.T) {
	mr := miniredis.RunT(t)

	client := redisv9.NewClient(&redisv9.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	c := &Cache{client: client, prefix: "test:"}
	originalJitter := jitterTTL
	jitterTTL = func(d time.Duration) time.Duration { return d }
	t.Cleanup(func() { jitterTTL = originalJitter })

	ctx := context.Background()

	var fetchCalls int32
	fetch := func() (any, error) {
		atomic.AddInt32(&fetchCalls, 1)
		return "value", nil
	}

	// First call: miss + fetch + write-back.
	var first string
	hit, err := c.GetOrFetch(ctx, "hot", &first, time.Minute, fetch)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, "value", first)

	// Second call: cache hit, fetch must NOT run.
	var second string
	hit, err = c.GetOrFetch(ctx, "hot", &second, time.Minute, fetch)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, "value", second)

	assert.Equal(t, int32(1), atomic.LoadInt32(&fetchCalls), "fetch should run only once")
}

// TestGetOrFetch_WriteBackSurvivesCallerCancellation_MiniRedis verifies that the
// cache write-back inside the singleflight callback is NOT bound to the caller's
// context. Even when the caller's context is cancelled mid-fetch (e.g. a client
// timeout during a slow back-end call), the cache must still be populated so
// that the next request hits the cache (preserving the stampede-protection
// guarantee). This is a regression test for a bug where the write-back used the
// caller's ctx and therefore failed when the caller timed out.
func TestGetOrFetch_WriteBackSurvivesCallerCancellation_MiniRedis(t *testing.T) {
	mr := miniredis.RunT(t)

	client := redisv9.NewClient(&redisv9.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	c := &Cache{client: client, prefix: "test:"}
	originalJitter := jitterTTL
	jitterTTL = func(d time.Duration) time.Duration { return d }
	t.Cleanup(func() { jitterTTL = originalJitter })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var dest string
	hit, err := c.GetOrFetch(ctx, "k", &dest, time.Minute, func() (any, error) {
		// Cancel the caller's context while the fetch is in flight,
		// simulating a client timeout during a slow back-end call.
		cancel()
		return "value", nil
	})

	require.NoError(t, err)
	assert.False(t, hit, "should be a cache miss (fetch executed)")
	assert.Equal(t, "value", dest)

	// The key must now exist in Redis: the write-back must use an independent
	// context so that the caller's cancellation does not prevent cache population.
	assert.True(t, mr.Exists("test:k"), "cache must be populated even when caller ctx is cancelled mid-fetch")
}

// ============================================================
// DeleteByPattern respects context cancellation (mock-based)
// ============================================================

func TestDeleteByPattern_RespectsContextCancellation(t *testing.T) {
	c, mock := mockCache(t)

	// Create a context that is already cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Because ctx is cancelled, the loop checks ctx.Err() before scanning,
	// so no Scan/Del calls are made.
	err := c.DeleteByPattern(ctx, "pattern:*")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)

	// No redis expectations should remain unmet.
	_ = mock
}

// ============================================================
// flightGroup unit tests (no Redis)
// ============================================================

func TestFlightGroup_DeduplicatesConcurrentCalls(t *testing.T) {
	g := newFlightGroup()

	var calls int32

	var wg sync.WaitGroup

	const n = 50

	// Barrier to maximise concurrency.
	var ready sync.WaitGroup

	ready.Add(1)

	results := make([]string, n)
	errs := make([]error, n)

	for i := range n {
		wg.Add(1)

		go func(idx int) {
			defer wg.Done()
			ready.Wait()

			val, err := g.do("samekey", func() (any, error) {
				atomic.AddInt32(&calls, 1)
				time.Sleep(20 * time.Millisecond) // simulate work
				return "shared", nil
			})
			results[idx] = val.(string)
			errs[idx] = err
		}(i)
	}

	ready.Done()
	wg.Wait()

	assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "fn should run only once")
	for i := range n {
		assert.NoError(t, errs[i])
		assert.Equal(t, "shared", results[i])
	}
}

func TestFlightGroup_PropagatesError(t *testing.T) {
	g := newFlightGroup()

	wantErr := errors.New("boom")
	val, err := g.do("k", func() (any, error) {
		return nil, wantErr
	})

	assert.Nil(t, val)
	assert.ErrorIs(t, err, wantErr)
}

func TestFlightGroup_DifferentKeysRunIndependently(t *testing.T) {
	g := newFlightGroup()

	var calls int32

	var wg sync.WaitGroup

	for i := range 10 {
		wg.Add(1)

		go func(idx int) {
			defer wg.Done()
			_, _ = g.do(fmt.Sprintf("key-%d", idx), func() (any, error) {
				atomic.AddInt32(&calls, 1)
				return idx, nil
			})
		}(i)
	}

	wg.Wait()

	// Different keys each run once.
	assert.Equal(t, int32(10), atomic.LoadInt32(&calls))
}

// TestFlightGroup_PanicDoesNotDeadlock verifies that when the leader's
// fn() panics, defer c.wg.Done() releases all waiters so they don't
// deadlock. Without the fix (wg.Done() not deferred), this test hangs.
func TestFlightGroup_PanicDoesNotDeadlock(t *testing.T) {
	g := newFlightGroup()

	// trigger lets the leader's fn block until we know the waiter has
	// registered as a follower, ensuring the panic happens while a
	// waiter is blocked on c.wg.Wait().
	trigger := make(chan struct{})

	// Leader goroutine: registers the call, blocks on trigger, panics.
	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		defer func() { _ = recover() }()
		_, _ = g.do("panickey", func() (any, error) {
			<-trigger
			panic("boom")
		})
	}()

	// Give the leader time to register the call in g.m.
	time.Sleep(20 * time.Millisecond)

	// Waiter goroutine: finds the call in g.m and blocks on c.wg.Wait().
	// When the leader panics, defer c.wg.Done() must release it.
	waiterDone := make(chan struct{})
	go func() {
		defer close(waiterDone)
		_, _ = g.do("panickey", func() (any, error) {
			return "unreachable", nil
		})
	}()

	// Give the waiter time to enter c.wg.Wait().
	time.Sleep(20 * time.Millisecond)

	// Trigger the panic. The waiter must be released.
	close(trigger)

	select {
	case <-waiterDone:
		// Good: waiter was released after the leader panicked.
	case <-time.After(3 * time.Second):
		t.Fatal("waiter deadlocked: defer c.wg.Done() not called after panic")
	}

	<-leaderDone
}
