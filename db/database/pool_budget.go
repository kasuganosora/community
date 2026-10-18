package database

import (
	"database/sql"
	"sync"

	"github.com/kasuganosora/community/log"
)

// Connection budget.
//
// DB_MAX_OPEN_CONNS is a per-process cap. The main pool (HTTP handlers, the
// write buffer, inbox workers, schedulers: everything that uses GetDB) gets
// the whole cap, minus whatever auxiliary pools the same process opens to the
// same database server (for example the Misskey source DB read by
// `tootctl passkeys import-misskey`, or the setup wizard's connection probes).
// ReserveAuxConnections shrinks the main pool while an auxiliary pool is open,
// so the process never holds more than the cap in total.

// minMainConns is the smallest main pool an auxiliary reservation may leave.
const minMainConns = 2

type poolBudget struct {
	mu       sync.Mutex
	cap      int
	cfg      DBConfig
	name     string
	main     *sql.DB
	reserved int
}

var budget = &poolBudget{}

// registerMainPool applies cfg to the main pool and logs the effective values.
func registerMainPool(sqlDB *sql.DB, name string, cfg DBConfig) {
	budget.mu.Lock()
	defer budget.mu.Unlock()

	budget.cap, budget.cfg, budget.name, budget.main = cfg.MaxOpenConns, cfg, name, sqlDB
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	budget.applyLocked()
}

// applyLocked sizes the main pool from the cap and current reservations.
func (b *poolBudget) applyLocked() {
	if b.main == nil {
		return
	}
	mainOpen := b.cap - b.reserved
	if mainOpen < minMainConns {
		mainOpen = minMainConns
	}
	idle := b.cfg.MaxIdleConns
	if idle > mainOpen {
		idle = mainOpen
	}
	b.main.SetMaxOpenConns(mainOpen)
	b.main.SetMaxIdleConns(idle)
	log.Info("%s connection pool: process cap=%d (DB_MAX_OPEN_CONNS), main max_open=%d, max_idle=%d, aux reserved=%d, max_lifetime=%v, max_idle_time=%v",
		b.name, b.cap, mainOpen, idle, b.reserved, b.cfg.ConnMaxLifetime, b.cfg.ConnMaxIdleTime)
}

// ReserveAuxConnections takes up to want connections out of the process cap
// for an auxiliary pool and returns how many were granted (at least 1) and a
// release func that gives them back to the main pool. Callers must size their
// pool with ConfigureAuxPool(granted).
func ReserveAuxConnections(purpose string, want int) (granted int, release func()) {
	if want < 1 {
		want = 1
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()

	granted = want
	if budget.main != nil {
		if room := budget.cap - budget.reserved - minMainConns; granted > room {
			granted = room
		}
		if granted < 1 {
			granted = 1
		}
	}
	budget.reserved += granted
	log.Info("Reserving %d database connection(s) for %s", granted, purpose)
	budget.applyLocked()

	var once sync.Once
	return granted, func() {
		once.Do(func() {
			budget.mu.Lock()
			defer budget.mu.Unlock()
			budget.reserved -= granted
			budget.applyLocked()
		})
	}
}

// ConfigureAuxPool limits an auxiliary pool to n connections.
func ConfigureAuxPool(sqlDB *sql.DB, n int) {
	sqlDB.SetMaxOpenConns(n)
	sqlDB.SetMaxIdleConns(n)
	cfg := LoadDBConfig()
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
}

// PoolStats reports the process cap, the main pool's max_open and the
// connections reserved for auxiliary pools.
func PoolStats() (processCap, mainMaxOpen, auxReserved int) {
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if budget.main != nil {
		mainMaxOpen = budget.main.Stats().MaxOpenConnections
	}

	return budget.cap, mainMaxOpen, budget.reserved
}
