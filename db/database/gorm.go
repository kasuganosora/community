package database

import (
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	_ "modernc.org/sqlite"
	"strings"
)

var DB *gorm.DB

// InitGorm 初始化 Gorm
func InitGorm() {
	dsn := config.C.DSN

	if dsn == "" {
		log.Fatal("Config DSN is empty")
	}

	dsnType := getDsnType()
	if dsnType == "" {
		log.Fatal("Config DSN is invalid")
	}

	var err error
	switch dsnType {
	case "mysql":
		err = initMysqlConnection()
	default:
		log.Fatal("Unsupported DSN type: %s", dsnType)
	}

	if err != nil {
		log.Fatal("Init database connection failed: %+v", err)
	}

}

func initMysqlConnection() (err error) {
	// 去除前缀 mysql://
	dsn := strings.SplitN(config.C.DSN, "://", 2)[1]

	// 判断dsn中是否包含?，如果包含则说明有参数，否则没有,如果没有则加上 ?charset=utf8mb4&parseTime=True&loc=Local
	if !strings.Contains(dsn, "?") {
		dsn += "?charset=utf8mb4&parseTime=True&loc=Local"
	}

	DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	return
}

func getDsnType() string {
	// mysql:// -> mysql
	// sqlite:// -> sqlite
	// postgres:// -> postgres
	// sqlserver:// -> mssql
	// mssql:// -> mssql
	// memory:// -> sqlite

	schema := strings.SplitN(config.C.DSN, "://", 2)[0]

	switch schema {
	case "mysql":
		return "mysql"
	case "sqlite":
		return "sqlite"
	case "postgres":
		return "postgres"
	case "sqlserver":
		return "mssql"
	case "mssql":
		return "mssql"
	case "memory":
		return "sqlite"
	default:
		return ""
	}
}
