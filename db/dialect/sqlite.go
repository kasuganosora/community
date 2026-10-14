package dialect

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/migrator"
	"gorm.io/gorm/schema"
)

// OpenSQLite preserves SQLite's integer rowid identity even when a shared
// model declares a MySQL bigint type. BIGINT PRIMARY KEY accepts NULL in SQLite
// and does not allocate an ID, breaking freshly registered accounts/follows.
func OpenSQLite(dsn string) gorm.Dialector {
	return SQLiteDialector{Dialector: sqlite.Dialector{DSN: dsn}}
}

type SQLiteDialector struct{ sqlite.Dialector }

func (d SQLiteDialector) DataTypeOf(field *schema.Field) string {
	if field.PrimaryKey && field.AutoIncrement && (field.GORMDataType == schema.Int || field.GORMDataType == schema.Uint) {
		copy := *field
		copy.DataType = copy.GORMDataType
		return d.Dialector.DataTypeOf(&copy)
	}
	return d.Dialector.DataTypeOf(field)
}

func (d SQLiteDialector) Migrator(conn *gorm.DB) gorm.Migrator {
	return sqlite.Migrator{Migrator: migrator.Migrator{Config: migrator.Config{DB: conn, Dialector: d, CreateIndexAfterCreateTable: true}}}
}
