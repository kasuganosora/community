package dialect

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/migrator"
	"gorm.io/gorm/schema"
)

// OpenPostgres returns a GORM dialector for PostgreSQL (pgx) that also
// understands the MySQL column types used in model tags (tinyint, datetime,
// longtext, blob, json, int(11), ... see PostgresType). The models keep their
// MySQL tags so existing MySQL schemas are not altered by AutoMigrate.
func OpenPostgres(dsn string) gorm.Dialector {
	return &PostgresDialector{Dialector: postgres.Dialector{Config: &postgres.Config{DSN: dsn}}}
}

// PostgresDialector wraps the upstream postgres dialector and translates
// MySQL-specific column types in `gorm:"type:..."` tags.
type PostgresDialector struct {
	postgres.Dialector
}

// DataTypeOf maps explicit MySQL column types to PostgreSQL equivalents and
// otherwise defers to the upstream dialector.
func (d PostgresDialector) DataTypeOf(field *schema.Field) string {
	if field.Schema != nil && caseInsensitiveColumns[field.Schema.Table+"."+field.DBName] {
		return "citext"
	}

	switch field.DataType {
	case schema.Bool, schema.Int, schema.Uint, schema.Float, schema.String, schema.Time, schema.Bytes:
		// GORM's own kinds ("int", "float", ...) are not MySQL types.
		return d.Dialector.DataTypeOf(field)
	}

	translated, ok := PostgresType(string(field.DataType))
	if !ok {
		translated = string(field.DataType)
	}
	if isCharType(translated) && isIntegerField(field) {
		// A few legacy models store integer ids in varchar columns
		// (accounts_extra.account_id). MySQL compares them with integer
		// columns implicitly; PostgreSQL does not, so keep them integral.
		translated, ok = "bigint", true
	}
	if ok {
		saved := field.DataType
		field.DataType = schema.DataType(translated)
		defer func() { field.DataType = saved }()
	}

	return d.Dialector.DataTypeOf(field)
}

// caseInsensitiveColumns are identity columns that MySQL compares
// case-insensitively through its default *_ci collation (usernames, acct,
// instance domains, hashtags); lookups such as login, WebFinger, Matrix
// localparts and tag timelines rely on that. On PostgreSQL they use citext so
// =, IN, LIKE and unique indexes keep the same semantics.
var caseInsensitiveColumns = map[string]bool{
	"accounts.username": true,
	"accounts.acct":     true,
	"accounts.domain":   true,
	"users.username":    true,
	"tags.name":         true,
}

// EnsureExtensions installs the extensions the schema depends on (citext).
// citext is a trusted extension (PostgreSQL 13+), so the database owner can
// create it; otherwise a superuser must run CREATE EXTENSION citext once.
func EnsureExtensions(conn *gorm.DB) error {
	if err := conn.Exec("CREATE EXTENSION IF NOT EXISTS citext").Error; err != nil {
		return fmt.Errorf("create extension citext (run CREATE EXTENSION citext as a superuser): %w", err)
	}

	return nil
}

// Migrator returns the upstream postgres migrator bound to this dialector so
// that DataTypeOf above is used for CREATE/ALTER TABLE.
func (d PostgresDialector) Migrator(db *gorm.DB) gorm.Migrator {
	return postgresMigrator{postgres.Migrator{Migrator: migrator.Migrator{Config: migrator.Config{
		DB:                          db,
		Dialector:                   d,
		CreateIndexAfterCreateTable: true,
	}}}}
}

type postgresMigrator struct {
	postgres.Migrator
}

// ColumnTypes runs the upstream implementation with the plain postgres
// dialector: upstream GetRows type-asserts on it to send its probe query with
// the simple protocol, which keeps pgx from reusing a cached "SELECT *" plan
// after the table was altered earlier on the same connection ("cached plan
// must not change result type").
func (m postgresMigrator) ColumnTypes(value any) ([]gorm.ColumnType, error) {
	inner := m.Migrator
	if d, ok := m.Dialector.(PostgresDialector); ok {
		inner.Dialector = d.Dialector
	}

	return inner.ColumnTypes(value)
}

var (
	reMySQLIntType  = regexp.MustCompile(`^(tinyint|smallint|mediumint|int|integer|bigint)(\s*\(\s*\d+\s*\))?(\s+unsigned)?(\s+zerofill)?$`)
	reMySQLTimeType = regexp.MustCompile(`^(datetime|timestamp)(\s*\(\s*(\d)\s*\))?$`)
	reMySQLCharType = regexp.MustCompile(`^char\s*\(\s*(\d+)\s*\)$`)
	reMySQLBinType  = regexp.MustCompile(`^(var)?binary\s*\(\s*\d+\s*\)$`)
	reMySQLFloat    = regexp.MustCompile(`^(double|real)(\s+precision)?(\s*\(\s*\d+\s*,\s*\d+\s*\))?(\s+unsigned)?$`)
)

// PostgresType translates a MySQL column type to PostgreSQL. ok is false
// when the type needs no translation (or is not a recognized MySQL type).
func PostgresType(mysqlType string) (string, bool) {
	t := strings.ToLower(strings.TrimSpace(mysqlType))
	if t == "" {
		return "", false
	}

	switch t {
	case "tinytext", "mediumtext", "longtext":
		return "text", true
	case "tinyblob", "blob", "mediumblob", "longblob":
		return "bytea", true
	case "json":
		// jsonb, like MySQL's JSON, is a normalized binary representation
		// that supports equality, DISTINCT and indexing.
		return "jsonb", true
	case "float":
		return "real", true
	}

	if g := reMySQLIntType.FindStringSubmatch(t); g != nil {
		base := g[1]
		unsigned := g[3] != ""

		switch base {
		case "tinyint":
			// tinyint (and tinyint unsigned, 0-255) fits in smallint.
			return "smallint", true
		case "smallint":
			if unsigned {
				return "integer", true
			}

			return "smallint", true
		case "mediumint":
			return "integer", true
		case "int", "integer":
			if unsigned {
				return "bigint", true
			}

			return "integer", true
		default:
			// bigint unsigned has no PostgreSQL equivalent; bigint covers
			// every value the application stores (ids and int64-sized flags).
			return "bigint", true
		}
	}

	if g := reMySQLTimeType.FindStringSubmatch(t); g != nil {
		if g[3] != "" {
			return "timestamptz(" + g[3] + ")", true
		}

		return "timestamptz", true
	}

	// char(n) is reported as bpchar by PostgreSQL; spelling it that way lets
	// AutoMigrate recognize an unchanged column instead of re-altering it.
	if g := reMySQLCharType.FindStringSubmatch(t); g != nil {
		return "bpchar(" + g[1] + ")", true
	}

	if reMySQLBinType.MatchString(t) {
		return "bytea", true
	}

	if g := reMySQLFloat.FindStringSubmatch(t); g != nil {
		return "double precision", true
	}

	return "", false
}

func isCharType(t string) bool {
	t = strings.ToLower(t)

	return strings.HasPrefix(t, "varchar") || strings.HasPrefix(t, "bpchar") || strings.HasPrefix(t, "character")
}

func isIntegerField(field *schema.Field) bool {
	if field.FieldType == nil {
		return false
	}
	typ := field.FieldType
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	default:
		return false
	}
}
