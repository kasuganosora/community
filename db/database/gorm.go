package database

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/log"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// dbMu protects concurrent access to DB, isTestMode and the sqlexec handles.
// GetDB/SetTestDB/ResetTestDB/InitGorm/CloseDB/PingDB/GetDBStats all acquire it.
var dbMu sync.RWMutex

// DB is the global GORM DB handle. Access it via GetDB() to be concurrency-safe.
var DB *gorm.DB

// isTestMode 用于测试时跳过真实数据库连接.
var isTestMode = false

func init() {
	// 自动检测是否在测试模式（仅在非测试模式下才检测，避免覆盖测试前显式设置的标记）
	if !isTestMode {
		isTestMode = detectTestMode()
	}
}

// detectTestMode 检测是否在运行 go test.
func detectTestMode() bool {
	// 检查命令行参数
	for _, arg := range os.Args {
		// go test 会传递 -test.* 参数
		if strings.HasPrefix(arg, "-test.") {
			return true
		}
	}
	// 检查环境变量
	if os.Getenv("GO_TEST_MODE") == "1" {
		return true
	}

	return false
}

// SetTestMode 设置测试模式（必须在包导入之前调用）
// 这个函数只能在 init() 之前调用才有效.
func SetTestMode() {
	dbMu.Lock()
	defer dbMu.Unlock()

	isTestMode = true
}

// SetTestDB 设置测试数据库（仅用于测试）.
func SetTestDB(db *gorm.DB) {
	dbMu.Lock()
	defer dbMu.Unlock()

	DB = db
	isTestMode = true
}

// ResetTestDB 重置测试数据库.
func ResetTestDB() {
	dbMu.Lock()
	defer dbMu.Unlock()

	DB = nil
	isTestMode = false
}

// IsTestMode 检查是否在测试模式.
func IsTestMode() bool {
	dbMu.RLock()
	defer dbMu.RUnlock()

	return isTestMode
}

// GetDB 返回全局 GORM DB 句柄（并发安全）.
func GetDB() *gorm.DB {
	dbMu.RLock()
	defer dbMu.RUnlock()

	return DB
}

// InitGorm 初始化 Gorm.
func InitGorm() {
	dbMu.Lock()
	// 初始化逻辑由 initGormLocked 执行，返回的 fatalMsg 非空时表示需要 Fatal。
	// Fatal 会调用 os.Exit，因此必须在 defer Unlock 之后再执行，
	// 避免 gocritic exitAfterDefer（defer 永不执行）。
	fatalMsg := initGormLocked()
	dbMu.Unlock()

	if fatalMsg != "" {
		log.Fatal(fatalMsg)
	}
}

// initGormLocked 执行实际的数据库初始化，调用方必须已持有 dbMu.
// 返回值非空时表示遇到致命配置错误，需要由调用方在解锁后 log.Fatal.
func initGormLocked() string {
	// 如果是测试模式，跳过初始化
	if isTestMode {
		return ""
	}

	// 如果已经初始化过（DB != nil），避免重复初始化导致连接泄漏
	if DB != nil {
		return ""
	}

	dsnType := getDsnType()
	if dsnType == "" {
		return "Config DSN is invalid"
	}

	var err error

	switch dsnType {
	case "mysql": //nolint:goconst
		err = initMysqlConnection()
	case "postgres":
		err = initPostgresConnection()
	case "sqlite": //nolint:goconst
		err = initSqliteConnection()
	case "xml": //nolint:goconst
		err = initSqlexecXmlConnection()
	default:
		return fmt.Sprintf("Unsupported DSN type: %s", dsnType)
	}

	if err != nil {
		return fmt.Sprintf("Init database connection failed: %+v", err)
	}

	return ""
}

// DBConfig 数据库连接池配置.
type DBConfig struct {
	MaxOpenConns    int           // 最大连接数
	MaxIdleConns    int           // 最大空闲连接数
	ConnMaxLifetime time.Duration // 连接最大生命周期
	ConnMaxIdleTime time.Duration // 空闲连接最大生命周期
}

// DefaultDBConfig 默认数据库配置.
func DefaultDBConfig() DBConfig {
	return DBConfig{
		MaxOpenConns:    100,
		MaxIdleConns:    10,
		ConnMaxLifetime: 30 * time.Minute,
		ConnMaxIdleTime: 10 * time.Minute,
	}
}

// LoadDBConfig 从环境变量加载配置.
func LoadDBConfig() DBConfig {
	cfg := DefaultDBConfig()

	if v := os.Getenv("DB_MAX_OPEN_CONNS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			cfg.MaxOpenConns = i
		}
	}

	if v := os.Getenv("DB_MAX_IDLE_CONNS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			cfg.MaxIdleConns = i
		}
	}

	if v := os.Getenv("DB_CONN_MAX_LIFETIME_MINUTES"); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			cfg.ConnMaxLifetime = time.Duration(i) * time.Minute
		}
	}

	if v := os.Getenv("DB_CONN_MAX_IDLE_TIME_MINUTES"); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			cfg.ConnMaxIdleTime = time.Duration(i) * time.Minute
		}
	}

	return cfg
}

func initMysqlConnection() (err error) {
	dsn := config.C.DbUsername + ":" + config.C.DbPassword + "@tcp(" + config.C.DbHost + ":" + config.C.DbPort + ")/" + config.C.DbDatabase
	// multiStatements=true enables executing multiple SQL statements in a single
	// Exec call, which is required by the migration scripts that batch DDL.
	// It is safe because we never interpolate user input into these statements.
	dsn += "?charset=utf8mb4&parseTime=True&loc=Local&multiStatements=true"

	DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: NewRedactingLogger(logger.Default)})
	if err != nil {
		return
	}

	// 配置连接池
	sqlDB, err := DB.DB()
	if err != nil {
		// gorm.Open succeeded but we can't get *sql.DB — close the DB to avoid leak
		_ = closeGormSilently(DB)
		DB = nil

		return err
	}

	cfg := LoadDBConfig()
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	log.Info("MySQL connection pool configured: max_open=%d, max_idle=%d, max_lifetime=%v, max_idle_time=%v",
		cfg.MaxOpenConns, cfg.MaxIdleConns, cfg.ConnMaxLifetime, cfg.ConnMaxIdleTime)

	if config.C.DevMode == "true" { //nolint:goconst
		DB = DB.Debug()
	}

	return
}

func initPostgresConnection() (err error) {
	dsn := postgresDSN()

	DB, err = gorm.Open(dialect.OpenPostgres(dsn), &gorm.Config{Logger: NewRedactingLogger(logger.Default)})
	if err != nil {
		return
	}

	if err = configureConnectionPool(DB, "PostgreSQL"); err != nil {
		return err
	}

	if err = dialect.EnsureExtensions(DB); err != nil {
		_ = closeGormSilently(DB)
		DB = nil

		return err
	}

	if config.C.DevMode == "true" {
		DB = DB.Debug()
	}

	return
}

// postgresDSN builds a pgx/libpq DSN. DB_DSN, when set, is used verbatim so
// operators can pass anything libpq accepts (keyword/value or a postgres://
// URL); otherwise the DSN is assembled from the DB_HOST/PORT/DATABASE/
// USERNAME/PASSWORD fields plus DB_SSLMODE (default "disable").
//
// DB_PORT defaults to MySQL's 3306, so an unset DB_PORT means 5432 here.
func postgresDSN() string {
	if dsn := strings.TrimSpace(config.C.DbDSN); dsn != "" {
		return dsn
	}

	host := config.C.DbHost
	if host == "" {
		host = "127.0.0.1"
	}

	port := config.C.DbPort
	if port == "" || !config.IsExplicitlyConfigured("DB_PORT") {
		port = "5432"
	}

	sslmode := config.C.DbSSLMode
	if sslmode == "" {
		sslmode = "disable"
	}

	parts := []string{
		"host=" + quoteDSNValue(host),
		"port=" + quoteDSNValue(port),
		"user=" + quoteDSNValue(config.C.DbUsername),
		"password=" + quoteDSNValue(config.C.DbPassword),
		"dbname=" + quoteDSNValue(config.C.DbDatabase),
		"sslmode=" + quoteDSNValue(sslmode),
	}

	// Match MySQL's loc=Local: the session time zone follows the process, so
	// NOW()/CURRENT_TIMESTAMP and timestamp scans agree with Go's time.Local.
	if tz := time.Local.String(); tz != "" && tz != "Local" {
		parts = append(parts, "TimeZone="+quoteDSNValue(tz))
	}

	return strings.Join(parts, " ")
}

// quoteDSNValue quotes a libpq keyword value when it contains spaces or
// characters that terminate a value.
func quoteDSNValue(v string) string {
	if v == "" || strings.ContainsAny(v, " '\\\t") {
		v = strings.ReplaceAll(v, `\`, `\\`)
		v = strings.ReplaceAll(v, `'`, `\'`)

		return "'" + v + "'"
	}

	return v
}

// configureConnectionPool applies the shared pool settings from LoadDBConfig.
// On failure the opened handle is closed so a half-initialized connection
// cannot leak.
func configureConnectionPool(gdb *gorm.DB, name string) error {
	sqlDB, err := gdb.DB()
	if err != nil {
		_ = closeGormSilently(gdb)
		DB = nil

		return err
	}

	cfg := LoadDBConfig()
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	log.Info("%s connection pool configured: max_open=%d, max_idle=%d, max_lifetime=%v, max_idle_time=%v",
		name, cfg.MaxOpenConns, cfg.MaxIdleConns, cfg.ConnMaxLifetime, cfg.ConnMaxIdleTime)

	return nil
}

func initSqliteConnection() (err error) {
	dbPath := config.C.DbHost
	if dbPath == "" {
		dbPath = "community.db"
	}

	DB, err = gorm.Open(dialect.OpenSQLite(dbPath), &gorm.Config{Logger: NewRedactingLogger(logger.Default)})
	if err != nil {
		return
	}

	// Enable WAL mode and busy timeout to prevent SQLITE_BUSY under concurrent writes
	if sqlDB, dbErr := DB.DB(); dbErr == nil {
		pragmas := []struct {
			name string
			sql  string
		}{
			{"journal_mode", "PRAGMA journal_mode=WAL"},
			{"busy_timeout", "PRAGMA busy_timeout=5000"},
			{"synchronous", "PRAGMA synchronous=NORMAL"},
			{"cache_size", "PRAGMA cache_size=-64000"}, // 64MB cache
			{"temp_store", "PRAGMA temp_store=MEMORY"},
		}

		for _, p := range pragmas {
			if _, execErr := sqlDB.Exec(p.sql); execErr != nil {
				log.Warn("SQLite PRAGMA %s failed (continuing): %v", p.name, execErr)
			}
		}

		// 配置连接池（SQLite 只支持一个写入连接，但可以多个读取连接）
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetConnMaxLifetime(time.Hour)
	} else {
		// Can't obtain *sql.DB — clean up to avoid leaking the opened GORM handle
		_ = closeGormSilently(DB)
		DB = nil

		return dbErr
	}

	if config.C.DevMode == "true" {
		DB = DB.Debug()
	}

	return
}

// closeGormSilently closes the underlying *sql.DB of a gorm.DB without
// propagating errors. It is used for best-effort cleanup on error paths.
func closeGormSilently(gdb *gorm.DB) error {
	if gdb == nil {
		return nil
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return err
	}

	return sqlDB.Close()
}

func getDsnType() string {
	switch config.C.DbConnection {
	case "mysql":
		return "mysql"
	case "postgres", "postgresql", "pgsql":
		return "postgres"
	case "sqlite":
		return "sqlite"
	case "xml":
		return "xml"
	}

	return ""
}

// GetDBStats 获取数据库连接池统计信息.
func GetDBStats() (map[string]any, error) {
	dbMu.RLock()
	db := DB
	dbMu.RUnlock()

	if db == nil {
		return nil, os.ErrInvalid
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	stats := sqlDB.Stats()

	return map[string]any{
		"open_connections":    stats.OpenConnections,
		"in_use":              stats.InUse,
		"idle":                stats.Idle,
		"wait_count":          stats.WaitCount,
		"wait_duration_ms":    stats.WaitDuration.Milliseconds(),
		"max_idle_closed":     stats.MaxIdleClosed,
		"max_lifetime_closed": stats.MaxLifetimeClosed,
	}, nil
}

// CloseDB 关闭数据库连接.
func CloseDB() error {
	dbMu.Lock()
	db := DB
	DB = nil // clear the global handle so callers stop using a closed DB
	dbMu.Unlock()

	if db == nil {
		return nil
	}

	sqlDB, err := db.DB()
	if err != nil {
		return err
	}

	return sqlDB.Close()
}

// PingDB 检查数据库连接.
func PingDB() error {
	dbMu.RLock()
	db := DB
	dbMu.RUnlock()

	if db == nil {
		return os.ErrInvalid
	}

	sqlDB, err := db.DB()
	if err != nil {
		return err
	}

	return sqlDB.Ping()
}
