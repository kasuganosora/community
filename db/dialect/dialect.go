// Package dialect hides the few SQL differences between the supported
// database engines (MySQL/MariaDB, PostgreSQL, SQLite and the sqlexec "xml"
// engine used in tests) behind small helpers, so call sites can stay
// dialect-neutral instead of branching on the driver name.
package dialect

import (
	"strings"

	"gorm.io/gorm"
)

// Engine names returned by Name.
const (
	MySQL    = "mysql"
	Postgres = "postgres"
	SQLite   = "sqlite"
)

// Name returns the normalized dialect name of conn ("mysql", "postgres",
// "sqlite", or the raw dialector name for anything else). A nil handle is
// reported as MySQL, the historical default.
func Name(conn *gorm.DB) string {
	if conn == nil || conn.Dialector == nil {
		return MySQL
	}

	switch name := strings.ToLower(conn.Dialector.Name()); name {
	case "postgres", "postgresql", "pgx":
		return Postgres
	case "sqlite", "sqlite3":
		return SQLite
	default:
		return name
	}
}

// IsPostgres reports whether conn talks to PostgreSQL.
func IsPostgres(conn *gorm.DB) bool { return Name(conn) == Postgres }

// IsMySQL reports whether conn talks to MySQL/MariaDB.
func IsMySQL(conn *gorm.DB) bool { return Name(conn) == MySQL }

// IsSQLite reports whether conn talks to SQLite.
func IsSQLite(conn *gorm.DB) bool { return Name(conn) == SQLite }

// Quote quotes a (possibly qualified) identifier for conn's dialect: `x` on
// MySQL, "x" on PostgreSQL/SQLite. Only use it for fixed identifiers, never
// for user input.
func Quote(conn *gorm.DB, ident string) string {
	if conn == nil || conn.Statement == nil {
		return "`" + ident + "`"
	}

	return conn.Statement.Quote(ident)
}

// ILike returns the operator for a case-insensitive LIKE. MySQL's default
// *_ci collations and SQLite's LIKE are already case-insensitive (ASCII);
// PostgreSQL needs ILIKE.
func ILike(conn *gorm.DB) string {
	if IsPostgres(conn) {
		return "ILIKE"
	}

	return "LIKE"
}

// LikeEscape returns an ESCAPE clause that makes backslash the LIKE escape
// character. MySQL-compatible engines (MySQL/MariaDB, sqlexec) treat a
// backslash in a string literal as an escape, so it is doubled there;
// PostgreSQL (standard_conforming_strings) and SQLite take it literally.
func LikeEscape(conn *gorm.DB) string {
	if IsPostgres(conn) || IsSQLite(conn) {
		return `ESCAPE '\'`
	}

	return `ESCAPE '\\'`
}

// JSONText returns an SQL expression extracting the top-level string member
// key of the JSON column col, comparable with a plain string parameter. key
// and col must be fixed identifiers, never user input.
func JSONText(conn *gorm.DB, col, key string) string {
	if IsPostgres(conn) {
		return col + "->>'" + key + "'"
	}

	return "json_extract(" + col + ", '$." + key + "')"
}
