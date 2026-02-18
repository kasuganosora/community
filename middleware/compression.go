package middleware

import (
	"bufio"
	"compress/gzip"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Gzip 压缩中间件
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
		defer gz.Close()

		// 包装响应写入器
		c.Writer = &gzipWriter{
			ResponseWriter: c.Writer,
			writer:         gz,
		}

		// 设置响应头
		c.Header("Content-Encoding", "gzip")
		c.Header("Vary", "Accept-Encoding")

		c.Next()
	}
}

// gzipWriter 包装 gin.ResponseWriter 以支持 gzip 压缩
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

// ResponseCache 缓存中间件
func ResponseCache(duration int) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 只缓存 GET 请求
		if c.Request.Method != "GET" {
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

// RateLimit 简单的速率限制中间件
func RateLimit(limit int, window time.Duration) gin.HandlerFunc {
	// 使用内存存储（生产环境应使用 Redis）
	type limiter struct {
		count   int
		expires time.Time
	}
	limiters := make(map[string]*limiter)

	return func(c *gin.Context) {
		key := c.ClientIP()

		if l, ok := limiters[key]; ok {
			if time.Now().Before(l.expires) {
				if l.count >= limit {
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

		c.Next()
	}
}

// intToString 简单的整数转字符串
func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	var neg bool
	if n < 0 {
		neg = true
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
