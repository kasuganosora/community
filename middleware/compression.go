package middleware

import (
	"bufio"
	"compress/gzip"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Gzip 压缩中间件.
func Gzip() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 检查客户端是否支持 gzip
		if !strings.Contains(c.GetHeader("Accept-Encoding"), "gzip") {
			c.Next()

			return
		}

		// 跳过 SSE 流
		if strings.Contains(c.GetHeader("Accept"), "text/event-stream") {
			c.Next()

			return
		}

		// 跳过已经压缩的响应
		if strings.Contains(c.GetHeader("Content-Encoding"), "gzip") {
			c.Next()

			return
		}

		// 创建 gzip 写入器
		gz := gzip.NewWriter(c.Writer)
		// 在 c.Next() 之后、defer 链中关闭，确保所有数据被 flush 并写入底层 ResponseWriter
		defer func() { _ = gz.Close() }()

		// 包装响应写入器
		c.Writer = &gzipWriter{
			ResponseWriter: c.Writer,
			writer:         gz,
		}

		// 设置响应头
		c.Header("Content-Encoding", "gzip")
		c.Header("Vary", "Accept-Encoding")

		c.Next()

		// 确保 gzip 缓冲区在 defer Close 之前被 flush 到 ResponseWriter
		_ = gz.Flush()
	}
}

// gzipWriter 包装 gin.ResponseWriter 以支持 gzip 压缩.
type gzipWriter struct {
	gin.ResponseWriter

	writer *gzip.Writer
}

func (w *gzipWriter) Write(data []byte) (int, error) {
	return w.writer.Write(data)
}

func (w *gzipWriter) WriteString(s string) (int, error) {
	return w.writer.Write([]byte(s))
}

func (w *gzipWriter) WriteHeader(code int) {
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.Hijack()
}

// ResponseCache 缓存中间件.
func ResponseCache(duration int) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 只缓存 GET 请求
		if c.Request.Method != http.MethodGet {
			c.Next()

			return
		}

		// 跳过认证用户的请求（避免缓存私有数据）
		if _, exists := c.Get("account"); exists {
			c.Next()

			return
		}

		// 设置缓存头
		c.Header("Cache-Control", "public, max-age="+intToString(duration))
		c.Header("Expires", time.Now().Add(time.Duration(duration)*time.Second).Format(http.TimeFormat))

		c.Next()
	}
}

// RateLimit 简单的速率限制中间件.
func RateLimit(limit int, window time.Duration) gin.HandlerFunc {
	// 使用内存存储（生产环境应使用 Redis）
	type limiter struct {
		count   int
		expires time.Time
	}

	var mu sync.Mutex
	limiters := make(map[string]*limiter)
	lastCleanup := time.Now()
	cleanupInterval := 5 * time.Minute

	return func(c *gin.Context) {
		key := c.ClientIP()

		mu.Lock()

		if l, ok := limiters[key]; ok {
			if time.Now().Before(l.expires) {
				if l.count >= limit {
					mu.Unlock()
					c.AbortWithStatusJSON(429, gin.H{"error": "rate limit exceeded"})

					return
				}

				l.count++
			} else {
				l.count = 1
				l.expires = time.Now().Add(window)
			}
		} else {
			limiters[key] = &limiter{
				count:   1,
				expires: time.Now().Add(window),
			}
		}

		// 定期清理过期的 limiter 条目，防止内存泄漏
		if time.Since(lastCleanup) > cleanupInterval {
			lastCleanup = time.Now()
			now := time.Now()
			for k, l := range limiters {
				if !now.Before(l.expires) {
					delete(limiters, k)
				}
			}
		}

		mu.Unlock()

		c.Next()
	}
}

// intToString 简单的整数转字符串.
func intToString(n int) string {
	return strconv.Itoa(n)
}
