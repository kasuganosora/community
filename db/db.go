package db

import (
	"github.com/kasuganosora/community/db/database"
	"gorm.io/gorm"
)

func init() {
	database.InitGorm()
}

// GetDB 获取数据库连接
func GetDB() *gorm.DB {
	return database.DB
}
