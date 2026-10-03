package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestIsIPInCIDR(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		cidr     string
		expected bool
	}{
		// IPv4 测试
		{"IPv4 in CIDR", "192.168.1.100", "192.168.1.0/24", true}, //nolint:goconst
		{"IPv4 not in CIDR", "192.168.2.100", "192.168.1.0/24", false},
		{"IPv4 exact match", "192.168.1.1", "192.168.1.1/32", true}, //nolint:goconst
		{"IPv4 single IP mismatch", "192.168.1.2", "192.168.1.1/32", false},
		{"IPv4 large subnet", "10.0.0.50", "10.0.0.0/8", true}, //nolint:goconst
		{"IPv4 outside large subnet", "11.0.0.1", "10.0.0.0/8", false},

		// IPv6 测试
		{"IPv6 in CIDR", "2001:db8::1", "2001:db8::/32", true}, //nolint:goconst
		{"IPv6 not in CIDR", "2001:db9::1", "2001:db8::/32", false},
		{"IPv6 exact match", "2001:db8::1", "2001:db8::1/128", true},
		{"IPv6 loopback", "::1", "::1/128", true},
		{"IPv6 full", "fe80::1", "fe80::/10", true},

		// IPv4-mapped IPv6
		{"IPv4-mapped IPv6 in IPv4 CIDR", "::ffff:192.168.1.100", "192.168.1.0/24", true},

		// 边缘情况
		{"empty IP", "", "192.168.1.0/24", false},
		{"empty CIDR", "192.168.1.1", "", false},
		{"invalid IP", "invalid", "192.168.1.0/24", false}, //nolint:goconst
		{"invalid CIDR", "192.168.1.1", "invalid", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isIPInCIDR(tt.ip, tt.cidr)
			if result != tt.expected {
				t.Errorf("isIPInCIDR(%q, %q) = %v, expected %v", tt.ip, tt.cidr, result, tt.expected)
			}
		})
	}
}

func TestIsValidIP(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected bool
	}{
		{"valid IPv4", "192.168.1.1", true},
		{"valid IPv6", "2001:db8::1", true},
		{"valid IPv6 full", "fe80::1%eth0", false}, // 带 zone ID 的无效格式
		{"invalid IP", "invalid", false},
		{"empty IP", "", false},
		{"IPv4 with port", "192.168.1.1:8080", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidIP(tt.ip)
			if result != tt.expected {
				t.Errorf("isValidIP(%q) = %v, expected %v", tt.ip, result, tt.expected)
			}
		})
	}
}

func TestGetIPVersion(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected int
	}{
		{"IPv4", "192.168.1.1", 4},
		{"IPv6", "2001:db8::1", 6},
		{"IPv6 loopback", "::1", 6},
		{"invalid", "invalid", 0},
		{"empty", "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getIPVersion(tt.ip)
			if result != tt.expected {
				t.Errorf("getIPVersion(%q) = %d, expected %d", tt.ip, result, tt.expected)
			}
		})
	}
}

func TestNormalizeIP(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected string
	}{
		{"IPv4", "192.168.1.1", "192.168.1.1"},
		{"IPv6", "2001:db8::1", "2001:db8::1"},
		{"IPv4-mapped IPv6", "::ffff:192.168.1.1", "192.168.1.1"},
		{"invalid", "invalid", "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeIP(tt.ip)
			if result != tt.expected {
				t.Errorf("normalizeIP(%q) = %q, expected %q", tt.ip, result, tt.expected)
			}
		})
	}
}

func TestIPWhitelistMiddleware(t *testing.T) {
	// 测试白名单中间件的基本功能
	t.Run("empty whitelist allows all", func(t *testing.T) {
		middleware := IPWhitelistMiddleware([]string{})
		if middleware == nil {
			t.Error("expected middleware to be created")
		}
	})

	t.Run("whitelist with IPs", func(t *testing.T) {
		middleware := IPWhitelistMiddleware([]string{"192.168.1.1", "10.0.0.0/8"})
		if middleware == nil {
			t.Error("expected middleware to be created")
		}
	})
}

func TestCORSMiddleware(t *testing.T) {
	t.Run("returns gin handler for empty origins", func(t *testing.T) {
		handler := CORSMiddleware(nil)
		assert.NotNil(t, handler)
	})

	t.Run("returns gin handler for specific origins", func(t *testing.T) {
		handler := CORSMiddleware([]string{"https://example.com"})
		assert.NotNil(t, handler)
	})

	t.Run("accepts wildcard origin without panic", func(t *testing.T) {
		// Passing ["*"] should be normalized internally and not panic.
		assert.NotPanics(t, func() {
			handler := CORSMiddleware([]string{"*"})
			assert.NotNil(t, handler)
		})
	})

	t.Run("sets CORS allow-origin header for allowed origin", func(t *testing.T) {
		router := gin.New()
		router.Use(CORSMiddleware([]string{"https://app.example.com"}))
		router.GET("/ping", func(c *gin.Context) {
			c.String(http.StatusOK, "pong")
		})

		// The request host differs from the Origin so it is treated as a CORS request.
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ping", nil) //nolint:noctx
		req.Host = "api.server.local"
		req.Header.Set("Origin", "https://app.example.com")
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "https://app.example.com", w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("rejects disallowed origin with 403", func(t *testing.T) {
		router := gin.New()
		router.Use(CORSMiddleware([]string{"https://app.example.com"}))
		router.GET("/ping", func(c *gin.Context) {
			c.String(http.StatusOK, "pong")
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ping", nil) //nolint:noctx
		req.Host = "api.server.local"
		req.Header.Set("Origin", "https://evil.example.com")
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	})
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	t.Run("sets standard security headers", func(t *testing.T) {
		router := gin.New()
		router.Use(SecurityHeadersMiddleware())
		router.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx
		router.ServeHTTP(w, req)

		assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
		assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
		assert.Equal(t, "0", w.Header().Get("X-XSS-Protection"))
		assert.NotEmpty(t, w.Header().Get("Content-Security-Policy"))
		assert.NotEmpty(t, w.Header().Get("Referrer-Policy"))
		assert.NotEmpty(t, w.Header().Get("Permissions-Policy"))
	})

	t.Run("sets cross-origin isolation headers", func(t *testing.T) {
		router := gin.New()
		router.Use(SecurityHeadersMiddleware())
		router.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx
		router.ServeHTTP(w, req)

		assert.Equal(t, "same-origin", w.Header().Get("Cross-Origin-Opener-Policy"))
		assert.Equal(t, "same-origin", w.Header().Get("Cross-Origin-Resource-Policy"))
	})

	t.Run("does not set HSTS over plain HTTP", func(t *testing.T) {
		router := gin.New()
		router.Use(SecurityHeadersMiddleware())
		router.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx
		router.ServeHTTP(w, req)

		assert.Empty(t, w.Header().Get("Strict-Transport-Security"))
	})
}

func TestSecurityHeadersMediaEmbedding(t *testing.T) {
	for _, tc := range []struct{ path, policy string }{
		{"/media/1/original/photo.png", "cross-origin"},
		{"/_matrix/client/v1/media/download/remote.test/file", "cross-origin"},
		{"/_matrix/client/v1/media/thumbnail/remote.test/file", "cross-origin"},
		{"/_matrix/client/v1/media/upload", "same-origin"},
		{"/_matrix/client/v1/media/download-private/file", "same-origin"},
		{"/_matrix/media/v3/download/remote.test/file", "same-origin"},
		{"/_matrix/federation/v1/media/download/file", "same-origin"},
		{"/_matrix/client/v3/sync", "same-origin"},
		{"/index.html", "same-origin"},
		{"/api/v1/accounts/verify_credentials", "same-origin"},
		{"/media-private", "same-origin"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := gin.New()
			r.Use(SecurityHeadersMiddleware())
			r.GET(tc.path, func(c *gin.Context) { c.Status(http.StatusOK) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			assert.Equal(t, tc.policy, w.Header().Get("Cross-Origin-Resource-Policy"))
			assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
		})
	}
}
