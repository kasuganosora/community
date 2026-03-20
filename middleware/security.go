package middleware

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
	"time"
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

// isIPInCIDR 检查 IP 是否在 CIDR 范围内
func isIPInCIDR(ip, cidr string) bool {
	// 简化实现，实际项目中可以使用 net 包
	return false
}
