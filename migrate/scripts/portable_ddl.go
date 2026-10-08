package scripts

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/log"
	"gorm.io/gorm"
)

// The index/column migration scripts were written with MariaDB-only syntax
// (CREATE INDEX IF NOT EXISTS, ADD COLUMN IF NOT EXISTS, ADD FULLTEXT INDEX IF
// NOT EXISTS). MySQL (including 8.4, the version used by docker-compose.yml)
// rejects all of these, and several statements reference tables that are not
// created by any migration (e.g. favourites, bookmarks, push_subscriptions).
// Either problem aborted migrate.Up() on a fresh install.
//
// applyPortableDDL runs the same statements in a dialect-neutral way: each
// statement is parsed, existence checks are done through GORM's Migrator,
// and statements whose target table/columns don't exist are skipped instead
// of failing the whole migration.

var (
	reCreateIndex = regexp.MustCompile(`(?i)^CREATE\s+INDEX\s+IF\s+NOT\s+EXISTS\s+(\w+)\s+ON\s+(\w+)\s*\(([^)]*)\)$`)
	reAddColumn   = regexp.MustCompile(`(?i)^ALTER\s+TABLE\s+(\w+)\s+ADD\s+COLUMN\s+IF\s+NOT\s+EXISTS\s+(\w+)\s+(.+)$`)
	reAddFulltext = regexp.MustCompile(`(?i)^ALTER\s+TABLE\s+(\w+)\s+ADD\s+FULLTEXT\s+INDEX\s+IF\s+NOT\s+EXISTS\s+(\w+)\s*\(([^)]*)\)$`)
)

// splitSQLStatements splits a multi-statement SQL string on ';', dropping
// "--" comments and blank statements.
func splitSQLStatements(sql string) []string {
	var cleaned strings.Builder

	for _, line := range strings.Split(sql, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}

		cleaned.WriteString(line)
		cleaned.WriteString("\n")
	}

	var stmts []string

	for _, s := range strings.Split(cleaned.String(), ";") {
		s = strings.Join(strings.Fields(s), " ")
		if s != "" {
			stmts = append(stmts, s)
		}
	}

	return stmts
}

// indexColumnNames extracts bare column names from an index column list such
// as "account_id, created_at DESC".
func indexColumnNames(list string) []string {
	var cols []string

	for _, part := range strings.Split(list, ",") {
		fields := strings.Fields(part)
		if len(fields) > 0 {
			cols = append(cols, strings.Trim(fields[0], "`"))
		}
	}

	return cols
}

func tableHasColumns(m gorm.Migrator, table string, cols []string) (bool, string) {
	if !m.HasTable(table) {
		return false, "table does not exist"
	}

	for _, col := range cols {
		if !m.HasColumn(table, col) {
			return false, "column " + col + " does not exist"
		}
	}

	return true, ""
}

// portableColumnDef translates the leading MySQL column type of a column
// definition (e.g. "DOUBLE DEFAULT 0") for PostgreSQL.
func portableColumnDef(db *gorm.DB, def string) string {
	if !dialect.IsPostgres(db) {
		return def
	}

	typ, rest, _ := strings.Cut(def, " ")
	if pg, ok := dialect.PostgresType(typ); ok {
		return strings.TrimSpace(pg + " " + rest)
	}

	return def
}

// applyPortableDDL executes the given index/column DDL statements in a way
// that works on both MySQL and MariaDB (see comment above).
func applyPortableDDL(db *gorm.DB, sql string) error {
	m := db.Migrator()

	for _, stmt := range splitSQLStatements(sql) {
		switch {
		case reCreateIndex.MatchString(stmt):
			g := reCreateIndex.FindStringSubmatch(stmt)
			name, table, colList := g[1], g[2], g[3]

			if ok, why := tableHasColumns(m, table, indexColumnNames(colList)); !ok {
				log.Debug("Skip index %s on %s: %s", name, table, why)

				continue
			}

			if m.HasIndex(table, name) {
				continue
			}

			ddl := fmt.Sprintf("CREATE INDEX %s ON %s (%s)", name, table, colList)
			if err := db.Exec(ddl).Error; err != nil {
				return fmt.Errorf("%s: %w", ddl, err)
			}

		case reAddFulltext.MatchString(stmt):
			g := reAddFulltext.FindStringSubmatch(stmt)
			table, name, colList := g[1], g[2], g[3]

			if ok, why := tableHasColumns(m, table, indexColumnNames(colList)); !ok {
				log.Debug("Skip fulltext index %s on %s: %s", name, table, why)

				continue
			}

			if m.HasIndex(table, name) {
				continue
			}

			// FULLTEXT is MySQL-only. Nothing queries it with MATCH ... AGAINST
			// (database search falls back to LIKE), so other engines skip it.
			if !dialect.IsMySQL(db) {
				log.Debug("Skip fulltext index %s on %s: not supported by %s", name, table, dialect.Name(db))

				continue
			}

			ddl := fmt.Sprintf("ALTER TABLE %s ADD FULLTEXT INDEX %s (%s)", table, name, colList)
			if err := db.Exec(ddl).Error; err != nil {
				return fmt.Errorf("%s: %w", ddl, err)
			}

		case reAddColumn.MatchString(stmt):
			g := reAddColumn.FindStringSubmatch(stmt)
			table, col, def := g[1], g[2], g[3]

			if !m.HasTable(table) {
				log.Debug("Skip column %s.%s: table does not exist", table, col)

				continue
			}

			if m.HasColumn(table, col) {
				continue
			}

			ddl := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, col, portableColumnDef(db, def))
			if err := db.Exec(ddl).Error; err != nil {
				return fmt.Errorf("%s: %w", ddl, err)
			}

		default:
			if err := db.Exec(stmt).Error; err != nil {
				return fmt.Errorf("%s: %w", stmt, err)
			}
		}
	}

	return nil
}
