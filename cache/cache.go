package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	redisv9 "github.com/redis/go-redis/v9"

	"github.com/kasuganosora/community/db/redis"
)

// Cache 缓存服务
type Cache struct {
	client *redisv9.Client
	prefix string
}

var (
	defaultCache *Cache
	once         sync.Once
)

// Init 初始化缓存
func Init() {
	once.Do(func() {
		defaultCache = &Cache{
			client: redis.GetRedis(),
			prefix: "community:",
		}
	})
}

// GetCache 获取默认缓存实例
func GetCache() *Cache {
	once.Do(func() {
		if defaultCache == nil {
			Init()
		}
	})
	return defaultCache
}

// Set 设置缓存
func (c *Cache) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, c.prefix+key, data, expiration).Err()
}

// Get 获取缓存
func (c *Cache) Get(ctx context.Context, key string, dest interface{}) error {
	data, err := c.client.Get(ctx, c.prefix+key).Bytes()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}

// GetString 获取字符串缓存
func (c *Cache) GetString(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, c.prefix+key).Result()
}

// SetString 设置字符串缓存
func (c *Cache) SetString(ctx context.Context, key string, value string, expiration time.Duration) error {
	return c.client.Set(ctx, c.prefix+key, value, expiration).Err()
}

// Delete 删除缓存
func (c *Cache) Delete(ctx context.Context, keys ...string) error {
	fullKeys := make([]string, len(keys))
	for i, k := range keys {
		fullKeys[i] = c.prefix + k
	}
	return c.client.Del(ctx, fullKeys...).Err()
}

// DeleteByPattern 按模式删除缓存 (使用SCAN替代KEYS避免阻塞)
func (c *Cache) DeleteByPattern(ctx context.Context, pattern string) error {
	var cursor uint64
	fullPattern := c.prefix + pattern

	for {
		var keys []string
		var err error
		keys, cursor, err = c.client.Scan(ctx, cursor, fullPattern, 100).Result()
		if err != nil {
			return err
		}

		if len(keys) > 0 {
			if err := c.client.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}

		if cursor == 0 {
			break
		}
	}

	return nil
}

// Exists 检查缓存是否存在
func (c *Cache) Exists(ctx context.Context, key string) (bool, error) {
	count, err := c.client.Exists(ctx, c.prefix+key).Result()
	return count > 0, err
}

// Expire 设置过期时间
func (c *Cache) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return c.client.Expire(ctx, c.prefix+key, expiration).Err()
}

// TTL 获取剩余过期时间
func (c *Cache) TTL(ctx context.Context, key string) (time.Duration, error) {
	return c.client.TTL(ctx, c.prefix+key).Result()
}

// Increment 自增
func (c *Cache) Increment(ctx context.Context, key string) (int64, error) {
	return c.client.Incr(ctx, c.prefix+key).Result()
}

// IncrementBy 指定增量
func (c *Cache) IncrementBy(ctx context.Context, key string, value int64) (int64, error) {
	return c.client.IncrBy(ctx, c.prefix+key, value).Result()
}

// Decrement 自减
func (c *Cache) Decrement(ctx context.Context, key string) (int64, error) {
	return c.client.Decr(ctx, c.prefix+key).Result()
}

// HSet 设置哈希字段
func (c *Cache) HSet(ctx context.Context, key string, field string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.client.HSet(ctx, c.prefix+key, field, data).Err()
}

// HGet 获取哈希字段
func (c *Cache) HGet(ctx context.Context, key string, field string, dest interface{}) error {
	data, err := c.client.HGet(ctx, c.prefix+key, field).Bytes()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}

// HGetAll 获取所有哈希字段
func (c *Cache) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return c.client.HGetAll(ctx, c.prefix+key).Result()
}

// HDel 删除哈希字段
func (c *Cache) HDel(ctx context.Context, key string, fields ...string) error {
	return c.client.HDel(ctx, c.prefix+key, fields...).Err()
}

// LPush 列表左侧插入
func (c *Cache) LPush(ctx context.Context, key string, values ...interface{}) error {
	return c.client.LPush(ctx, c.prefix+key, values...).Err()
}

// RPush 列表右侧插入
func (c *Cache) RPush(ctx context.Context, key string, values ...interface{}) error {
	return c.client.RPush(ctx, c.prefix+key, values...).Err()
}

// LRange 获取列表范围
func (c *Cache) LRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return c.client.LRange(ctx, c.prefix+key, start, stop).Result()
}

// LLen 获取列表长度
func (c *Cache) LLen(ctx context.Context, key string) (int64, error) {
	return c.client.LLen(ctx, c.prefix+key).Result()
}

// SAdd 集合添加
func (c *Cache) SAdd(ctx context.Context, key string, members ...interface{}) error {
	return c.client.SAdd(ctx, c.prefix+key, members...).Err()
}

// SMembers 获取集合所有成员
func (c *Cache) SMembers(ctx context.Context, key string) ([]string, error) {
	return c.client.SMembers(ctx, c.prefix+key).Result()
}

// SIsMember 检查是否是集合成员
func (c *Cache) SIsMember(ctx context.Context, key string, member interface{}) (bool, error) {
	return c.client.SIsMember(ctx, c.prefix+key, member).Result()
}

// ZAdd 有序集合添加
func (c *Cache) ZAdd(ctx context.Context, key string, score float64, member string) error {
	return c.client.ZAdd(ctx, c.prefix+key, redisv9.Z{Score: score, Member: member}).Err()
}

// ZRange 获取有序集合范围
func (c *Cache) ZRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return c.client.ZRange(ctx, c.prefix+key, start, stop).Result()
}

// ZRangeByScore 按分数获取有序集合范围
func (c *Cache) ZRangeByScore(ctx context.Context, key string, min, max string, offset, count int64) ([]string, error) {
	return c.client.ZRangeByScore(ctx, c.prefix+key, &redisv9.ZRangeBy{
		Min:    min,
		Max:    max,
		Offset: offset,
		Count:  count,
	}).Result()
}

// ZRem 删除有序集合成员
func (c *Cache) ZRem(ctx context.Context, key string, members ...interface{}) error {
	return c.client.ZRem(ctx, c.prefix+key, members...).Err()
}

// 缓存键常量
const (
	KeyAccount        = "account:%d"
	KeyAccountByAcct  = "account:acct:%s"
	KeyStatus         = "status:%d"
	KeyTimeline       = "timeline:%d:%s"
	KeyNotifications  = "notifications:%d"
	KeyInstanceStats  = "instance:stats"
	KeyTrendingTags   = "trending:tags"
	KeyTrendingStatus = "trending:statuses"
	KeySearchResults  = "search:%s"
)

// 默认过期时间
const (
	DefaultExpiration  = 5 * time.Minute
	ShortExpiration    = 1 * time.Minute
	LongExpiration     = 30 * time.Minute
	VeryLongExpiration = 24 * time.Hour
)

// 缓存辅助函数

// CacheAccount 缓存账户
func CacheAccount(ctx context.Context, accountID uint64, account interface{}) error {
	key := fmt.Sprintf(KeyAccount, accountID)
	return GetCache().Set(ctx, key, account, LongExpiration)
}

// GetCachedAccount 获取缓存的账户
func GetCachedAccount(ctx context.Context, accountID uint64, dest interface{}) error {
	key := fmt.Sprintf(KeyAccount, accountID)
	return GetCache().Get(ctx, key, dest)
}

// InvalidateAccount 使账户缓存失效
func InvalidateAccount(ctx context.Context, accountID uint64) error {
	key := fmt.Sprintf(KeyAccount, accountID)
	return GetCache().Delete(ctx, key)
}

// CacheStatus 缓存嘟文
func CacheStatus(ctx context.Context, statusID uint64, status interface{}) error {
	key := fmt.Sprintf(KeyStatus, statusID)
	return GetCache().Set(ctx, key, status, LongExpiration)
}

// GetCachedStatus 获取缓存的嘟文
func GetCachedStatus(ctx context.Context, statusID uint64, dest interface{}) error {
	key := fmt.Sprintf(KeyStatus, statusID)
	return GetCache().Get(ctx, key, dest)
}

// InvalidateStatus 使嘟文缓存失效
func InvalidateStatus(ctx context.Context, statusID uint64) error {
	key := fmt.Sprintf(KeyStatus, statusID)
	return GetCache().Delete(ctx, key)
}
