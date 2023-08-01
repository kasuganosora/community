package redis

import "C"
import (
	"fmt"
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
		var host, port, password, db string
		ok := false
		if i == 0 {
			host = config.C.Redis0Host
			port = config.C.Redis0Port
			password = config.C.Redis0Password
			db = config.C.Redis0Db
		} else {
			host, ok = config.C.Other[fmt.Sprintf("REDIS_%d_HOST", i)]
			if !ok {
				break
			}
			port = config.C.Other[fmt.Sprintf("REDIS_%d_PORT", i)]
			password = config.C.Other[fmt.Sprintf("REDIS_%d_PASSWORD", i)]
			db = config.C.Other[fmt.Sprintf("REDIS_%d_DB", i)]
		}

		if host == "" {
			break
		}

		if port == "" {
			port = "6379"
		}

		if db == "" {
			db = "0"
		}

		redises = append(redises, redis.NewClient(&redis.Options{
			Addr:     fmt.Sprintf("%s:%s", host, port),
			Password: password,
			DB:       0,
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
	return redises[connNum[0]]

}

func GetRedises() []*redis.Client {
	return redises
}
