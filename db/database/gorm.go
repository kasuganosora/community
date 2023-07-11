package database

import (
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	_ "modernc.org/sqlite"
)

var DB *gorm.DB

// InitGorm 初始化 Gorm
func InitGorm() {

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
	dsn := config.C.DbUsername + ":" + config.C.DbPassword + "@tcp(" + config.C.DbHost + ":" + config.C.DbPort + ")/" + config.C.DbDatabase
	dsn += "?charset=utf8mb4&parseTime=True&loc=Local"
	DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if config.C.DevMode == "true" {
		DB = DB.Debug()
	}

	return
}

func getDsnType() string {
	if config.C.DbConnection == "mysql" {
		return "mysql"
	}

	return ""
}
