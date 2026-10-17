// Package writebuffer batches low-value federation row updates (delivery
// bookkeeping, blurhashes, ...) and flushes them together every few hundred
// milliseconds in one transaction with relaxed durability (see
// dialect.AsyncCommit). Updates to the same row are coalesced, and a flush
// skips columns that already hold their new value, so redundant writes cost
// nothing. Local user data must keep using normal synchronous writes.
//
// When no buffer is running (CLI tools, tests), Update writes immediately.
package writebuffer

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/log"
	"gorm.io/gorm"
)

// DefaultInterval is how long an update may wait before it is written.
const DefaultInterval = 250 * time.Millisecond

// maxPending forces an early flush so a burst cannot grow without bound.
const maxPending = 2000

type rowKey struct {
	table string
	id    any
}

type op struct {
	table  string
	id     any
	values map[string]any
	// guard is an extra WHERE clause (with args) the update must satisfy.
	guard     string
	guardArgs []any
}

// Buffer coalesces row updates and flushes them in batches.
type Buffer struct {
	conn     func() *gorm.DB
	interval time.Duration

	mu      sync.Mutex
	pending map[rowKey]*op
	order   []rowKey
	kick    chan struct{}
	stop    chan struct{}
	done    chan struct{}
}

// New returns a stopped buffer writing through conn.
func New(conn func() *gorm.DB, interval time.Duration) *Buffer {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Buffer{conn: conn, interval: interval, pending: map[rowKey]*op{}, kick: make(chan struct{}, 1)}
}

var (
	defaultMu  sync.RWMutex
	defaultBuf *Buffer
)

// Start runs b as the process-wide buffer used by Update.
func Start(b *Buffer) *Buffer {
	b.stop, b.done = make(chan struct{}), make(chan struct{})
	go b.loop()
	defaultMu.Lock()
	defaultBuf = b
	defaultMu.Unlock()
	return b
}

// Stop flushes what is pending and detaches the buffer; later Updates are
// written immediately again.
func (b *Buffer) Stop() {
	defaultMu.Lock()
	if defaultBuf == b {
		defaultBuf = nil
	}
	defaultMu.Unlock()
	if b.stop != nil {
		close(b.stop)
		<-b.done
	}
}

func (b *Buffer) loop() {
	defer close(b.done)
	t := time.NewTicker(b.interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
		case <-b.kick:
		case <-b.stop:
			b.flushLogged()
			return
		}
		b.flushLogged()
	}
}

func (b *Buffer) flushLogged() {
	if err := b.Flush(context.Background()); err != nil {
		log.Warn("writebuffer flush: %v", err)
	}
}

// Update queues `UPDATE table SET values WHERE id = id` on the running
// buffer, or runs it now (still skipping unchanged values) when none runs.
// guard, when set, is an additional WHERE condition with its args.
func Update(conn *gorm.DB, table string, id any, values map[string]any, guard string, guardArgs ...any) error {
	defaultMu.RLock()
	b := defaultBuf
	defaultMu.RUnlock()
	if b == nil {
		return apply(conn, &op{table: table, id: id, values: values, guard: guard, guardArgs: guardArgs})
	}
	b.Add(table, id, values, guard, guardArgs...)
	return nil
}

// Add queues an update; values for the same row are merged (last wins).
func (b *Buffer) Add(table string, id any, values map[string]any, guard string, guardArgs ...any) {
	k := rowKey{table: table, id: fmt.Sprint(id)}
	b.mu.Lock()
	if cur := b.pending[k]; cur != nil {
		for c, v := range values {
			cur.values[c] = v
		}
		cur.guard, cur.guardArgs = guard, guardArgs
	} else {
		vals := make(map[string]any, len(values))
		for c, v := range values {
			vals[c] = v
		}
		b.pending[k] = &op{table: table, id: id, values: vals, guard: guard, guardArgs: guardArgs}
		b.order = append(b.order, k)
	}
	full := len(b.pending) >= maxPending
	b.mu.Unlock()
	if full {
		select {
		case b.kick <- struct{}{}:
		default:
		}
	}
}

// Pending reports how many rows wait to be written.
func (b *Buffer) Pending() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}

// Flush writes everything pending in one transaction. On failure the batch
// is put back (without overwriting newer values) and retried next time.
func (b *Buffer) Flush(ctx context.Context) error {
	b.mu.Lock()
	if len(b.pending) == 0 {
		b.mu.Unlock()
		return nil
	}
	batch, order := b.pending, b.order
	b.pending, b.order = map[rowKey]*op{}, nil
	b.mu.Unlock()

	// A stable order (table, id) keeps concurrent flushers from deadlocking.
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].table != order[j].table {
			return order[i].table < order[j].table
		}
		return fmt.Sprint(order[i].id) < fmt.Sprint(order[j].id)
	})
	err := b.conn().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		dialect.AsyncCommit(tx)
		for _, k := range order {
			if err := apply(tx, batch[k]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.mu.Lock()
		for _, k := range order {
			if cur := b.pending[k]; cur != nil {
				for c, v := range batch[k].values {
					if _, newer := cur.values[c]; !newer {
						cur.values[c] = v
					}
				}
				continue
			}
			b.pending[k] = batch[k]
			b.order = append(b.order, k)
		}
		b.mu.Unlock()
	}
	return err
}

func apply(conn *gorm.DB, o *op) error {
	q := conn.Table(o.table).Where("id = ?", o.id)
	if o.guard != "" {
		q = q.Where(o.guard, o.guardArgs...)
	}
	return dialect.UpdateChanged(q, o.values, nil).Error
}
