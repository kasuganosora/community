// Package pgtest lets database tests run on PostgreSQL instead of their
// default in-memory engine. It only depends on GORM and db/dialect so any
// package's internal tests can import it without cycles.
package pgtest

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/db/dialect"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Tests run on their default engine (in-memory sqlexec or SQLite). Setting
//
//	TEST_DATABASE_URL=postgres://user:pass@127.0.0.1:5432/community_test?sslmode=disable
//
// (or TEST_DB_CONNECTION=postgres plus TEST_DATABASE_URL in keyword/value
// form) runs them on PostgreSQL instead. Every test database is a fresh
// schema in that database, selected through search_path and dropped on
// cleanup, so parallel tests stay isolated.

var pgAdmin struct {
	once sync.Once
	db   *gorm.DB
	err  error
}

// URL returns the PostgreSQL DSN tests should use, or "" when
// tests run on the default in-memory engine.
func URL() string {
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		return ""
	}

	switch strings.ToLower(strings.TrimSpace(os.Getenv("TEST_DB_CONNECTION"))) {
	case "postgres", "postgresql", "pgsql":
		return dsn
	case "":
		if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
			return dsn
		}
	}

	return ""
}

// Enabled reports whether tests are configured to run on PostgreSQL.
func Enabled() bool { return URL() != "" }

func postgresAdmin() (*gorm.DB, error) {
	pgAdmin.once.Do(func() {
		pgAdmin.db, pgAdmin.err = gorm.Open(dialect.OpenPostgres(URL()), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		if pgAdmin.err == nil {
			if sqlDB, err := pgAdmin.db.DB(); err == nil {
				sqlDB.SetMaxOpenConns(4)
			}
			// Shared by every test schema through search_path (see withSearchPath).
			pgAdmin.err = pgAdmin.db.Exec("CREATE EXTENSION IF NOT EXISTS citext WITH SCHEMA public").Error
		}
	})

	return pgAdmin.db, pgAdmin.err
}

// withSearchPath adds search_path to a URL or keyword/value DSN. public stays
// on the path for extension types such as citext.
func withSearchPath(dsn, schema string) string {
	schema += ",public"

	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err == nil {
			q := u.Query()
			q.Set("search_path", schema)
			u.RawQuery = q.Encode()

			return u.String()
		}
	}

	return dsn + " search_path=" + schema
}

// Open creates a fresh schema in TEST_DATABASE_URL and returns
// a GORM handle whose search_path points at it. The schema is dropped when
// the test ends. cfg may be nil.
func Open(t testing.TB, cfg *gorm.Config) *gorm.DB {
	t.Helper()

	gdb, cleanup, err := OpenNoCleanup(cfg)
	if err != nil {
		t.Fatalf("postgres test database: %v", err)
	}

	t.Cleanup(cleanup)

	return gdb
}

// OpenShared returns a handle on the schema named schema, recreated empty.
// It is meant for packages that cache one database across tests; the schema
// is left in place after the run and replaced by the next one.
func OpenShared(schema string, cfg *gorm.Config) (*gorm.DB, error) {
	admin, err := postgresAdmin()
	if err != nil {
		return nil, err
	}

	if err := admin.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE").Error; err != nil {
		return nil, fmt.Errorf("drop schema: %w", err)
	}

	gdb, _, err := openSchema(admin, schema, cfg)

	return gdb, err
}

// OpenNoCleanup is Open for callers that manage cleanup themselves; call the
// returned function to close the handle and drop the schema.
func OpenNoCleanup(cfg *gorm.Config) (*gorm.DB, func(), error) {
	admin, err := postgresAdmin()
	if err != nil {
		return nil, nil, err
	}

	return openSchema(admin, "t_"+strings.ReplaceAll(uuid.NewString(), "-", "")[:16], cfg)
}

func openSchema(admin *gorm.DB, schema string, cfg *gorm.Config) (*gorm.DB, func(), error) {
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		return nil, nil, fmt.Errorf("create schema: %w", err)
	}

	if cfg == nil {
		cfg = &gorm.Config{}
	}

	if cfg.Logger == nil {
		cfg.Logger = logger.Default.LogMode(logger.Silent)
	}

	gdb, err := gorm.Open(dialect.OpenPostgres(withSearchPath(URL(), schema)), cfg)
	if err != nil {
		_ = admin.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE").Error

		return nil, nil, err
	}

	cleanup := func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}

		if os.Getenv("TEST_KEEP_SCHEMA") == "" {
			_ = admin.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE").Error
		}
	}

	return gdb, cleanup, nil
}

// GormOpen opens dialector with cfg, or a fresh PostgreSQL test schema when
// PostgreSQL tests are enabled. It is a drop-in for gorm.Open in test
// helpers that default to SQLite.
func GormOpen(t testing.TB, dialector gorm.Dialector, cfg *gorm.Config) (*gorm.DB, error) {
	t.Helper()

	if !Enabled() {
		if cfg == nil {
			cfg = &gorm.Config{}
		}

		return gorm.Open(dialector, cfg)
	}

	// SQLite does not enforce (or validate) foreign keys by default, so
	// SQLite-based tests migrate models in any order and insert rows without
	// their parents. Mirror that here; migrations keep their constraints.
	if cfg == nil {
		cfg = &gorm.Config{}
	}

	cfg.DisableForeignKeyConstraintWhenMigrating = true

	gdb, cleanup, err := OpenNoCleanup(cfg)
	if err != nil {
		return nil, err
	}

	t.Cleanup(cleanup)

	return gdb, nil
}
