package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	redisv9 "github.com/redis/go-redis/v9"

	"github.com/kasuganosora/community/db/redis"
)

// ErrCacheMiss 缓存未命中（key 不存在）.
// 调用方可通过 errors.Is(err, cache.ErrCacheMiss) 判断是否为正常的缓存未命中.
var ErrCacheMiss = errors.New("cache: key not found")

// ErrEmptyKey 表示传入了空的缓存键.
var ErrEmptyKey = errors.New("cache: empty key")

// Cache 缓存服务.
type Cache struct {
	client *redisv9.Client
	prefix string

	// sf / sfOnce 用于 singleflight，防止缓存击穿（同一 key 并发回源只执行一次）.
	sf     *flightGroup
	sfOnce sync.Once
}

// getSF 惰性初始化 singleflight 组.
func (c *Cache) getSF() *flightGroup {
	c.sfOnce.Do(func() {
		c.sf = newFlightGroup()
	})

	return c.sf
}

var (
	defaultCache *Cache
	once         sync.Once
)

// Init 初始化缓存.
func Init() {
	once.Do(func() {
		defaultCache = &Cache{
			client: redis.GetRedis(),
			prefix: "community:",
		}
	})
}

// GetCache 获取默认缓存实例.
func GetCache() *Cache {
	Init()

	return defaultCache
}

// Close 释放缓存资源（复用全局 Redis 连接，无需实际关闭）.
func Close() {
	// cache 复用 db/redis 的全局连接，由 redis 包统一管理生命周期
}

// IsErrCacheMiss 判断错误是否为缓存未命中.
func IsErrCacheMiss(err error) bool {
	return errors.Is(err, ErrCacheMiss)
}

// IsErrEmptyKey 判断错误是否为空键错误.
func IsErrEmptyKey(err error) bool {
	return errors.Is(err, ErrEmptyKey)
}

// checkKey 校验缓存键非空，空键返回 ErrEmptyKey.
func checkKey(key string) error {
	if key == "" {
		return ErrEmptyKey
	}

	return nil
}

// Set 设置缓存.
// 若 expiration > 0，会附加少量随机抖动，避免大量 key 同时过期造成缓存雪崩.
func (c *Cache) Set(ctx context.Context, key string, value any, expiration time.Duration) error {
	if err := checkKey(key); err != nil {
		return err
	}

	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return c.client.Set(ctx, c.prefix+key, data, jitterTTL(expiration)).Err()
}

// Get 获取缓存.
// 缓存未命中（key 不存在）时返回 ErrCacheMiss，可通过 IsErrCacheMiss 判断.
func (c *Cache) Get(ctx context.Context, key string, dest any) error {
	if err := checkKey(key); err != nil {
		return err
	}

	data, err := c.client.Get(ctx, c.prefix+key).Bytes()
	if err != nil {
		return wrapRedisErr(err)
	}

	return json.Unmarshal(data, dest)
}

// GetString 获取字符串缓存.
// 缓存未命中时返回 ErrCacheMiss.
func (c *Cache) GetString(ctx context.Context, key string) (string, error) {
	if err := checkKey(key); err != nil {
		return "", err
	}

	val, err := c.client.Get(ctx, c.prefix+key).Result()
	if err != nil {
		return "", wrapRedisErr(err)
	}

	return val, nil
}

// SetString 设置字符串缓存.
// 若 expiration > 0，会附加随机抖动避免缓存雪崩.
func (c *Cache) SetString(ctx context.Context, key string, value string, expiration time.Duration) error {
	if err := checkKey(key); err != nil {
		return err
	}

	return c.client.Set(ctx, c.prefix+key, value, jitterTTL(expiration)).Err()
}

// Delete 删除缓存.
func (c *Cache) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}

	for _, k := range keys {
		if err := checkKey(k); err != nil {
			return err
		}
	}

	fullKeys := make([]string, len(keys))
	for i, k := range keys {
		fullKeys[i] = c.prefix + k
	}

	return c.client.Del(ctx, fullKeys...).Err()
}

// DeleteByPattern 按模式删除缓存 (使用SCAN替代KEYS避免阻塞).
func (c *Cache) DeleteByPattern(ctx context.Context, pattern string) error {
	if pattern == "" {
		return ErrEmptyKey
	}

	var cursor uint64

	fullPattern := c.prefix + pattern

	for {
		// 响应 context 取消，避免长时间阻塞
		if err := ctx.Err(); err != nil {
			return err
		}

		keys, nextCursor, err := c.client.Scan(ctx, cursor, fullPattern, 100).Result()
		if err != nil {
			return err
		}

		if len(keys) > 0 {
			if err := c.client.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return nil
}

// Exists 检查缓存是否存在.
func (c *Cache) Exists(ctx context.Context, key string) (bool, error) {
	if err := checkKey(key); err != nil {
		return false, err
	}

	count, err := c.client.Exists(ctx, c.prefix+key).Result()

	return count > 0, err
}

// Expire 设置过期时间.
func (c *Cache) Expire(ctx context.Context, key string, expiration time.Duration) error {
	if err := checkKey(key); err != nil {
		return err
	}

	return c.client.Expire(ctx, c.prefix+key, expiration).Err()
}

// TTL 获取剩余过期时间.
func (c *Cache) TTL(ctx context.Context, key string) (time.Duration, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}

	return c.client.TTL(ctx, c.prefix+key).Result()
}

// Increment 自增.
func (c *Cache) Increment(ctx context.Context, key string) (int64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}

	return c.client.Incr(ctx, c.prefix+key).Result()
}

// IncrementBy 指定增量.
func (c *Cache) IncrementBy(ctx context.Context, key string, value int64) (int64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}

	return c.client.IncrBy(ctx, c.prefix+key, value).Result()
}

// Decrement 自减.
func (c *Cache) Decrement(ctx context.Context, key string) (int64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}

	return c.client.Decr(ctx, c.prefix+key).Result()
}

// HSet 设置哈希字段.
func (c *Cache) HSet(ctx context.Context, key string, field string, value any) error {
	if err := checkKey(key); err != nil {
		return err
	}

	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return c.client.HSet(ctx, c.prefix+key, field, data).Err()
}

// HGet 获取哈希字段.
// 字段不存在时返回 ErrCacheMiss.
func (c *Cache) HGet(ctx context.Context, key string, field string, dest any) error {
	if err := checkKey(key); err != nil {
		return err
	}

	data, err := c.client.HGet(ctx, c.prefix+key, field).Bytes()
	if err != nil {
		return wrapRedisErr(err)
	}

	return json.Unmarshal(data, dest)
}

// HGetAll 获取所有哈希字段.
func (c *Cache) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}

	return c.client.HGetAll(ctx, c.prefix+key).Result()
}

// HDel 删除哈希字段.
func (c *Cache) HDel(ctx context.Context, key string, fields ...string) error {
	if err := checkKey(key); err != nil {
		return err
	}

	return c.client.HDel(ctx, c.prefix+key, fields...).Err()
}

// LPush 列表左侧插入.
func (c *Cache) LPush(ctx context.Context, key string, values ...any) error {
	if err := checkKey(key); err != nil {
		return err
	}

	return c.client.LPush(ctx, c.prefix+key, values...).Err()
}

// RPush 列表右侧插入.
func (c *Cache) RPush(ctx context.Context, key string, values ...any) error {
	if err := checkKey(key); err != nil {
		return err
	}

	return c.client.RPush(ctx, c.prefix+key, values...).Err()
}

// LRange 获取列表范围.
func (c *Cache) LRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}

	return c.client.LRange(ctx, c.prefix+key, start, stop).Result()
}

// LLen 获取列表长度.
func (c *Cache) LLen(ctx context.Context, key string) (int64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}

	return c.client.LLen(ctx, c.prefix+key).Result()
}

// SAdd 集合添加.
func (c *Cache) SAdd(ctx context.Context, key string, members ...any) error {
	if err := checkKey(key); err != nil {
		return err
	}

	return c.client.SAdd(ctx, c.prefix+key, members...).Err()
}

// SMembers 获取集合所有成员.
func (c *Cache) SMembers(ctx context.Context, key string) ([]string, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}

	return c.client.SMembers(ctx, c.prefix+key).Result()
}

// SIsMember 检查是否是集合成员.
func (c *Cache) SIsMember(ctx context.Context, key string, member any) (bool, error) {
	if err := checkKey(key); err != nil {
		return false, err
	}

	return c.client.SIsMember(ctx, c.prefix+key, member).Result()
}

// ZAdd 有序集合添加.
func (c *Cache) ZAdd(ctx context.Context, key string, score float64, member string) error {
	if err := checkKey(key); err != nil {
		return err
	}

	return c.client.ZAdd(ctx, c.prefix+key, redisv9.Z{Score: score, Member: member}).Err()
}

// ZRange 获取有序集合范围.
func (c *Cache) ZRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}

	return c.client.ZRange(ctx, c.prefix+key, start, stop).Result()
}

// ZRangeByScore 按分数获取有序集合范围.
func (c *Cache) ZRangeByScore(ctx context.Context, key string, min, max string, offset, count int64) ([]string, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}

	return c.client.ZRangeArgs(ctx, redisv9.ZRangeArgs{
		Key:     c.prefix + key,
		Start:   min,
		Stop:    max,
		Offset:  offset,
		Count:   count,
		ByScore: true,
	}).Result()
}

// ZRem 删除有序集合成员.
func (c *Cache) ZRem(ctx context.Context, key string, members ...any) error {
	if err := checkKey(key); err != nil {
		return err
	}

	return c.client.ZRem(ctx, c.prefix+key, members...).Err()
}

// 缓存键常量.
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

// 默认过期时间.
const (
	DefaultExpiration  = 5 * time.Minute
	ShortExpiration    = 1 * time.Minute
	LongExpiration     = 30 * time.Minute
	VeryLongExpiration = 24 * time.Hour
)

// writeBackTimeout 是 GetOrFetch 回写缓存时使用的独立 context 超时时间。
// 它与调用方的 ctx 解耦，确保即便调用方取消请求，缓存填充仍能完成，
// 从而维持缓存击穿（stampede）保护。该值应略大于 Redis 写入的预期耗时。
const writeBackTimeout = 3 * time.Second

// 缓存辅助函数

// CacheAccount 缓存账户.
func CacheAccount(ctx context.Context, accountID uint64, account any) error {
	key := fmt.Sprintf(KeyAccount, accountID)

	return GetCache().Set(ctx, key, account, LongExpiration)
}

// GetCachedAccount 获取缓存的账户.
// 缓存未命中时返回 ErrCacheMiss.
func GetCachedAccount(ctx context.Context, accountID uint64, dest any) error {
	key := fmt.Sprintf(KeyAccount, accountID)

	return GetCache().Get(ctx, key, dest)
}

// InvalidateAccount 使账户缓存失效.
func InvalidateAccount(ctx context.Context, accountID uint64) error {
	key := fmt.Sprintf(KeyAccount, accountID)

	return GetCache().Delete(ctx, key)
}

// CacheStatus 缓存嘟文.
func CacheStatus(ctx context.Context, statusID uint64, status any) error {
	key := fmt.Sprintf(KeyStatus, statusID)

	return GetCache().Set(ctx, key, status, LongExpiration)
}

// GetCachedStatus 获取缓存的嘟文.
// 缓存未命中时返回 ErrCacheMiss.
func GetCachedStatus(ctx context.Context, statusID uint64, dest any) error {
	key := fmt.Sprintf(KeyStatus, statusID)

	return GetCache().Get(ctx, key, dest)
}

// InvalidateStatus 使嘟文缓存失效.
func InvalidateStatus(ctx context.Context, statusID uint64) error {
	key := fmt.Sprintf(KeyStatus, statusID)

	return GetCache().Delete(ctx, key)
}

// GetOrFetch 是带缓存击穿（stampede）保护的读取模式.
// 先查缓存，命中则直接返回（hit=true）；
// 未命中时通过 singleflight 合并同一 key 的并发回源请求，保证同一时刻只有一个 goroutine 执行 fetch，
// fetch 的返回值在回调内只序列化一次，既写入缓存（带 expiration 的随机抖动），
// 又作为 singleflight 共享结果，各并发调用者只需各做一次反序列化，避免重复序列化.
//
// 参数:
//   - key: 缓存键（不含前缀）
//   - dest: 反序列化目标指针，用于接收缓存或回源结果
//   - expiration: 写入缓存时的过期时间（<=0 表示不缓存）
//   - fetch: 缓存未命中时的回源函数，返回值会被序列化写入缓存并反序列化到 dest
//
// 返回值:
//   - hit: 缓存是否命中（true 表示命中缓存，false 表示触发了回源）
func (c *Cache) GetOrFetch(ctx context.Context, key string, dest any, expiration time.Duration, fetch func() (any, error)) (hit bool, err error) {
	if err = checkKey(key); err != nil {
		return false, err
	}

	// 1. 先尝试读缓存
	err = c.Get(ctx, key, dest)
	if err == nil {
		return true, nil
	}

	// 非"缓存未命中"的真正错误直接返回
	if !errors.Is(err, ErrCacheMiss) {
		return false, err
	}

	// 2. 缓存未命中，singleflight 合并并发回源.
	// 在回调内只序列化一次得到 []byte：既用于回写缓存，又作为 singleflight 的共享结果，
	// 各并发调用者对该共享 []byte 各做一次反序列化即可，避免重复序列化.
	//nolint:contextcheck // singleflight 回写缓存必须用独立 context，解耦调用方生命周期（见上方注释）
	val, ferr := c.getSF().do(key, func() (any, error) {
		v, e := fetch()
		if e != nil {
			return nil, e
		}

		data, e := json.Marshal(v)
		if e != nil {
			return nil, e
		}

		// 回源成功后回写缓存（忽略写缓存的错误，不影响主流程）。
		// 注意：这里必须使用独立的 context，而非调用方的 ctx。
		// 因为 singleflight 会把同一 key 的并发回源合并到首个调用者（leader）执行，
		// 若使用 leader 的 ctx，一旦 leader 的请求被取消（超时/客户端断开），
		// 回写就会失败，导致缓存未被填充，下一个请求周期将再次 miss 并回源，
		// 从而丧失缓存击穿（stampede）保护——而这正是本函数的核心目的。
		// 使用带超时的独立 context 既解耦了调用方生命周期，又防止回写无限阻塞。
		if expiration > 0 {
			writeCtx, cancel := context.WithTimeout(context.Background(), writeBackTimeout)
			_ = c.client.Set(writeCtx, c.prefix+key, data, jitterTTL(expiration)).Err()
			cancel()
		}

		return data, nil
	})
	if ferr != nil {
		return false, ferr
	}

	// 将共享的序列化结果反序列化到 dest
	data, ok := val.([]byte)
	if !ok {
		return false, fmt.Errorf("cache: unexpected fetch result type %T", val)
	}

	if err := json.Unmarshal(data, dest); err != nil {
		return false, err
	}

	return false, nil
}
