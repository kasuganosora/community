package database

import (
	"os"
	"strings"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var DB *gorm.DB

// isTestMode 用于测试时跳过真实数据库连接
var isTestMode = false

func init() {
	// 自动检测是否在测试模式
	isTestMode = detectTestMode()
}

// detectTestMode 检测是否在运行 go test
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
// 这个函数只能在 init() 之前调用才有效
func SetTestMode() {
	isTestMode = true
}

// SetTestDB 设置测试数据库（仅用于测试）
func SetTestDB(db *gorm.DB) {
	DB = db
	isTestMode = true
}

// ResetTestDB 重置测试数据库
func ResetTestDB() {
	DB = nil
	isTestMode = false
}

// IsTestMode 检查是否在测试模式
func IsTestMode() bool {
	return isTestMode
}

// InitGorm 初始化 Gorm
func InitGorm() {
	// 如果是测试模式，跳过初始化
	if isTestMode {
		return
	}

	dsnType := getDsnType()
	if dsnType == "" {
		log.Fatal("Config DSN is invalid")
	}

	var err error
	switch dsnType {
	case "mysql":
		err = initMysqlConnection()
	case "sqlite":
		err = initSqliteConnection()
	default:
		log.Fatal("Unsupported DSN type: %s", dsnType)
	}

	if err != nil {
		log.Fatal("Init database connection failed: %+v", err)
	}

}

func initMysqlConnection() (err error) {
	dsn := config.C.DbUsername + ":" + config.C.DbPassword + "@tcp(" + config.C.DbHost + ":" + config.C.DbPort + ")/" + config.C.DbDatabase
	dsn += "?charset=utf8mb4&parseTime=True&loc=Local"
	DB, err = gorm.Open(mysql.Open(dsn))
	if err != nil {
		return
	}
	if config.C.DevMode == "true" {
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
		sqlDB.Exec("PRAGMA journal_mode=WAL")
		sqlDB.Exec("PRAGMA busy_timeout=5000")
	}
	if config.C.DevMode == "true" {
		DB = DB.Debug()
	}
	return
}

func getDsnType() string {
	switch config.C.DbConnection {
	case "mysql":
		return "mysql"
	case "sqlite":
		return "sqlite"
	}
	return ""
}
