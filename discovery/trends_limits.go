package discovery

import "context"

// Only two SQL snapshots run at once. Sixteen more requests may wait within
// their existing deadline; no worker or detached waiting goroutine is created.
var tagScans = newTagScanLimiter(2, 16)

type tagScanLimiter struct {
	active   chan struct{}
	admitted chan struct{}
}

func newTagScanLimiter(concurrency, waiting int) *tagScanLimiter {
	return &tagScanLimiter{
		active:   make(chan struct{}, concurrency),
		admitted: make(chan struct{}, concurrency+waiting),
	}
}

func (limiter *tagScanLimiter) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case limiter.admitted <- struct{}{}:
	default:
		return nil, ErrTrendBusy
	}
	select {
	case limiter.active <- struct{}{}:
		// Cancellation may race with an available slot. Do not begin SQL
		// after that deadline merely because select chose the ready slot.
		if err := ctx.Err(); err != nil {
			<-limiter.active
			<-limiter.admitted
			return nil, err
		}
		return func() {
			<-limiter.active
			<-limiter.admitted
		}, nil
	case <-ctx.Done():
		<-limiter.admitted
		return nil, ctx.Err()
	}
}
