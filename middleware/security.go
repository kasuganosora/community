package middleware

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// SecurityHeadersMiddleware 添加安全 HTTP 头部.
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 防止 MIME 类型嗅探
		c.Header("X-Content-Type-Options", "nosniff")
		// 防止点击劫持
		c.Header("X-Frame-Options", "DENY")
		// XSS 防护
		c.Header("X-XSS-Protection", "0")
		// 引用策略
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		// 权限策略：禁用不需要的浏览器 API
		c.Header("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=()")
		// 跨域隔离，提供针对 Spectre 类侧信道攻击的深度防御
		c.Header("Cross-Origin-Opener-Policy", "same-origin")
		c.Header("Cross-Origin-Resource-Policy", "same-origin")
		// Separate web clients embed public media and authenticated Matrix media.
		// Element's service worker forwards authenticated responses to image
		// requests; same-origin CORP would block those successfully authorized
		// bytes. The Matrix routes still require their normal bearer token.
		path := c.Request.URL.Path
		// The emoji route serves public generated image files, which must be
		// embeddable by federated clients. Match only its single file segment;
		// admin APIs and similarly named/private routes retain same-origin.
		emojiFile, emojiAsset := strings.CutPrefix(path, "/emoji-assets/")
		publicEmoji := (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && emojiAsset &&
			emojiFile != "" && emojiFile != "." && emojiFile != ".." &&
			!strings.ContainsAny(emojiFile, "/\\")
		if publicEmoji || strings.HasPrefix(path, "/media/") || strings.HasPrefix(path, "/card-images/") || strings.HasPrefix(path, "/proxy/media/") ||
			strings.HasPrefix(path, "/_matrix/client/v1/media/download/") ||
			strings.HasPrefix(path, "/_matrix/client/v1/media/thumbnail/") {
			c.Header("Cross-Origin-Resource-Policy", "cross-origin")
		}
		// 内容安全策略
		// TODO: 前端迁移到 nonce-based CSP 后移除 'unsafe-inline'
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; media-src 'self' https:; connect-src 'self' wss: https:; font-src 'self' data:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		// HSTS（仅 HTTPS 环境启用）
		if c.Request.TLS != nil {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		c.Next()
	}
}

// CORSMiddleware CORS 跨域中间件.
func CORSMiddleware(allowOrigins []string) gin.HandlerFunc {
	config := cors.DefaultConfig()

	if len(allowOrigins) == 0 {
		// 开发模式下允许所有来源；生产环境必须配置具体来源
		config.AllowAllOrigins = true
		config.AllowCredentials = false // AllowAllOrigins 时必须禁用 credentials
	} else {
		// 检查是否包含通配符 "*"。同时使用 AllowOrigins=["*"] 和
		// AllowCredentials=true 违反 CORS 规范，浏览器会拒绝该响应。
		// 此处将其规范化为 AllowAllOrigins（同时禁用 credentials）。
		allOrigins := false
		for _, o := range allowOrigins {
			if o == "*" {
				allOrigins = true

				break
			}
		}

		if allOrigins {
			config.AllowAllOrigins = true
			config.AllowCredentials = false
		} else {
			config.AllowOrigins = allowOrigins
			config.AllowCredentials = true
		}
	}

	config.AllowMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} //nolint:goconst
	config.AllowHeaders = []string{
		"Origin",
		"Content-Length",
		"Content-Type",
		"Authorization",
		"Accept",
		"X-Requested-With",
		"X-CSRF-Token",
		"X-Request-ID",
	}
	config.ExposeHeaders = []string{
		"Link",
		"X-Total-Count",
		"Content-Length",
		"Content-Type",
		"X-Request-ID",
	}
	config.MaxAge = 12 * time.Hour

	return cors.New(config)
}

// RequestSizeLimitMiddleware 请求大小限制.
func RequestSizeLimitMiddleware(maxSize int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		// ContentLength > maxSize 直接拒绝
		if c.Request.ContentLength > maxSize {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
				"error": "request body too large",
			})

			return
		}

		// 对于 chunked transfer encoding（ContentLength == -1），
		// 使用 http.MaxBytesReader 限制实际读取大小
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSize)

		c.Next()
	}
}

// DefaultMaxRequestBodySize bounds ordinary (non-upload) request bodies:
// JSON/form API calls, Matrix client requests, etc.
const DefaultMaxRequestBodySize int64 = 10 << 20 // 10 MB

// matrixMediaMinBodySize matches the largest Matrix media upload size the
// homeserver advertises (m.upload.size in media_async.go).
const matrixMediaMinBodySize int64 = 100 << 20

// multipartOverhead leaves room for multipart boundaries and the other form
// fields (description, focus, thumbnail) next to the file itself.
const multipartOverhead int64 = 1 << 20

// BodySizeLimitMiddleware caps request bodies. Media uploads (multipart
// forms and Matrix media uploads) may be as large as the configured media
// limits returned by mediaLimit (the same values the upload handler enforces
// and /api/v2/instance advertises); every other request is limited to
// defaultLimit.
func BodySizeLimitMiddleware(defaultLimit int64, mediaLimit func() int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		RequestSizeLimitMiddleware(bodyLimitFor(c.Request, defaultLimit, mediaLimit))(c)
	}
}

func bodyLimitFor(r *http.Request, defaultLimit int64, mediaLimit func() int64) int64 {
	isMultipart := strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data")
	if r.Method == http.MethodPost && isMultipart &&
		(r.URL.Path == "/api/v1/data/imports" || r.URL.Path == "/api/v1/admin/data/imports") {
		return 256<<20 + multipartOverhead
	}
	isMatrixMedia := strings.HasPrefix(r.URL.Path, "/_matrix/media/") ||
		(strings.HasPrefix(r.URL.Path, "/_matrix/client/") && strings.Contains(r.URL.Path, "/media/"))

	if !isMultipart && !isMatrixMedia {
		return defaultLimit
	}

	limit := mediaLimit() + multipartOverhead
	if isMatrixMedia && limit < matrixMediaMinBodySize {
		limit = matrixMediaMinBodySize
	}

	if limit < defaultLimit {
		limit = defaultLimit
	}

	return limit
}

// IPWhitelistMiddleware IP 白名单中间件.
func IPWhitelistMiddleware(whitelist []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := c.ClientIP()

		// 如果没有设置白名单，允许所有
		if len(whitelist) == 0 {
			c.Next()

			return
		}

		// 检查 IP 是否在白名单中（支持 CIDR 和单个 IP）
		for _, allowed := range whitelist {
			if isIPInCIDR(clientIP, allowed) {
				c.Next()

				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "IP not allowed",
		})
	}
}

// isIPInCIDR 检查 IP 是否在 CIDR 范围内（支持 IPv4 和 IPv6）.
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

// isValidIP 验证 IP 地址是否有效（支持 IPv4 和 IPv6）.
func isValidIP(ip string) bool {
	return net.ParseIP(ip) != nil
}

// getIPVersion 获取 IP 版本（4 或 6）.
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

// normalizeIP 规范化 IP 地址（处理 IPv4-mapped IPv6 地址）.
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
