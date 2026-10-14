package discovery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTagScanLimiterQueuesBurstWithoutIncreasingConcurrency(t *testing.T) {
	limiter := newTagScanLimiter(2, 16)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	first, err := limiter.acquire(ctx)
	require.NoError(t, err)
	second, err := limiter.acquire(ctx)
	require.NoError(t, err)
	entered := make(chan struct{}, 8)
	complete := make(chan error, 8)
	finish := make(chan struct{})
	for range 8 {
		go func() {
			release, acquireErr := limiter.acquire(ctx)
			if acquireErr != nil {
				complete <- acquireErr
				return
			}
			entered <- struct{}{}
			select {
			case <-finish:
			case <-ctx.Done():
			}
			release()
			complete <- nil
		}()
	}
	require.Eventually(t, func() bool { return len(limiter.admitted) == 10 }, 500*time.Millisecond, time.Millisecond)
	require.Empty(t, entered)
	first()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("queued scan did not start when capacity was released")
	}
	require.Len(t, limiter.active, 2)
	require.Empty(t, entered, "a second queued scan must not exceed two active snapshots")
	second()
	close(finish)
	for range 8 {
		require.NoError(t, <-complete)
	}
	require.Empty(t, limiter.active)
	require.Empty(t, limiter.admitted)
}

func TestTagScanLimiterBoundedQueueAndCancellation(t *testing.T) {
	limiter := newTagScanLimiter(1, 1)
	release, err := limiter.acquire(t.Context())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan error, 1)
	go func() {
		_, acquireErr := limiter.acquire(ctx)
		finished <- acquireErr
	}()
	require.Eventually(t, func() bool { return len(limiter.admitted) == 2 }, time.Second, time.Millisecond)
	_, err = limiter.acquire(t.Context())
	require.ErrorIs(t, err, ErrTrendBudget, "only a full bounded queue is rejected immediately")
	cancel()
	require.ErrorIs(t, <-finished, context.Canceled)
	require.Len(t, limiter.admitted, 1, "canceled waiters must release admission capacity")
	release()
	release, err = limiter.acquire(t.Context())
	require.NoError(t, err)
	release()
	require.Empty(t, limiter.admitted)
}

func TestTagScanLimiterWaitingUsesCallerDeadline(t *testing.T) {
	limiter := newTagScanLimiter(1, 1)
	release, err := limiter.acquire(t.Context())
	require.NoError(t, err)
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err = limiter.acquire(ctx)
	require.True(t, errors.Is(err, context.DeadlineExceeded))
	require.Len(t, limiter.admitted, 1)
	// An already canceled call must not consume an available admission slot.
	_, err = limiter.acquire(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Len(t, limiter.admitted, 1)
}
