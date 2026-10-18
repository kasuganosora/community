package database

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

func TestLoadDBConfigDefaults(t *testing.T) {
	for _, k := range []string{"DB_MAX_OPEN_CONNS", "DB_MAX_IDLE_CONNS", "DB_CONN_MAX_LIFETIME", "DB_CONN_MAX_LIFETIME_MINUTES", "DB_CONN_MAX_IDLE_TIME", "DB_CONN_MAX_IDLE_TIME_MINUTES"} {
		t.Setenv(k, "")
	}
	cfg := LoadDBConfig()
	if cfg.MaxOpenConns != 30 || cfg.MaxIdleConns != 10 || cfg.ConnMaxLifetime != 30*time.Minute || cfg.ConnMaxIdleTime != 10*time.Minute {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestLoadDBConfigFromEnv(t *testing.T) {
	t.Setenv("DB_MAX_OPEN_CONNS", "20")
	t.Setenv("DB_MAX_IDLE_CONNS", "50") // clamped to the cap
	t.Setenv("DB_CONN_MAX_LIFETIME", "1h")
	t.Setenv("DB_CONN_MAX_IDLE_TIME", "5") // bare minutes
	cfg := LoadDBConfig()
	if cfg.MaxOpenConns != 20 || cfg.MaxIdleConns != 20 || cfg.ConnMaxLifetime != time.Hour || cfg.ConnMaxIdleTime != 5*time.Minute {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadDBConfigLegacyAndInvalid(t *testing.T) {
	t.Setenv("DB_MAX_OPEN_CONNS", "zero")
	t.Setenv("DB_MAX_IDLE_CONNS", "-1")
	t.Setenv("DB_CONN_MAX_LIFETIME", "")
	t.Setenv("DB_CONN_MAX_LIFETIME_MINUTES", "45")
	t.Setenv("DB_CONN_MAX_IDLE_TIME", "soon")
	t.Setenv("DB_CONN_MAX_IDLE_TIME_MINUTES", "")
	cfg := LoadDBConfig()
	if cfg.MaxOpenConns != 30 || cfg.MaxIdleConns != 10 || cfg.ConnMaxLifetime != 45*time.Minute || cfg.ConnMaxIdleTime != 10*time.Minute {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestAuxReservationsStayWithinProcessCap(t *testing.T) {
	main, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer main.Close()
	saved := budget
	budget = &poolBudget{}
	t.Cleanup(func() { budget = saved })

	registerMainPool(main, "test", DBConfig{MaxOpenConns: 30, MaxIdleConns: 10, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute})
	if c, m, r := PoolStats(); c != 30 || m != 30 || r != 0 {
		t.Fatalf("stats = %d %d %d", c, m, r)
	}

	n, release := ReserveAuxConnections("misskey", 2)
	if c, m, r := PoolStats(); n != 2 || m+r != c || m != 28 {
		t.Fatalf("after reserve: granted=%d stats=%d %d %d", n, c, m, r)
	}
	release()
	release() // idempotent
	if _, m, r := PoolStats(); m != 30 || r != 0 {
		t.Fatalf("after release: %d %d", m, r)
	}

	// A huge request never starves the main pool below its floor.
	n, release = ReserveAuxConnections("greedy", 100)
	if _, m, _ := PoolStats(); n != 28 || m != minMainConns {
		t.Fatalf("greedy: granted=%d main=%d", n, m)
	}
	release()
}

func TestAuxReservationWithoutMainPool(t *testing.T) {
	saved := budget
	budget = &poolBudget{}
	t.Cleanup(func() { budget = saved })
	n, release := ReserveAuxConnections("wizard probe", 1)
	defer release()
	if n != 1 {
		t.Fatalf("granted = %d", n)
	}
}
