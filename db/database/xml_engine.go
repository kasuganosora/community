package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/sqlexec/pkg/api"
	sqlexecgorm "github.com/kasuganosora/sqlexec/pkg/api/gorm"
	"github.com/kasuganosora/sqlexec/pkg/resource/domain"
	"github.com/kasuganosora/sqlexec/pkg/resource/memory"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

var (
	sqlexecDB      *api.DB
	sqlexecSession *api.Session
)

func initSqlexecXmlConnection() error {
	databaseDir := config.C.DbHost
	if databaseDir == "" {
		databaseDir = "./database"
	}

	dbName := config.C.DbDatabase
	if dbName == "" {
		dbName = "community"
	}

	var err error
	sqlexecDB, err = api.NewDB(&api.DBConfig{
		DatabaseDir: databaseDir,
	})
	if err != nil {
		return fmt.Errorf("failed to create sqlexec DB: %w", err)
	}

	// Register MVCC datasource
	memDS := memory.NewMVCCDataSource(&domain.DataSourceConfig{
		Type:     domain.DataSourceTypeMemory,
		Name:     dbName,
		Writable: true,
	})
	if err := memDS.Connect(context.Background()); err != nil {
		sqlexecDB.Close()
		return fmt.Errorf("failed to connect datasource: %w", err)
	}
	if err := sqlexecDB.RegisterDataSource(dbName, memDS); err != nil {
		sqlexecDB.Close()
		return fmt.Errorf("failed to register datasource: %w", err)
	}
	if err := sqlexecDB.SetDefaultDataSource(dbName); err != nil {
		sqlexecDB.Close()
		return fmt.Errorf("failed to set default datasource: %w", err)
	}

	sqlexecSession = sqlexecDB.Session()

	// USE database - restores tables from XML files if they exist
	if _, err = sqlexecSession.Execute("USE " + dbName); err != nil {
		log.Debug("USE %s: %v (may be normal on first run)", dbName, err)
	}

	// Create GORM with wrapped dialector for ENGINE=xml support
	innerDialector := sqlexecgorm.NewDialector(sqlexecSession)
	DB, err = gorm.Open(
		&xmlDialectorWrapper{Dialector: innerDialector, session: sqlexecSession},
		&gorm.Config{SkipDefaultTransaction: true},
	)
	if err != nil {
		sqlexecSession.Close()
		sqlexecDB.Close()
		return fmt.Errorf("failed to open GORM with sqlexec: %w", err)
	}

	if config.C.DevMode == "true" {
		DB = DB.Debug()
	}

	log.Info("Database connected: sqlexec XML (dir=%s, db=%s)", databaseDir, dbName)
	return nil
}

// xmlDialectorWrapper wraps sqlexec GORM dialector to add ENGINE=xml to table creation
type xmlDialectorWrapper struct {
	gorm.Dialector
	session *api.Session
}

func (d *xmlDialectorWrapper) Migrator(db *gorm.DB) gorm.Migrator {
	return &xmlMigratorWrapper{
		Migrator: d.Dialector.Migrator(db),
		session:  d.session,
		db:       db,
	}
}

// xmlMigratorWrapper intercepts migration operations for sqlexec XML compatibility.
// Strategy: generate full CREATE TABLE ... ENGINE=xml from GORM schemas directly,
// and skip ALTER TABLE operations (not supported by sqlexec).
type xmlMigratorWrapper struct {
	gorm.Migrator
	session *api.Session
	db      *gorm.DB
}

func (m *xmlMigratorWrapper) AutoMigrate(values ...interface{}) error {
	for _, value := range values {
		if !m.Migrator.HasTable(value) {
			if err := m.createXmlTable(value); err != nil {
				return err
			}
		}
		// Skip ALTER TABLE for existing tables — sqlexec doesn't support it
	}
	return nil
}

func (m *xmlMigratorWrapper) CreateTable(values ...interface{}) error {
	for _, value := range values {
		if m.Migrator.HasTable(value) {
			continue
		}
		if err := m.createXmlTable(value); err != nil {
			return err
		}
	}
	return nil
}

// AddColumn is a no-op — sqlexec doesn't support ALTER TABLE
func (m *xmlMigratorWrapper) AddColumn(dst interface{}, field string) error {
	tableName := m.resolveTableName(dst)
	log.Debug("AddColumn %s.%s skipped (sqlexec: ALTER TABLE not supported)", tableName, field)
	return nil
}

// DropColumn is a no-op — sqlexec doesn't support ALTER TABLE
func (m *xmlMigratorWrapper) DropColumn(dst interface{}, field string) error {
	return nil
}

// AlterColumn is a no-op — sqlexec doesn't support ALTER TABLE
func (m *xmlMigratorWrapper) AlterColumn(dst interface{}, field string) error {
	return nil
}

// createXmlTable generates full CREATE TABLE ... ENGINE=xml from a GORM model
func (m *xmlMigratorWrapper) createXmlTable(model interface{}) error {
	stmt := &gorm.Statement{DB: m.db}
	if err := stmt.Parse(model); err != nil {
		return fmt.Errorf("failed to parse model: %w", err)
	}
	s := stmt.Schema
	tableName := s.Table

	var cols []string
	var pks []string

	for _, field := range s.Fields {
		if field.DBName == "" || field.IgnoreMigration {
			continue
		}

		colDef := m.buildColumnDef(field)
		cols = append(cols, colDef)

		if field.PrimaryKey {
			pks = append(pks, field.DBName)
		}
	}

	if len(cols) == 0 {
		return fmt.Errorf("no columns for table %s", tableName)
	}

	sql := "CREATE TABLE IF NOT EXISTS `" + tableName + "` (" + strings.Join(cols, ", ")
	if len(pks) > 0 {
		quotedPks := make([]string, len(pks))
		for i, pk := range pks {
			quotedPks[i] = "`" + pk + "`"
		}
		sql += ", PRIMARY KEY (" + strings.Join(quotedPks, ", ") + ")"
	}
	sql += ") ENGINE=xml"

	if _, err := m.session.Execute(sql); err != nil {
		return fmt.Errorf("create XML table %s failed: %w", tableName, err)
	}
	log.Debug("Created XML table: %s", tableName)
	return nil
}

// buildColumnDef generates a column definition like "`username` VARCHAR(64) NOT NULL"
func (m *xmlMigratorWrapper) buildColumnDef(f *schema.Field) string {
	sqlType := m.resolveColumnType(f)
	def := "`" + f.DBName + "` " + sqlType

	if f.AutoIncrement {
		def += " AUTO_INCREMENT"
	}
	if f.NotNull {
		def += " NOT NULL"
	}
	if f.HasDefaultValue && f.DefaultValue != "" {
		def += " DEFAULT " + m.quoteDefault(f.DefaultValue)
	}
	return def
}

// resolveColumnType maps GORM field to SQL type, preferring explicit tag types
func (m *xmlMigratorWrapper) resolveColumnType(f *schema.Field) string {
	// Prefer explicit type from GORM tag: gorm:"type:varchar(64)"
	if tagType, ok := f.TagSettings["TYPE"]; ok && tagType != "" {
		return strings.ToUpper(tagType)
	}

	// Use the dialector's DataTypeOf for proper type mapping
	sqlType := m.db.Config.Dialector.DataTypeOf(f)
	if sqlType != "" {
		return sqlType
	}

	// Fallback type mapping
	switch f.DataType {
	case schema.Bool:
		return "BOOLEAN"
	case schema.Int:
		if f.Size > 32 || f.AutoIncrement {
			return "BIGINT"
		}
		return "INT"
	case schema.Uint:
		if f.Size > 32 || f.AutoIncrement {
			return "BIGINT UNSIGNED"
		}
		return "INT UNSIGNED"
	case schema.Float:
		return "DOUBLE"
	case schema.String:
		if f.Size > 0 && f.Size <= 65535 {
			return fmt.Sprintf("VARCHAR(%d)", f.Size)
		}
		return "TEXT"
	case schema.Time:
		return "DATETIME"
	case schema.Bytes:
		return "BLOB"
	default:
		return "TEXT"
	}
}

func (m *xmlMigratorWrapper) quoteDefault(val string) string {
	// Numeric or boolean defaults don't need quoting
	lower := strings.ToLower(val)
	if lower == "true" || lower == "false" || lower == "null" {
		return val
	}
	// Try to detect numeric values
	for _, c := range val {
		if (c < '0' || c > '9') && c != '.' && c != '-' {
			return "'" + strings.ReplaceAll(val, "'", "''") + "'"
		}
	}
	return val
}

func (m *xmlMigratorWrapper) resolveTableName(model interface{}) string {
	stmt := &gorm.Statement{DB: m.db}
	if err := stmt.Parse(model); err == nil {
		return stmt.Table
	}
	return ""
}

// IsXmlMode returns true if using sqlexec XML persistence
func IsXmlMode() bool {
	return config.C.DbConnection == "xml"
}
