package resilience

import (
	"context"
	"errors"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"
)

// ErrPipelineFull is returned by Add when a shard's channel buffer is full.
var ErrPipelineFull = errors.New("resilience: pipeline buffer full")

// ErrPipelineClosed is returned when adding to a closed Pipeline.
var ErrPipelineClosed = errors.New("resilience: pipeline closed")

// PipelineConfig configures a sharded micro-batching Pipeline
// (inspired by openBshi/library/sync/pipeline/pipeline.go).
type PipelineConfig struct {
	// MaxSize flushes a shard batch immediately once accumulated items reach MaxSize.
	MaxSize int
	// Interval flushes a shard batch periodically even if below MaxSize.
	Interval time.Duration
	// Buffer is the channel capacity per worker shard.
	Buffer int
	// Workers is the number of independent hash-sharded flush workers.
	Workers int
	// Smooth staggers per-shard flush timers to avoid synchronized downstream bursts.
	Smooth bool
}

func (c *PipelineConfig) normalize() {
	if c.MaxSize <= 0 {
		c.MaxSize = 256
	}
	if c.Interval <= 0 {
		c.Interval = 500 * time.Millisecond
	}
	if c.Buffer <= 0 {
		c.Buffer = 1024
	}
	if c.Workers <= 0 {
		c.Workers = 4
	}
}

type pipelineMsg[T any] struct {
	key   string
	value T
}

// Pipeline shards incoming items by key and merges them into micro-batches
// flushed by size threshold or time interval.
type Pipeline[T any] struct {
	cfg     PipelineConfig
	doFn    func(ctx context.Context, shard int, batch map[string][]T)
	splitFn func(key string) uint32
	chans   []chan pipelineMsg[T]
	done    chan struct{}
	wg      sync.WaitGroup
	sendWg  sync.WaitGroup
	closed  atomic.Bool
	closeMu sync.RWMutex
}

// NewPipeline creates and starts a sharded micro-batching Pipeline.
func NewPipeline[T any](cfg PipelineConfig, doFn func(ctx context.Context, shard int, batch map[string][]T)) *Pipeline[T] {
	if doFn == nil {
		panic("resilience: pipeline doFn is nil")
	}
	cfg.normalize()
	p := &Pipeline[T]{
		cfg:   cfg,
		doFn:  doFn,
		chans: make([]chan pipelineMsg[T], cfg.Workers),
		done:  make(chan struct{}),
		splitFn: func(key string) uint32 {
			h := fnv.New32a()
			_, _ = h.Write([]byte(key))
			return h.Sum32()
		},
	}
	for i := range p.chans {
		p.chans[i] = make(chan pipelineMsg[T], cfg.Buffer)
	}
	p.wg.Add(cfg.Workers)
	for i := range p.chans {
		go p.runShard(i, p.chans[i])
	}
	return p
}

func (p *Pipeline[T]) shardFor(key string) int {
	return int(p.splitFn(key) % uint32(p.cfg.Workers))
}

// Add enqueues an item asynchronously without blocking the caller.
func (p *Pipeline[T]) Add(key string, value T) error {
	p.closeMu.RLock()
	if p.closed.Load() {
		p.closeMu.RUnlock()
		return ErrPipelineClosed
	}
	ch := p.chans[p.shardFor(key)]
	select {
	case ch <- pipelineMsg[T]{key: key, value: value}:
		p.closeMu.RUnlock()
		return nil
	default:
		p.closeMu.RUnlock()
		return ErrPipelineFull
	}
}

// SyncAdd enqueues an item, waiting until buffer space is available, the
// pipeline closes, or ctx expires.
func (p *Pipeline[T]) SyncAdd(ctx context.Context, key string, value T) error {
	p.closeMu.RLock()
	if p.closed.Load() {
		p.closeMu.RUnlock()
		return ErrPipelineClosed
	}
	p.sendWg.Add(1)
	ch := p.chans[p.shardFor(key)]
	p.closeMu.RUnlock()
	defer p.sendWg.Done()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return ErrPipelineClosed
	case ch <- pipelineMsg[T]{key: key, value: value}:
		return nil
	}
}

// Close stops accepting new items and waits for all buffered batches to flush.
func (p *Pipeline[T]) Close() error {
	p.closeMu.Lock()
	if p.closed.Swap(true) {
		p.closeMu.Unlock()
		p.wg.Wait()
		return nil
	}
	close(p.done)
	p.closeMu.Unlock()

	p.sendWg.Wait()
	for _, ch := range p.chans {
		close(ch)
	}
	p.wg.Wait()
	return nil
}

func (p *Pipeline[T]) runShard(index int, ch <-chan pipelineMsg[T]) {
	defer p.wg.Done()
	interval := p.cfg.Interval

	var initDelay time.Duration
	if p.cfg.Smooth && p.cfg.Workers > 1 && index > 0 {
		step := interval / time.Duration(p.cfg.Workers)
		if step > 0 {
			initDelay = step * time.Duration(index)
		}
	}

	var (
		initTimer *time.Timer
		initCh    <-chan time.Time
		ticker    *time.Ticker
		tickCh    <-chan time.Time
	)
	if initDelay > 0 {
		initTimer = time.NewTimer(initDelay)
		initCh = initTimer.C
		defer initTimer.Stop()
	} else {
		ticker = time.NewTicker(interval)
		tickCh = ticker.C
	}
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()

	batch := make(map[string][]T)
	count := 0
	flush := func() {
		if count == 0 {
			return
		}
		out := batch
		batch = make(map[string][]T)
		count = 0
		p.doFn(context.Background(), index, out)
	}

	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				flush()
				return
			}
			batch[msg.key] = append(batch[msg.key], msg.value)
			count++
			if count >= p.cfg.MaxSize {
				flush()
			}
		case <-initCh:
			initCh = nil
			flush()
			ticker = time.NewTicker(interval)
			tickCh = ticker.C
		case <-tickCh:
			flush()
		}
	}
}
