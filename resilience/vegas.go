package resilience

import (
	"math"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"
)

// VegasOp indicates the outcome of a request tracked by VegasLimiter.
type VegasOp int

const (
	VegasIgnore VegasOp = iota
	VegasSuccess
	VegasDrop
)

// VegasConfig configures the TCP-Vegas adaptive concurrency limiter
// (inspired by openBshi/library/rate/vegas/vegas.go).
type VegasConfig struct {
	MinLimit     int64
	MaxLimit     int64
	InitialLimit int64
	MinWindow    time.Duration
	MaxWindow    time.Duration
	MinSamples   int64
}

func (c *VegasConfig) normalize() {
	if c.MinLimit <= 0 {
		c.MinLimit = 16
	}
	if c.MaxLimit < c.MinLimit {
		c.MaxLimit = max(int64(2048), c.MinLimit)
	}
	if c.InitialLimit < c.MinLimit {
		c.InitialLimit = min(c.MaxLimit, max(c.MinLimit, 64))
	}
	if c.InitialLimit > c.MaxLimit {
		c.InitialLimit = c.MaxLimit
	}
	if c.MinWindow <= 0 {
		c.MinWindow = 500 * time.Millisecond
	}
	if c.MaxWindow < c.MinWindow {
		c.MaxWindow = max(2*time.Second, c.MinWindow)
	}
	if c.MinSamples <= 0 {
		c.MinSamples = 16
	}
}

// VegasStat captures a snapshot of the Vegas adaptive concurrency limiter.
type VegasStat struct {
	Limit    int64
	InFlight int64
	MinRTT   time.Duration
	LastRTT  time.Duration
}

type vegasSample struct {
	samples     atomic.Int64 // Total completed admitted calls (success + drop)
	rttCount    atomic.Int64 // Admitted calls that yielded valid success RTT
	totalRTT    atomic.Int64
	maxInFlight atomic.Int64
	dropped     atomic.Bool
}

func (s *vegasSample) add(rtt, inFlight int64, drop bool) {
	s.samples.Add(1)
	if drop {
		s.dropped.Store(true)
	} else if rtt > 0 {
		s.totalRTT.Add(rtt)
		s.rttCount.Add(1)
	}
	for {
		cur := s.maxInFlight.Load()
		if inFlight <= cur {
			break
		}
		if s.maxInFlight.CompareAndSwap(cur, inFlight) {
			break
		}
	}
}

func (s *vegasSample) avgRTT() int64 {
	c := s.rttCount.Load()
	if c == 0 {
		return 0
	}
	return s.totalRTT.Load() / c
}

// VegasLimiter dynamically adjusts concurrency limit based on observed RTT queueing.
type VegasLimiter struct {
	cfg        VegasConfig
	limit      atomic.Int64
	inFlight   atomic.Int64
	updateTime atomic.Int64
	minRTT     atomic.Int64
	sample     atomic.Pointer[vegasSample]

	mu     sync.Mutex
	probes int64
}

// NewVegasLimiter creates a TCP-Vegas adaptive concurrency limiter.
func NewVegasLimiter(cfg VegasConfig) *VegasLimiter {
	cfg.normalize()
	v := &VegasLimiter{
		cfg:    cfg,
		probes: 100,
	}
	v.limit.Store(cfg.InitialLimit)
	v.sample.Store(&vegasSample{})
	return v
}

// Stat returns the current adaptive limiter state.
func (v *VegasLimiter) Stat() VegasStat {
	return VegasStat{
		Limit:    v.limit.Load(),
		InFlight: v.inFlight.Load(),
		MinRTT:   time.Duration(v.minRTT.Load()),
		LastRTT:  time.Duration(v.sample.Load().avgRTT()),
	}
}

// Acquire reserves one in-flight slot. Caller MUST invoke done(start, op)
// regardless of whether allowed is true or false.
func (v *VegasLimiter) Acquire() (done func(start time.Time, op VegasOp), allowed bool) {
	inFlight := v.inFlight.Add(1)
	allowed = inFlight <= v.limit.Load()

	var once sync.Once
	return func(start time.Time, op VegasOp) {
		once.Do(func() {
			v.inFlight.Add(-1)
			// Gate-rejected calls never executed downstream and must not poison
			// server RTT baselines with sub-microsecond rejection times.
			if !allowed || op == VegasIgnore {
				return
			}
			end := time.Now().UnixNano()
			rtt := end - start.UnixNano()
			if rtt <= 0 {
				rtt = 1
			}
			s := v.sample.Load()
			s.add(rtt, inFlight, op == VegasDrop)
			if end > v.updateTime.Load() && s.samples.Load() >= v.cfg.MinSamples {
				v.adjust(s, end)
			}
		})
	}, allowed
}

func (v *VegasLimiter) adjust(s *vegasSample, end int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.sample.Load() != s {
		return
	}
	v.sample.Store(&vegasSample{})

	limit := v.limit.Load()
	threshold := math.Sqrt(float64(limit)) / 2.0
	lastRTT := s.avgRTT()

	// If every sample in the window was a downstream drop (5xx), back off even
	// without valid success RTTs and do not touch minRTT.
	if s.dropped.Load() && lastRTT <= 0 {
		v.updateTime.Store(end + int64(v.cfg.MinWindow))
		clamped := int64(math.Max(float64(v.cfg.MinLimit), float64(limit)-threshold))
		v.limit.Store(clamped)
		return
	}
	if lastRTT <= 0 {
		return
	}

	window := lastRTT * 5
	minW := int64(v.cfg.MinWindow)
	maxW := int64(v.cfg.MaxWindow)
	if window < minW {
		window = minW
	} else if window > maxW {
		window = maxW
	}
	v.updateTime.Store(end + window)

	minRTT := v.minRTT.Load()
	if !s.dropped.Load() {
		v.probes--
		if v.probes <= 0 {
			maxFlight := s.maxInFlight.Load()
			if maxFlight*2 < limit || maxFlight <= v.cfg.MinLimit {
				v.probes = 3*limit + rand.Int64N(max(int64(1), 3*limit))
				minRTT = lastRTT
				v.minRTT.Store(minRTT)
			}
		}
		if minRTT == 0 || lastRTT < minRTT {
			minRTT = lastRTT
			v.minRTT.Store(minRTT)
		}
	}

	var newLimit float64
	if s.dropped.Load() {
		newLimit = float64(limit) - threshold
	} else if s.maxInFlight.Load()*2 < limit || minRTT <= 0 {
		return
	} else {
		queue := float64(limit) * (1.0 - float64(minRTT)/float64(lastRTT))
		switch {
		case queue < threshold:
			newLimit = float64(limit) + 6*threshold
		case queue < 2*threshold:
			newLimit = float64(limit) + 3*threshold
		case queue < 3*threshold:
			newLimit = float64(limit) + threshold
		case queue > 6*threshold:
			newLimit = float64(limit) - threshold
		default:
			return
		}
	}
	clamped := int64(math.Max(float64(v.cfg.MinLimit), math.Min(float64(v.cfg.MaxLimit), newLimit)))
	v.limit.Store(clamped)
}
