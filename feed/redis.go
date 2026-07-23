package feed

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// RedisClient 定义 feed 模块需要的 Redis 操作接口.
type RedisClient interface {
	ZAdd(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd
	ZRem(ctx context.Context, key string, members ...any) *redis.IntCmd
	ZScore(ctx context.Context, key, member string) *redis.FloatCmd
	ZRevRank(ctx context.Context, key string, member string) *redis.IntCmd
	ZRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd
	ZRangeByScore(ctx context.Context, key string, opt *redis.ZRangeBy) *redis.StringSliceCmd
	ZRangeWithScores(ctx context.Context, key string, start, stop int64) *redis.ZSliceCmd
	ZRevRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd
	ZRemRangeByRank(ctx context.Context, key string, start, stop int64) *redis.IntCmd
	ZAddNX(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd
	SAdd(ctx context.Context, key string, members ...any) *redis.IntCmd
	SRem(ctx context.Context, key string, members ...any) *redis.IntCmd
	SMembers(ctx context.Context, key string) *redis.StringSliceCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	Publish(ctx context.Context, channel string, message any) *redis.IntCmd
	PubSubChannels(ctx context.Context, pattern string) *redis.StringSliceCmd
	Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error)
	Keys(ctx context.Context, pattern string) *redis.StringSliceCmd
	Unlink(ctx context.Context, keys ...string) *redis.IntCmd
	ZRevRangeByScoreWithScores(ctx context.Context, key string, opt *redis.ZRangeBy) *redis.ZSliceCmd
	ZRemRangeByScore(ctx context.Context, key string, min, max string) *redis.IntCmd
	// Scan 非阻塞迭代键（推荐用于生产环境，替代 Keys）
	Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd
}

// redisClientWrapper 包装 redis.Client 以实现 RedisClient 接口.
type redisClientWrapper struct {
	*redis.Client
}

func NewRedisClient(client *redis.Client) RedisClient {
	return &redisClientWrapper{Client: client}
}

// 确保 redisClientWrapper 实现了 RedisClient 接口.
var _ RedisClient = (*redisClientWrapper)(nil)
