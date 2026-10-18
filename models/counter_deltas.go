package models

import (
	"context"
	"sync"

	"github.com/kasuganosora/community/db/writebuffer"
	"gorm.io/gorm"
)

// Counter columns (accounts_extra follower/following/status counts,
// statuses.replies_count) are hot rows: every inbox transaction that bumps
// one holds its row lock until COMMIT, so concurrent deliveries touching the
// same popular account or thread queue up behind each other. Federation
// transactions therefore collect their counter deltas and hand them to the
// write buffer after a successful commit, where they are summed and written
// in one async-commit batch. Counters are derived data (see
// RecountFollowCounters); a crash loses at most one flush interval of them.

type counterDeltasKey struct{}

type counterDelta struct {
	table, keyCol string
	id            uint64
	column        string
	delta         int64
}

type counterDeltas struct {
	mu     sync.Mutex
	list   []counterDelta
	closed bool
}

// BeginDeferredCounters scopes ctx so counter updates made by transactions
// using it are deferred; apply hands them to the write buffer and must be
// called only after the transaction committed (never on rollback).
func BeginDeferredCounters(ctx context.Context) (context.Context, func(conn *gorm.DB)) {
	d := &counterDeltas{}
	apply := func(conn *gorm.DB) {
		d.mu.Lock()
		list := d.list
		d.list, d.closed = nil, true
		d.mu.Unlock()
		for _, c := range list {
			_ = writebuffer.Increment(conn, c.table, c.keyCol, c.id, c.column, c.delta)
		}
	}
	return context.WithValue(ctx, counterDeltasKey{}, d), apply
}

// bumpCounter adds delta to table.column of the row keyCol = id, deferred
// when tx runs in a BeginDeferredCounters scope.
func bumpCounter(tx *gorm.DB, table, keyCol string, id uint64, column string, delta int64) error {
	if id == 0 || delta == 0 {
		return nil
	}
	if tx.Statement != nil && tx.Statement.Context != nil {
		if d, ok := tx.Statement.Context.Value(counterDeltasKey{}).(*counterDeltas); ok {
			d.mu.Lock()
			defer d.mu.Unlock()
			if !d.closed {
				d.list = append(d.list, counterDelta{table: table, keyCol: keyCol, id: id, column: column, delta: delta})
				return nil
			}
		}
	}
	return tx.Session(&gorm.Session{NewDB: true}).Table(table).Where(keyCol+" = ?", id).
		UpdateColumn(column, gorm.Expr(column+" + ?", delta)).Error
}

// BumpAccountCounter is bumpCounter for accounts_extra.
func BumpAccountCounter(tx *gorm.DB, accountID uint64, column string, delta int64) error {
	return bumpCounter(tx, "accounts_extra", "account_id", accountID, column, delta)
}
