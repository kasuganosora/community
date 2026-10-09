package migrate_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/matrix/homeserver"
	"github.com/kasuganosora/community/migrate"
	"github.com/kasuganosora/community/migrate/scripts"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

const modulePath = "github.com/kasuganosora/community/"

// freshMigratedDB runs the whole migration chain on an empty database:
// PostgreSQL when TEST_DATABASE_URL is set, otherwise a file-backed SQLite
// database (a real sqlite deployment, not the in-memory test engine).
func freshMigratedDB(t *testing.T) *gorm.DB {
	t.Helper()
	var conn *gorm.DB
	if testutil.UsePostgres() {
		conn = testutil.OpenPostgresTestDB(t, nil)
	} else {
		var err error
		conn, err = gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "fresh.db")), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := conn.DB()
		require.NoError(t, err)
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	// A few legacy scripts read the global handle.
	prev := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() {
		if prev != nil {
			database.SetTestDB(prev)
		} else {
			database.ResetTestDB()
		}
	})

	require.NoError(t, migrate.Up(conn))
	var n int64
	require.NoError(t, conn.Model(&migrate.Migration{}).Count(&n).Error)
	require.EqualValues(t, len(scripts.GetScriptsName()), n)
	require.NoError(t, migrate.Up(conn), "re-running the chain is a no-op")
	return conn
}

// missingSchema lists the tables and columns of models that conn lacks.
func missingSchema(t *testing.T, conn *gorm.DB, list []any) []string {
	t.Helper()
	cache := &sync.Map{}
	var missing []string
	for _, m := range list {
		s, err := schema.Parse(m, cache, conn.NamingStrategy)
		require.NoError(t, err)
		if !conn.Migrator().HasTable(s.Table) {
			missing = append(missing, fmt.Sprintf("table %s (%T)", s.Table, m))
			continue
		}
		cols, err := conn.Migrator().ColumnTypes(s.Table)
		require.NoError(t, err)
		have := map[string]bool{}
		for _, c := range cols {
			have[c.Name()] = true
		}
		for _, f := range s.Fields {
			if f.DBName != "" && !f.IgnoreMigration && !have[f.DBName] {
				missing = append(missing, fmt.Sprintf("column %s.%s (%T)", s.Table, f.DBName, m))
			}
		}
	}
	return missing
}

// TestFreshMigrationCreatesEveryModelTable runs the migration chain on an
// empty SQLite or PostgreSQL database and checks that every table and
// column a model expects exists (featured_tags, endorsements, ... were
// queried for years without any migration creating them).
func TestFreshMigrationCreatesEveryModelTable(t *testing.T) {
	conn := freshMigratedDB(t)
	assert.Empty(t, missingSchema(t, conn, appModels))

	for _, unique := range []struct{ table, index string }{
		{"featured_tags", "uidx_featured_tags_account_tag"},
		{"endorsements", "uidx_endorsements_account_target"},
		{"tags", "uidx_tags_name_key"},
		{"status_tags", "uidx_status_tags_status_tag"},
	} {
		assert.True(t, conn.Migrator().HasIndex(unique.table, unique.index), unique.index)
	}

	// The Matrix homeserver creates its own tables on start; that must work
	// on top of the migrated schema.
	require.NoError(t, conn.AutoMigrate(homeserver.Models()...))
	assert.Empty(t, missingSchema(t, conn, homeserver.Models()))
}

// TestEveryTableModelIsRegistered keeps appModels complete: every type in
// the module with a TableName method must be listed there, be owned by the
// Matrix homeserver, or be explicitly exempt.
func TestEveryTableModelIsRegistered(t *testing.T) {
	known := map[string]bool{}
	for _, list := range [][]any{appModels, homeserver.Models()} {
		for _, m := range list {
			typ := reflect.TypeOf(m).Elem()
			known[strings.TrimPrefix(typ.PkgPath(), modulePath)+"."+typ.Name()] = true
		}
	}

	root, err := filepath.Abs("..")
	require.NoError(t, err)
	var unregistered []string
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "frontend", "vendor", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, filepath.Dir(path))
		rel = filepath.ToSlash(rel)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "TableName" {
				continue
			}
			recv := fn.Recv.List[0].Type
			if star, ok := recv.(*ast.StarExpr); ok {
				recv = star.X
			}
			ident, ok := recv.(*ast.Ident)
			if !ok {
				continue
			}
			key := rel + "." + ident.Name
			if known[key] || unmigratedModels[key] != "" || unmigratedModels[rel+".*"] != "" {
				continue
			}
			unregistered = append(unregistered, key)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, unregistered, "add these models to appModels (and create their tables in a migration)")
}
