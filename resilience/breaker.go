package resilience

import (
	"errors"
	"math"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"
)

// ErrCircuitOpen is returned when the SRE circuit breaker sheds a request.
var ErrCircuitOpen = errors.New("resilience: circuit breaker open")

// BreakerState represents the current breaker state.
type BreakerState int32

const (
	StateClosed BreakerState = iota
	StateOpen
)

// BreakerConfig configures a Google SRE adaptive circuit breaker
// (inspired by openBshi/library/net/netutil/breaker/sre_breaker.go).
type BreakerConfig struct {
	// K is the multiplier for accepted requests: drop probability activates
	// when total > K * success (e.g. K=2.0 tolerates up to 50% failures).
	K float64
	// Window is the total rolling statistics window duration.
	Window time.Duration
	// Buckets is the number of sub-buckets inside Window.
	Buckets int
	// MinRequests is the minimum request volume inside Window before tripping.
	MinRequests int64
}

func (c *BreakerConfig) normalize() {
	if c.K <= 1.0 {
		c.K = 2.0
	}
	if c.Window <= 0 {
		c.Window = 10 * time.Second
	}
	if c.Buckets <= 0 {
		c.Buckets = 10
	}
	if c.MinRequests <= 0 {
		c.MinRequests = 20
	}
}

type windowBucket struct {
	epoch   int64
	total   int64
	success int64
}

// SREBreaker implements Google SRE client-side probabilistic throttling.
type SREBreaker struct {
	cfg     BreakerConfig
	bucketD time.Duration
	state   atomic.Int32
	nowFn   func() time.Time

	mu      sync.Mutex
	buckets []windowBucket
}

// NewSREBreaker constructs a new SRE adaptive circuit breaker.
func NewSREBreaker(cfg BreakerConfig) *SREBreaker {
	cfg.normalize()
	bucketD := cfg.Window / time.Duration(cfg.Buckets)
	if bucketD <= 0 {
		bucketD = time.Second
	}
	return &SREBreaker{
		cfg:     cfg,
		bucketD: bucketD,
		nowFn:   time.Now,
		buckets: make([]windowBucket, cfg.Buckets),
	}
}

func (b *SREBreaker) currentEpoch(now time.Time) int64 {
	return now.UnixNano() / int64(b.bucketD)
}

func (b *SREBreaker) add(success bool) {
	now := b.nowFn()
	epoch := b.currentEpoch(now)
	idx := int(epoch % int64(len(b.buckets)))
	if idx < 0 {
		idx += len(b.buckets)
	}
	b.mu.Lock()
	slot := &b.buckets[idx]
	if slot.epoch != epoch {
		slot.epoch = epoch
		slot.total = 0
		slot.success = 0
	}
	slot.total++
	if success {
		slot.success++
	}
	b.mu.Unlock()
}

func (b *SREBreaker) summary() (success, total int64) {
	now := b.nowFn()
	epoch := b.currentEpoch(now)
	minEpoch := epoch - int64(len(b.buckets)) + 1
	b.mu.Lock()
	for i := range b.buckets {
		if b.buckets[i].epoch >= minEpoch && b.buckets[i].epoch <= epoch {
			total += b.buckets[i].total
			success += b.buckets[i].success
		}
	}
	b.mu.Unlock()
	return success, total
}

// Allow decides whether a request may proceed. If rejected, ErrCircuitOpen is returned.
func (b *SREBreaker) Allow() error {
	success, total := b.summary()
	accepts := b.cfg.K * float64(success)
	if total < b.cfg.MinRequests || float64(total) <= accepts {
		b.state.CompareAndSwap(int32(StateOpen), int32(StateClosed))
		return nil
	}
	b.state.CompareAndSwap(int32(StateClosed), int32(StateOpen))
	dropRatio := math.Max(0, (float64(total)-accepts)/float64(total+1))
	if dropRatio <= rand.Float64() {
		return nil
	}
	// Record locally shed request as failure so sustained outage keeps drop ratio high.
	b.add(false)
	return ErrCircuitOpen
}

// MarkSuccess records a successful downstream call.
func (b *SREBreaker) MarkSuccess() { b.add(true) }

// MarkFailed records a failed downstream call.
func (b *SREBreaker) MarkFailed() { b.add(false) }

// State returns the current breaker state.
func (b *SREBreaker) State() BreakerState {
	return BreakerState(b.state.Load())
}

// Do executes fn under circuit-breaker protection.
func (b *SREBreaker) Do(fn func() error) error {
	if err := b.Allow(); err != nil {
		return err
	}
	err := fn()
	if err != nil {
		b.MarkFailed()
		return err
	}
	b.MarkSuccess()
	return nil
}

// BreakerGroup manages a bounded set of named SREBreakers (e.g. per remote host).
type BreakerGroup struct {
	cfg      BreakerConfig
	maxKeys  int
	mu       sync.RWMutex
	breakers map[string]*SREBreaker
}

// NewBreakerGroup creates a group of keyed SRE breakers with bounded cardinality.
func NewBreakerGroup(cfg BreakerConfig, maxKeys int) *BreakerGroup {
	cfg.normalize()
	if maxKeys <= 0 {
		maxKeys = 4096
	}
	return &BreakerGroup{
		cfg:      cfg,
		maxKeys:  maxKeys,
		breakers: make(map[string]*SREBreaker),
	}
}

// Get returns the SREBreaker for key, creating it lazily if needed.
func (g *BreakerGroup) Get(key string) *SREBreaker {
	g.mu.RLock()
	b := g.breakers[key]
	g.mu.RUnlock()
	if b != nil {
		return b
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if b = g.breakers[key]; b != nil {
		return b
	}
	if len(g.breakers) >= g.maxKeys {
		// Evict an arbitrary closed entry (or reset map) to keep memory bounded.
		evicted := 0
		for k, existing := range g.breakers {
			if existing.State() == StateClosed {
				delete(g.breakers, k)
				evicted++
				if evicted >= g.maxKeys/8 {
					break
				}
			}
		}
		if len(g.breakers) >= g.maxKeys {
			clear(g.breakers)
		}
	}
	b = NewSREBreaker(g.cfg)
	g.breakers[key] = b
	return b
}

// Reset clears all tracked breakers (primarily for tests).
func (g *BreakerGroup) Reset() {
	g.mu.Lock()
	clear(g.breakers)
	g.mu.Unlock()
}
