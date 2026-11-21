package middleware

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

// RateLimiterConfig 限流配置.
type RateLimiterConfig struct {
	// 全局限流：每秒请求数
	GlobalRPS float64
	// 全局限流：突发请求数
	GlobalBurst int
	// 每用户限流：每分钟请求数
	PerUserRPM int
	// 每 IP 限流：每分钟请求数
	PerIPRPM int
	// API 特定限流配置
	APILimits map[string]APILimit
}

// APILimit API 特定限流配置.
type APILimit struct {
	RPS   float64
	Burst int
}

// DefaultRateLimiterConfig 默认限流配置.
func DefaultRateLimiterConfig() RateLimiterConfig {
	return RateLimiterConfig{
		GlobalRPS:   1000,
		GlobalBurst: 1500,
		PerUserRPM:  300,
		PerIPRPM:    600,
		APILimits: map[string]APILimit{
			"/api/v1/media":       {RPS: 10, Burst: 20}, // 媒体上传限制 //nolint:goconst
			"/api/v2/media":       {RPS: 10, Burst: 20},
			"/_matrix/federation": {RPS: 100, Burst: 200}, // 联邦接口限制
		},
	}
}

func rateForKey(cfg RateLimiterConfig, key string) (float64, int) {
	if strings.HasPrefix(key, "ip:") && cfg.PerIPRPM > 0 {
		return float64(cfg.PerIPRPM) / 60.0, max(1, cfg.PerIPRPM/6)
	}
	if strings.HasPrefix(key, "user:") && cfg.PerUserRPM > 0 {
		return float64(cfg.PerUserRPM) / 60.0, max(1, cfg.PerUserRPM/6)
	}
	return cfg.GlobalRPS, cfg.GlobalBurst
}

// RateLimiter 限流器接口.
type RateLimiter interface {
	Allow(key string) bool
	AllowN(key string, n int) bool
}

const numRateLimiterShards = 64

type memoryLimiterShard struct {
	mu          sync.RWMutex
	limiters    map[string]*limiterEntry
	lastCleanup int64
}

// MemoryRateLimiter 内存限流器（单机使用，64 分片读写锁消除全局锁竞争）.
type MemoryRateLimiter struct {
	limiters        map[string]*limiterEntry // 保留兼容字段供测试断言
	shards          [numRateLimiterShards]memoryLimiterShard
	config          RateLimiterConfig
	cleanupInterval time.Duration
	maxIdle         time.Duration // limiter 最大空闲时间，超过则被清理
}

// limiterEntry 包装 rate.Limiter 并记录最后访问时间.
type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen atomic.Int64
}

// NewMemoryRateLimiter 创建内存限流器.
func NewMemoryRateLimiter(config RateLimiterConfig) *MemoryRateLimiter {
	now := time.Now().UnixNano()
	m := &MemoryRateLimiter{
		limiters:        make(map[string]*limiterEntry),
		config:          config,
		cleanupInterval: 5 * time.Minute,
		maxIdle:         10 * time.Minute,
	}
	for i := range m.shards {
		m.shards[i].limiters = make(map[string]*limiterEntry)
		m.shards[i].lastCleanup = now
	}
	return m
}

func (m *MemoryRateLimiter) shardFor(key string) *memoryLimiterShard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &m.shards[h.Sum32()&(numRateLimiterShards-1)]
}

// Allow 检查是否允许请求.
func (m *MemoryRateLimiter) Allow(key string) bool {
	return m.getLimiter(key).Allow()
}

// AllowN 检查是否允许 N 个请求.
func (m *MemoryRateLimiter) AllowN(key string, n int) bool {
	return m.getLimiter(key).AllowN(time.Now(), n)
}

// getLimiter 获取或创建限流器.
func (m *MemoryRateLimiter) getLimiter(key string) *rate.Limiter {
	now := time.Now().UnixNano()
	s := m.shardFor(key)

	s.mu.RLock()
	if entry, ok := s.limiters[key]; ok {
		entry.lastSeen.Store(now)
		s.mu.RUnlock()
		return entry.limiter
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.limiters[key]; ok {
		entry.lastSeen.Store(now)
		return entry.limiter
	}

	rps, burst := rateForKey(m.config, key)
	limiter := rate.NewLimiter(rate.Limit(rps), burst)
	entry := &limiterEntry{limiter: limiter}
	entry.lastSeen.Store(now)
	s.limiters[key] = entry

	if now-s.lastCleanup > int64(m.cleanupInterval) {
		m.cleanupShard(s, now)
	}

	return limiter
}

// cleanupShard 清理当前分片中超过 maxIdle 未使用的 limiter.
func (m *MemoryRateLimiter) cleanupShard(s *memoryLimiterShard, now int64) {
	s.lastCleanup = now
	maxIdleNano := int64(m.maxIdle)
	for k, entry := range s.limiters {
		if k != "global" && now-entry.lastSeen.Load() > maxIdleNano {
			delete(s.limiters, k)
		}
	}
}

const redisRateLimitTimeout = 150 * time.Millisecond

// RedisRateLimiter Redis 分布式限流器.
type RedisRateLimiter struct {
	client *redis.Client
	config RateLimiterConfig
}

// NewRedisRateLimiter 创建 Redis 限流器.
func NewRedisRateLimiter(client *redis.Client, config RateLimiterConfig) *RedisRateLimiter {
	return &RedisRateLimiter{
		client: client,
		config: config,
	}
}

// Allow 检查是否允许请求（使用 Redis 令牌桶算法）.
func (r *RedisRateLimiter) Allow(key string) bool {
	return r.AllowN(key, 1)
}

// rateLimitScript Redis Lua 脚本实现令牌桶算法（包级常量，避免每次请求重新分配）.
const rateLimitScript = `
		local key = KEYS[1]
		local rate = tonumber(ARGV[1])
		local capacity = tonumber(ARGV[2])
		local requested = tonumber(ARGV[3])
		local now = tonumber(ARGV[4])
		
		local last_updated = redis.call('hget', key, 'last_updated')
		local tokens = redis.call('hget', key, 'tokens')
		
		if last_updated == false then
			last_updated = now
			tokens = capacity
		else
			last_updated = tonumber(last_updated)
			tokens = tonumber(tokens)
		end
		
		-- 计算新增令牌
		local delta = math.max(0, now - last_updated)
		tokens = math.min(capacity, tokens + delta * rate)
		
		local allowed = tokens >= requested
		if allowed then
			tokens = tokens - requested
		end
		
		redis.call('hset', key, 'tokens', tokens)
		redis.call('hset', key, 'last_updated', now)
		redis.call('expire', key, 60)
		
		return allowed and 1 or 0
	`

// AllowN 检查是否允许 N 个请求.
func (r *RedisRateLimiter) AllowN(key string, n int) bool {
	// 防御：未配置 Redis 时降级放行
	if r.client == nil {
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), redisRateLimitTimeout)
	defer cancel()

	now := time.Now().Unix()
	rps, burst := rateForKey(r.config, key)

	result, err := r.client.Eval(ctx, rateLimitScript, []string{key},
		rps,
		burst,
		n,
		now,
	).Result()
	if err != nil {
		log.Error("Redis rate limit error: %v", err)

		return true // 降级：允许请求
	}

	val, ok := result.(int64)
	if !ok {
		log.Error("Redis rate limit unexpected result type: %T", result)

		return true // 降级：允许请求
	}

	return val == 1
}

// allowMulti executes multiple tier checks in a single Redis Pipeline RTT.
// Returns -1 when all keys pass, or the index of the first rejected key.
func (r *RedisRateLimiter) allowMulti(ctx context.Context, keys []string) int {
	if r.client == nil || len(keys) == 0 {
		return -1
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rctx, cancel := context.WithTimeout(ctx, redisRateLimitTimeout)
	defer cancel()

	now := time.Now().Unix()
	pipe := r.client.Pipeline()
	cmds := make([]*redis.Cmd, len(keys))
	for i, key := range keys {
		rps, burst := rateForKey(r.config, key)
		cmds[i] = pipe.Eval(rctx, rateLimitScript, []string{key}, rps, burst, 1, now)
	}
	if _, err := pipe.Exec(rctx); err != nil {
		log.Error("Redis rate limit pipeline error: %v", err)
		return -1 // 降级：允许请求
	}
	for i, cmd := range cmds {
		if val, ok := cmd.Val().(int64); ok && val != 1 {
			return i
		}
	}
	return -1
}

// RateLimitMiddleware 限流中间件.
func RateLimitMiddleware(limiter RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := getRateLimitKey(c)

		if !limiter.Allow(key) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "rate limit exceeded", //nolint:goconst
				"retry_after": 60,
			})

			return
		}

		c.Next()
	}
}

// MultiLevelRateLimitMiddleware 多级限流中间件（全局 + 用户 + IP）.
func MultiLevelRateLimitMiddleware(redisClient *redis.Client) gin.HandlerFunc {
	config := DefaultRateLimiterConfig()

	var redisLimiter *RedisRateLimiter
	var memLimiter *MemoryRateLimiter
	if redisClient != nil {
		redisLimiter = NewRedisRateLimiter(redisClient, config)
	} else {
		memLimiter = NewMemoryRateLimiter(config)
	}

	tierErrors := [3]string{
		"global rate limit exceeded",
		"IP rate limit exceeded",
		"user rate limit exceeded",
	}

	return func(c *gin.Context) {
		ipKey := "ip:" + c.ClientIP()
		keys := make([]string, 0, 3)
		keys = append(keys, "global", ipKey)

		if account, exists := c.Get("account"); exists {
			var userID string
			if acct, ok := account.(*models.Account); ok && acct != nil {
				userID = strconv.FormatUint(acct.Id, 10)
			} else {
				userID = fmt.Sprintf("%v", account)
			}
			keys = append(keys, "user:"+userID)
		}

		if redisLimiter != nil {
			var reqCtx context.Context
			if c.Request != nil {
				reqCtx = c.Request.Context()
			}
			if idx := redisLimiter.allowMulti(reqCtx, keys); idx >= 0 && idx < len(tierErrors) {
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
					"error": tierErrors[idx],
				})
				return
			}
			c.Next()
			return
		}

		for i, k := range keys {
			if !memLimiter.Allow(k) {
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
					"error": tierErrors[i],
				})
				return
			}
		}

		c.Next()
	}
}

// APIRateLimitMiddleware API 特定限流中间件.
func APIRateLimitMiddleware(redisClient *redis.Client, pathPrefix string, rpm int) gin.HandlerFunc {
	config := RateLimiterConfig{
		GlobalRPS:   float64(rpm) / 60,
		GlobalBurst: max(1, rpm/6),
	}

	var limiter RateLimiter
	if redisClient != nil {
		limiter = NewRedisRateLimiter(redisClient, config)
	} else {
		limiter = NewMemoryRateLimiter(config)
	}

	return func(c *gin.Context) {
		// 只处理匹配的 API 路径
		if !hasPrefix(c.FullPath(), pathPrefix) {
			c.Next()

			return
		}

		key := fmt.Sprintf("api:%s:%s", pathPrefix, getRateLimitKey(c))

		if !limiter.Allow(key) {
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "API rate limit exceeded for " + pathPrefix,
				"retry_after": 60,
			})

			return
		}

		c.Next()
	}
}

// getRateLimitKey 获取限流键.
func getRateLimitKey(c *gin.Context) string {
	// 优先使用用户 ID
	if account, exists := c.Get("account"); exists {
		if acct, ok := account.(*models.Account); ok && acct != nil {
			return "user:" + strconv.FormatUint(acct.Id, 10)
		}

		// 非 *Account 类型（如测试中的 string/int），使用 %v 格式化
		return fmt.Sprintf("user:%v", account)
	}

	// 使用 IP
	return "ip:" + c.ClientIP()
}

// hasPrefix 检查字符串是否有前缀.
func hasPrefix(s, prefix string) bool {
	return strings.HasPrefix(s, prefix)
}

// 全局 Redis 客户端单例.
var (
	globalRedisClient     *redis.Client
	globalRedisClientOnce sync.Once
)

// GetRedisClient 获取 Redis 客户端（复用全局 db/redis 连接，单例模式）.
func GetRedisClient() *redis.Client {
	globalRedisClientOnce.Do(func() {
		// 复用 db/redis 包中已创建的连接，避免创建多余的连接
		// 如果没有配置 Redis，返回 nil 而不是 panic
		defer func() {
			if r := recover(); r != nil {
				globalRedisClient = nil
			}
		}()
		globalRedisClient = db.GetRedis()
	})

	return globalRedisClient
}

// parseInt 解析整数，失败返回默认值.
func parseInt(s string, defaultVal int) int {
	i, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}

	return i
}

// SlidingWindowRateLimiter 滑动窗口限流器（更精确的限流）.
type SlidingWindowRateLimiter struct {
	client      *redis.Client
	windowSize  time.Duration
	maxRequests int
}

// NewSlidingWindowRateLimiter 创建滑动窗口限流器.
func NewSlidingWindowRateLimiter(client *redis.Client, windowSize time.Duration, maxRequests int) *SlidingWindowRateLimiter {
	return &SlidingWindowRateLimiter{
		client:      client,
		windowSize:  windowSize,
		maxRequests: maxRequests,
	}
}

var slidingWindowCounter atomic.Uint64

const slidingWindowScript = `
local key = KEYS[1]
local window_start = tonumber(ARGV[1])
local now = tonumber(ARGV[2])
local max_req = tonumber(ARGV[3])
local member = ARGV[4]
local ttl_ms = tonumber(ARGV[5])

redis.call('ZREMRANGEBYSCORE', key, '-inf', window_start)
local count = redis.call('ZCARD', key)
if count >= max_req then
	return 0
end
redis.call('ZADD', key, now, member)
redis.call('PEXPIRE', key, ttl_ms)
return 1
`

// Allow 检查是否允许请求.
func (s *SlidingWindowRateLimiter) Allow(key string) bool {
	if s.client == nil {
		return true // 降级
	}

	ctx, cancel := context.WithTimeout(context.Background(), redisRateLimitTimeout)
	defer cancel()

	now := time.Now().UnixMilli()
	windowStart := now - s.windowSize.Milliseconds()
	member := fmt.Sprintf("%d:%d", now, slidingWindowCounter.Add(1))

	res, err := s.client.Eval(ctx, slidingWindowScript, []string{key},
		windowStart,
		now,
		s.maxRequests,
		member,
		max(int64(1), s.windowSize.Milliseconds()),
	).Int64()
	if err != nil {
		log.Error("Redis sliding window error: %v", err)

		return true // 降级
	}

	return res == 1
}
