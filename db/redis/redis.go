package redis

import (
	"fmt"
	"strconv"

	"github.com/kasuganosora/community/config"
	"github.com/redis/go-redis/v9"
)

var redises []*redis.Client

func init() {
	redises = make([]*redis.Client, 0)

	// 从配置文件中读取 redis 配置
	// 配置格式为:
	// redis_%d_host=127.0.0.1
	// redis_%d_port=6379
	// redis_%d_password=
	// redis_%d_db=0
	// 第0个直接从 REDIS_0_HOST 取，其他从Orther中取
	for i := 0; ; i++ {
		var host, port, password, dbStr string

		var ok bool

		if i == 0 {
			host = config.C.Redis0Host
			port = config.C.Redis0Port
			password = config.C.Redis0Password
			dbStr = config.C.Redis0Db
		} else {
			host, ok = config.C.Other[fmt.Sprintf("REDIS_%d_HOST", i)]
			if !ok {
				break
			}

			port = config.C.Other[fmt.Sprintf("REDIS_%d_PORT", i)]
			password = config.C.Other[fmt.Sprintf("REDIS_%d_PASSWORD", i)]
			dbStr = config.C.Other[fmt.Sprintf("REDIS_%d_DB", i)]
		}

		if host == "" {
			break
		}

		if port == "" {
			port = "6379"
		}

		db := 0

		if dbStr != "" {
			if parsedDB, err := strconv.Atoi(dbStr); err == nil {
				db = parsedDB
			}
		}

		redises = append(redises, redis.NewClient(&redis.Options{
			Addr:     fmt.Sprintf("%s:%s", host, port),
			Password: password,
			DB:       db,
		}))
	}
}

func GetRedis(connNum ...int) *redis.Client {
	// 未配置任何 redis
	if len(redises) == 0 {
		panic("no redis config")
	}

	if len(connNum) == 0 {
		return redises[0]
	}

	idx := connNum[0]
	if idx < 0 || idx >= len(redises) {
		panic(fmt.Sprintf("redis connection index out of range: %d", idx))
	}

	return redises[idx]
}

func GetRedises() []*redis.Client {
	return redises
}
