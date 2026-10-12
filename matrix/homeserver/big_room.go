package homeserver

import (
	"context"
	"time"

	"github.com/kasuganosora/community/log"
)

// Big-room read limits. A room joined through a partial-state send_join stores
// tens of thousands of state events whose history visibility cannot be proven
// (no stored prev events and no state snapshot). Visibility is decided event by
// event, so a request that walks the timeline looking for N visible events
// would examine every stored event of the room. These limits bound that walk.
//
//	MATRIX_HISTORY_SCAN_MAX     candidate events examined per request (default 1500)
//	MATRIX_HISTORY_SCAN_BUDGET  wall-clock budget of that walk (default 8s)
const (
	defaultHistoryScanMax    = 1500
	defaultHistoryScanBudget = 8 * time.Second
)

type historyScan struct {
	started time.Time
	max     int
	budget  time.Duration
	scanned int
}

func newHistoryScan() *historyScan {
	return &historyScan{
		started: time.Now(),
		max:     envInt("MATRIX_HISTORY_SCAN_MAX", defaultHistoryScanMax, 16, 1_000_000),
		budget:  envDuration("MATRIX_HISTORY_SCAN_BUDGET", defaultHistoryScanBudget),
	}
}

func (h *historyScan) add(n int)              { h.scanned += n }
func (h *historyScan) elapsed() time.Duration { return time.Since(h.started) }
func (h *historyScan) exceeded() bool         { return h.scanned >= h.max || h.elapsed() >= h.budget }

// historyScanPage is the page size used after the first (limit-sized) page.
func historyScanPage(limit int) int { return min(max(limit, 64), 256) }

// logHistoryError records the real cause of a 500 on a read path. It never
// logs request headers or tokens, only identifiers.
func logHistoryError(endpoint, roomID, userID, phase string, err error) {
	log.Warn("matrix %s failed room=%s user=%s phase=%s: %v", endpoint, roomID, userID, phase, err)
}

// ---- CPU throttle for background partial-state work --------------------------
//
// Validating and storing the ~26k events of a huge room is CPU bound and ran at
// ~100% for a minute, starving request handling. Background work calls
// throttleTick in its loops; once the work since the last pause exceeds ~20 ms it
// sleeps long enough to keep the loop at MATRIX_JOIN_BG_CPU_DUTY (default 0.5,
// 1 disables) of one core. Only work whose context carries a throttle (the
// background resume path) is affected.

type cpuThrottle struct {
	duty float64
	last time.Time
}

type cpuThrottleKey struct{}

func throttleDuty() float64 {
	v := envInt("MATRIX_JOIN_BG_CPU_DUTY_PERCENT", 50, 5, 100)
	return float64(v) / 100
}

func withCPUThrottle(ctx context.Context) context.Context {
	duty := throttleDuty()
	if duty >= 1 {
		return ctx
	}
	return context.WithValue(ctx, cpuThrottleKey{}, &cpuThrottle{duty: duty, last: time.Now()})
}

// throttleTick pauses the caller when it has run too long without a break.
func throttleTick(ctx context.Context) {
	t, _ := ctx.Value(cpuThrottleKey{}).(*cpuThrottle)
	if t == nil {
		return
	}
	work := time.Since(t.last)
	if work < 20*time.Millisecond {
		return
	}
	pause := time.Duration(float64(work) * (1 - t.duty) / t.duty)
	if pause > 500*time.Millisecond {
		pause = 500 * time.Millisecond
	}
	select {
	case <-time.After(pause):
	case <-ctx.Done():
	}
	t.last = time.Now()
}
