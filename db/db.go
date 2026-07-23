package db

import (
	"github.com/kasuganosora/community/db/database"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func init() {
	database.InitGorm()
}

// GetDB 获取数据库连接.
func GetDB() *gorm.DB {
	return database.DB
}

func GetRedis(connNum ...int) *redis.Client {
	return cRedis.GetRedis(connNum...)
}
