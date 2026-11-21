package resilience

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSREBreakerTripsAndRecovers(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	b := NewSREBreaker(BreakerConfig{
		K:           2.0,
		Window:      5 * time.Second,
		Buckets:     5,
		MinRequests: 10,
	})
	b.nowFn = func() time.Time { return now }

	// Below MinRequests: always allowed even with failures.
	for range 9 {
		require.NoError(t, b.Allow())
		b.MarkFailed()
	}
	require.Equal(t, StateClosed, b.State())

	// Push total over MinRequests with 0% success => breaker opens and drops most calls.
	for range 15 {
		b.MarkFailed()
	}
	rejected := 0
	for range 50 {
		if errors.Is(b.Allow(), ErrCircuitOpen) {
			rejected++
		}
	}
	require.Greater(t, rejected, 30, "open breaker should probabilistically shed failing traffic")
	require.Equal(t, StateOpen, b.State())

	// Advance past window: stale failures expire and breaker closes automatically.
	now = now.Add(6 * time.Second)
	require.NoError(t, b.Allow())
	require.Equal(t, StateClosed, b.State())
}

func TestBreakerGroupBoundedCardinality(t *testing.T) {
	g := NewBreakerGroup(BreakerConfig{MinRequests: 5}, 16)
	for i := range 64 {
		b := g.Get(string(rune('a' + i)))
		require.NotNil(t, b)
	}
	g.mu.RLock()
	size := len(g.breakers)
	g.mu.RUnlock()
	require.LessOrEqual(t, size, 16)
}

func TestPipelineMergesByKeyAndFlushesOnClose(t *testing.T) {
	var total atomic.Int64
	var mu sync.Mutex
	collected := map[string][]int{}

	p := NewPipeline[int](PipelineConfig{
		MaxSize:  5,
		Interval: 50 * time.Millisecond,
		Buffer:   64,
		Workers:  4,
		Smooth:   true,
	}, func(_ context.Context, _ int, batch map[string][]int) {
		mu.Lock()
		for k, vs := range batch {
			collected[k] = append(collected[k], vs...)
			total.Add(int64(len(vs)))
		}
		mu.Unlock()
	})

	for i := range 20 {
		require.NoError(t, p.Add("acct:1", i))
		require.NoError(t, p.SyncAdd(context.Background(), "acct:2", i))
	}
	require.NoError(t, p.Close())
	require.Equal(t, ErrPipelineClosed, p.Add("acct:1", 99))
	require.Equal(t, int64(40), total.Load())
	require.Len(t, collected["acct:1"], 20)
	require.Len(t, collected["acct:2"], 20)
}

func TestVegasLimiterEnforcesAndAdjustsConcurrency(t *testing.T) {
	v := NewVegasLimiter(VegasConfig{
		MinLimit:     4,
		MaxLimit:     64,
		InitialLimit: 4,
		MinWindow:    5 * time.Millisecond,
		MaxWindow:    20 * time.Millisecond,
		MinSamples:   4,
	})

	dones := make([]func(time.Time, VegasOp), 0, 4)
	start := time.Now()
	for range 4 {
		done, ok := v.Acquire()
		require.True(t, ok)
		dones = append(dones, done)
	}
	// 5th concurrent request exceeds InitialLimit=4.
	done5, ok5 := v.Acquire()
	require.False(t, ok5)
	done5(start, VegasIgnore)

	for _, d := range dones {
		d(start, VegasSuccess)
	}
	require.Equal(t, int64(0), v.Stat().InFlight)

	// Concurrently exercise Acquire/Done under -race.
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				t0 := time.Now()
				done, ok := v.Acquire()
				if ok {
					done(t0, VegasSuccess)
				} else {
					done(t0, VegasDrop)
				}
			}
		}()
	}
	wg.Wait()
	st := v.Stat()
	require.Equal(t, int64(0), st.InFlight)
	require.GreaterOrEqual(t, st.Limit, int64(4))
	require.LessOrEqual(t, st.Limit, int64(64))
}

func TestPipelineSyncAddRaceWithClose(t *testing.T) {
	blockCh := make(chan struct{})
	p := NewPipeline[int](PipelineConfig{
		MaxSize:  1,
		Interval: time.Second,
		Buffer:   1,
		Workers:  1,
		Smooth:   true,
	}, func(_ context.Context, _ int, _ map[string][]int) {
		<-blockCh
	})

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(v int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			_ = p.SyncAdd(ctx, "same-shard", v)
		}(i)
	}
	time.Sleep(15 * time.Millisecond)
	close(blockCh)
	require.NoError(t, p.Close())
	wg.Wait()
}

func TestVegasGateShedDoesNotPoisonMinRTT(t *testing.T) {
	v := NewVegasLimiter(VegasConfig{
		MinLimit:     4,
		MaxLimit:     64,
		InitialLimit: 4,
		MinWindow:    time.Millisecond,
		MaxWindow:    10 * time.Millisecond,
		MinSamples:   4,
	})

	// Hold 4 slots and trigger 20 fast gate rejections (~ns).
	start := time.Now().Add(-5 * time.Millisecond)
	held := make([]func(time.Time, VegasOp), 0, 4)
	for range 4 {
		d, ok := v.Acquire()
		require.True(t, ok)
		held = append(held, d)
	}
	for range 20 {
		d, ok := v.Acquire()
		require.False(t, ok)
		d(time.Now(), VegasDrop)
	}
	for _, d := range held {
		d(start, VegasSuccess)
	}

	st := v.Stat()
	require.GreaterOrEqual(t, st.MinRTT, 4*time.Millisecond, "gate rejections must not poison MinRTT to sub-microseconds")
	require.Greater(t, st.Limit, int64(4), "low-queue healthy window should grow limit despite gate rejections")
}
