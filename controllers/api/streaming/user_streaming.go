package streaming

import (
	"context"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"strings"
	"time"
)

func UserStreaming(c *gin.Context) {
	account := c.MustGet("account").(*models.Account)
	ws, err := upgrade.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	redisConn := db.GetRedis()
	channels := make([]string, 0)

	homeTimeline := &home.Home{Account: account}
	channels = append(channels, feed.GetRedisChannelName(homeTimeline))
	ctx := context.Background()
	sub := redisConn.Subscribe(ctx, channels...)

	// 创建订阅标记的key
	_, err = redisConn.Pipelined(ctx, func(pipe redis.Pipeliner) (err error) {
		for _, channel := range channels {
			pipe.Set(ctx, channel, "1", 0)
		}
		return
	})

	defer func() {
		_ = sub.Close()
		_, _ = redisConn.Del(ctx, channels...).Result()
	}()

	checkConnTicker := time.NewTicker(3 * time.Second)
	for {
		select {
		case msg := <-sub.Channel():
			var payload []byte
			switch {
			case strings.Contains(msg.Channel, "feed:home"):
				payload, err = serializerStatusEvent(account, msg.Payload)
			}
			if err != nil {
				err = errors.Wrap(err, fmt.Sprintf("failed to serialize message: %s", msg.Payload))
				log.ErrorV(err)
				continue
			}
			// 从redis中读取到消息
			err = ws.WriteMessage(websocket.TextMessage, payload)
		case <-c.Done():
			// 客户端关闭连接
			return
		case <-checkConnTicker.C:
			// 检查连接是否关闭
			err = ws.WriteMessage(websocket.PingMessage, nil)
			if err != nil {
				return
			}
		default:
			// 每次循环休息 100ms
			time.Sleep(100 * time.Millisecond)
		}
	}
}
