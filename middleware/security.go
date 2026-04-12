package middleware

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// SecurityHeadersMiddleware 添加安全 HTTP 头部
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 防止 MIME 类型嗅探
		c.Header("X-Content-Type-Options", "nosniff")
		// 防止点击劫持
		c.Header("X-Frame-Options", "DENY")
		// XSS 防护
		c.Header("X-XSS-Protection", "1; mode=block")
		// 引用策略
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		// 内容安全策略（可根据需要调整）
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'")
		// HSTS（仅 HTTPS 环境启用）
		// c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")

		c.Next()
	}
}

// CORSMiddleware CORS 跨域中间件
func CORSMiddleware(allowOrigins []string) gin.HandlerFunc {
	config := cors.DefaultConfig()

	if len(allowOrigins) == 0 {
		config.AllowAllOrigins = true
	} else {
		config.AllowOrigins = allowOrigins
	}

	config.AllowMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}
	config.AllowHeaders = []string{
		"Origin",
		"Content-Length",
		"Content-Type",
		"Authorization",
		"Accept",
		"X-Requested-With",
		"X-CSRF-Token",
	}
	config.ExposeHeaders = []string{
		"Content-Length",
		"Content-Type",
	}
	config.AllowCredentials = true
	config.MaxAge = 12 * time.Hour

	return cors.New(config)
}

// RequestSizeLimitMiddleware 请求大小限制
func RequestSizeLimitMiddleware(maxSize int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxSize {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
				"error": "request body too large",
			})
			return
		}
		c.Next()
	}
}

// IPWhitelistMiddleware IP 白名单中间件
func IPWhitelistMiddleware(whitelist []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := c.ClientIP()

		// 如果没有设置白名单，允许所有
		if len(whitelist) == 0 {
			c.Next()
			return
		}

		// 检查 IP 是否在白名单中
		for _, ip := range whitelist {
			if ip == clientIP {
				c.Next()
				return
			}
			// 支持 CIDR 格式
			if strings.Contains(ip, "/") {
				if isIPInCIDR(clientIP, ip) {
					c.Next()
					return
				}
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "IP not allowed",
		})
	}
}

// isIPInCIDR 检查 IP 是否在 CIDR 范围内（支持 IPv4 和 IPv6）
func isIPInCIDR(ip, cidr string) bool {
	// 解析 IP 地址
	clientIP := net.ParseIP(ip)
	if clientIP == nil {
		return false
	}

	// 解析 CIDR
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		// 如果不是 CIDR 格式，尝试作为单个 IP 比较
		allowedIP := net.ParseIP(cidr)
		if allowedIP == nil {
			return false
		}
		return clientIP.Equal(allowedIP)
	}

	// 检查 IP 是否在 CIDR 范围内
	return ipNet.Contains(clientIP)
}

// isValidIP 验证 IP 地址是否有效（支持 IPv4 和 IPv6）
func isValidIP(ip string) bool {
	return net.ParseIP(ip) != nil
}

// getIPVersion 获取 IP 版本（4 或 6）
func getIPVersion(ip string) int {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return 0
	}
	if parsedIP.To4() != nil {
		return 4
	}
	return 6
}

// normalizeIP 规范化 IP 地址（处理 IPv4-mapped IPv6 地址）
func normalizeIP(ip string) string {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return ip
	}

	// 如果是 IPv4-mapped IPv6 地址（如 ::ffff:192.168.1.1），转换为 IPv4
	if ipv4 := parsedIP.To4(); ipv4 != nil {
		return ipv4.String()
	}

	return parsedIP.String()
}
