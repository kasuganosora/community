package database

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
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
	defer dbMu.Unlock()

	// 如果是测试模式，跳过初始化
	if isTestMode {
		return
	}

	// 如果已经初始化过（DB != nil），避免重复初始化导致连接泄漏
	if DB != nil {
		return
	}

	dsnType := getDsnType()
	if dsnType == "" {
		log.Fatal("Config DSN is invalid")
	}

	var err error

	switch dsnType {
	case "mysql": //nolint:goconst
		err = initMysqlConnection()
	case "sqlite": //nolint:goconst
		err = initSqliteConnection()
	case "xml": //nolint:goconst
		err = initSqlexecXmlConnection()
	default:
		log.Fatal("Unsupported DSN type: %s", dsnType)
	}

	if err != nil {
		log.Fatal("Init database connection failed: %+v", err)
	}
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

	DB, err = gorm.Open(mysql.Open(dsn))
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

func initSqliteConnection() (err error) {
	dbPath := config.C.DbHost
	if dbPath == "" {
		dbPath = "community.db"
	}

	DB, err = gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
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
