package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
	"golang.org/x/time/rate"
)

// RateLimiterConfig 限流配置
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

// APILimit API 特定限流配置
type APILimit struct {
	RPS   float64
	Burst int
}

// DefaultRateLimiterConfig 默认限流配置
func DefaultRateLimiterConfig() RateLimiterConfig {
	return RateLimiterConfig{
		GlobalRPS:   1000,
		GlobalBurst: 1500,
		PerUserRPM:  300,
		PerIPRPM:    600,
		APILimits: map[string]APILimit{
			"/api/v1/media":     {RPS: 10, Burst: 20},   // 媒体上传限制
			"/api/v2/media":     {RPS: 10, Burst: 20},
			"/_matrix/federation": {RPS: 100, Burst: 200}, // 联邦接口限制
		},
	}
}

// RateLimiter 限流器接口
type RateLimiter interface {
	Allow(key string) bool
	AllowN(key string, n int) bool
}

// MemoryRateLimiter 内存限流器（单机使用）
type MemoryRateLimiter struct {
	limiters map[string]*rate.Limiter
	config   RateLimiterConfig
}

// NewMemoryRateLimiter 创建内存限流器
func NewMemoryRateLimiter(config RateLimiterConfig) *MemoryRateLimiter {
	return &MemoryRateLimiter{
		limiters: make(map[string]*rate.Limiter),
		config:   config,
	}
}

// Allow 检查是否允许请求
func (m *MemoryRateLimiter) Allow(key string) bool {
	return m.getLimiter(key).Allow()
}

// AllowN 检查是否允许 N 个请求
func (m *MemoryRateLimiter) AllowN(key string, n int) bool {
	return m.getLimiter(key).AllowN(time.Now(), n)
}

// getLimiter 获取或创建限流器
func (m *MemoryRateLimiter) getLimiter(key string) *rate.Limiter {
	if limiter, ok := m.limiters[key]; ok {
		return limiter
	}
	limiter := rate.NewLimiter(rate.Limit(m.config.GlobalRPS), m.config.GlobalBurst)
	m.limiters[key] = limiter
	return limiter
}

// RedisRateLimiter Redis 分布式限流器
type RedisRateLimiter struct {
	client *redis.Client
	config RateLimiterConfig
}

// NewRedisRateLimiter 创建 Redis 限流器
func NewRedisRateLimiter(client *redis.Client, config RateLimiterConfig) *RedisRateLimiter {
	return &RedisRateLimiter{
		client: client,
		config: config,
	}
}

// Allow 检查是否允许请求（使用 Redis 令牌桶算法）
func (r *RedisRateLimiter) Allow(key string) bool {
	return r.AllowN(key, 1)
}

// AllowN 检查是否允许 N 个请求
func (r *RedisRateLimiter) AllowN(key string, n int) bool {
	ctx := r.client.Context()
	
	// Redis Lua 脚本实现令牌桶
	script := `
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
	
	now := time.Now().Unix()
	result, err := r.client.Eval(ctx, script, []string{key}, 
		r.config.GlobalRPS, 
		r.config.GlobalBurst, 
		n, 
		now,
	).Result()
	
	if err != nil {
		log.Error("Redis rate limit error: %v", err)
		return true // 降级：允许请求
	}
	
	return result.(int64) == 1
}

// RateLimitMiddleware 限流中间件
func RateLimitMiddleware(limiter RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := getRateLimitKey(c)
		
		if !limiter.Allow(key) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "rate limit exceeded",
				"retry_after": 60,
			})
			return
		}
		
		c.Next()
	}
}

// MultiLevelRateLimitMiddleware 多级限流中间件（全局 + 用户 + IP）
func MultiLevelRateLimitMiddleware(redisClient *redis.Client) gin.HandlerFunc {
	config := DefaultRateLimiterConfig()
	
	var limiter RateLimiter
	if redisClient != nil {
		limiter = NewRedisRateLimiter(redisClient, config)
	} else {
		limiter = NewMemoryRateLimiter(config)
	}
	
	return func(c *gin.Context) {
		// 1. 全局限流
		if !limiter.Allow("global") {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "global rate limit exceeded",
			})
			return
		}
		
		// 2. IP 限流
		ipKey := fmt.Sprintf("ip:%s", c.ClientIP())
		if !limiter.Allow(ipKey) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "IP rate limit exceeded",
			})
			return
		}
		
		// 3. 用户限流（已认证用户）
		if account, exists := c.Get("account"); exists {
			userID := fmt.Sprintf("%v", account)
			userKey := fmt.Sprintf("user:%s", userID)
			if !limiter.Allow(userKey) {
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
					"error": "user rate limit exceeded",
				})
				return
			}
		}
		
		c.Next()
	}
}

// APIRateLimitMiddleware API 特定限流中间件
func APIRateLimitMiddleware(redisClient *redis.Client, pathPrefix string, rpm int) gin.HandlerFunc {
	config := RateLimiterConfig{
		GlobalRPS:   float64(rpm) / 60,
		GlobalBurst: rpm / 6,
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
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       fmt.Sprintf("API rate limit exceeded for %s", pathPrefix),
				"retry_after": 60,
			})
			return
		}
		
		c.Next()
	}
}

// getRateLimitKey 获取限流键
func getRateLimitKey(c *gin.Context) string {
	// 优先使用用户 ID
	if account, exists := c.Get("account"); exists {
		return fmt.Sprintf("user:%v", account)
	}
	
	// 使用 IP
	return fmt.Sprintf("ip:%s", c.ClientIP())
}

// hasPrefix 检查字符串是否有前缀
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// GetRedisClient 获取 Redis 客户端
func GetRedisClient() *redis.Client {
	// 检查 Redis 配置是否完整
	if config.C.Redis0Host == "" {
		return nil
	}
	
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", config.C.Redis0Host, config.C.Redis0Port),
		Password: config.C.Redis0Password,
		DB:       parseInt(config.C.Redis0Db, 0),
	})
	
	return client
}

// parseInt 解析整数，失败返回默认值
func parseInt(s string, defaultVal int) int {
	i, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return i
}

// SlidingWindowRateLimiter 滑动窗口限流器（更精确的限流）
type SlidingWindowRateLimiter struct {
	client     *redis.Client
	windowSize time.Duration
	maxRequests int
}

// NewSlidingWindowRateLimiter 创建滑动窗口限流器
func NewSlidingWindowRateLimiter(client *redis.Client, windowSize time.Duration, maxRequests int) *SlidingWindowRateLimiter {
	return &SlidingWindowRateLimiter{
		client:      client,
		windowSize:  windowSize,
		maxRequests: maxRequests,
	}
}

// Allow 检查是否允许请求
func (s *SlidingWindowRateLimiter) Allow(key string) bool {
	if s.client == nil {
		return true // 降级
	}
	
	ctx := s.client.Context()
	now := time.Now().Unix()
	windowStart := now - int64(s.windowSize.Seconds())
	
	// 使用 Redis Sorted Set 实现滑动窗口
	pipe := s.client.Pipeline()
	
	// 移除窗口外的请求记录
	pipe.ZRemRangeByScore(ctx, key, "0", strconv.FormatInt(windowStart, 10))
	
	// 获取当前窗口内的请求数
	countCmd := pipe.ZCard(ctx, key)
	
	// 添加当前请求
	pipe.ZAdd(ctx, key, &redis.Z{
		Score:  float64(now),
		Member: fmt.Sprintf("%d:%d", now, now),
	})
	
	// 设置过期时间
	pipe.Expire(ctx, key, s.windowSize)
	
	_, err := pipe.Exec(ctx)
	if err != nil {
		log.Error("Redis sliding window error: %v", err)
		return true // 降级
	}
	
	count := countCmd.Val()
	return count < int64(s.maxRequests)
}
