package cache

import (
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	redisv9 "github.com/redis/go-redis/v9"
)

// jitterRatio 是 TTL 抖动的最大比例，用于防止缓存雪崩.
// 实际过期时间 = expiration + rand(expiration * jitterRatio).
const jitterRatio = 0.1

// jitterTTL 在原有过期时间上叠加 0~10% 的随机抖动，防止大量 key 同时过期造成缓存雪崩.
// expiration <= 0 时原样返回（表示永不过期/不设置 TTL）.
//
// 这是一个包级变量，测试中可替换为恒等函数以进行精确匹配.
var jitterTTL = jitterTTLImpl

// jitterTTLImpl 是 jitterTTL 的默认实现.
func jitterTTLImpl(expiration time.Duration) time.Duration {
	if expiration <= 0 {
		return expiration
	}

	// 随机增加 0 ~ 10% 的抖动
	jitter := time.Duration(randInt63n(int64(float64(expiration) * jitterRatio)))

	return expiration + jitter
}

// randInt63n 返回 [0, n) 范围内的伪随机 int64，n<=0 时返回 0.
// 使用 math/rand/v2 的全局函数，Go 1.20+ 全局已自动加锁，无需手动 seed.
func randInt63n(n int64) int64 {
	if n <= 0 {
		return 0
	}

	return rand.Int64N(n)
}

// wrapRedisErr 将 go-redis 的错误统一包装为缓存层语义.
// redis.Nil（key 不存在）转换为 ErrCacheMiss，其余错误原样返回.
func wrapRedisErr(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, redisv9.Nil) {
		return ErrCacheMiss
	}

	return err
}

// ============================================================
// singleflight 实现（缓存击穿保护）
// ============================================================
//
// flightGroup 确保同一 key 的并发调用只执行一次回源函数，
// 其他并发调用者共享同一次结果，避免缓存击穿。
//
// 采用 Go 官方 golang.org/x/sync/singleflight 的等价实现，
// 不引入额外依赖。

// call 代表一次正在进行的或已完成的回源调用.
type call struct {
	wg  sync.WaitGroup
	val any
	err error
}

// flightGroup 管理按 key 去重的回源调用.
type flightGroup struct {
	mu sync.Mutex
	m  map[string]*call
}

// newFlightGroup 创建一个 flightGroup.
func newFlightGroup() *flightGroup {
	return &flightGroup{
		m: make(map[string]*call),
	}
}

// do 对同一 key 的并发调用做去重，保证 fn 只执行一次.
// 后续相同 key 的调用会等待首个调用完成并共享其结果.
func (g *flightGroup) do(key string, fn func() (any, error)) (any, error) {
	g.mu.Lock()

	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait() // 等待正在进行的调用完成

		return c.val, c.err
	}

	c := &call{}
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	defer func() {
		g.mu.Lock()
		delete(g.m, key)
		g.mu.Unlock()
	}()

	// defer Done 保证即使 fn panic 也不会永久阻塞等待者.
	defer c.wg.Done()

	c.val, c.err = fn()

	return c.val, c.err
}
