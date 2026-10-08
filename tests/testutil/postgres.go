package testutil

import (
	"testing"

	"github.com/kasuganosora/community/tests/pgtest"
	"gorm.io/gorm"
)

// PostgresTestURL returns the PostgreSQL DSN tests should use (see
// tests/pgtest), or "" when tests run on the in-memory sqlexec engine.
func PostgresTestURL() string { return pgtest.URL() }

// UsePostgres reports whether tests are configured to run on PostgreSQL.
func UsePostgres() bool { return pgtest.Enabled() }

// OpenPostgresTestDB returns a GORM handle on a fresh PostgreSQL schema that
// is dropped when the test ends.
func OpenPostgresTestDB(t testing.TB, cfg *gorm.Config) *gorm.DB { return pgtest.Open(t, cfg) }

func openPostgresTestDB(cfg *gorm.Config) (*gorm.DB, func(), error) { return pgtest.OpenNoCleanup(cfg) }
