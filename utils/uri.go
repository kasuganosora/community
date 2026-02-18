package utils

import (
	"net/url"
	"strings"
	"time"
)

// ExtractDomainFromURI 从 URI 中提取域名
func ExtractDomainFromURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	return u.Host
}

// ExtractUsernameFromURI 从 ActivityPub URI 中提取用户名
// 支持格式: https://domain.com/users/username 或 https://domain.com/@username
func ExtractUsernameFromURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return ""
	}

	// 尝试匹配 /users/username 格式
	parts := strings.Split(u.Path, "/")
	for i, part := range parts {
		if part == "users" && i+1 < len(parts) {
			return parts[i+1]
		}
	}

	// 尝试匹配 /@username 格式
	if strings.HasPrefix(u.Path, "/@") {
		username := strings.TrimPrefix(u.Path, "/@")
		if idx := strings.Index(username, "/"); idx > 0 {
			username = username[:idx]
		}
		return username
	}

	return ""
}

// ExtractIDFromURI 从 URI 最后一段提取 ID
func ExtractIDFromURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.TrimSuffix(u.Path, "/"), "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

// ParseRFC3339 解析 RFC3339 格式的时间字符串，返回指针
func ParseRFC3339(timeStr string) *time.Time {
	if timeStr == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, timeStr)
	if err != nil {
		return nil
	}
	return &t
}

// ParseRFC3339WithFallback 解析 RFC3339 格式的时间字符串，失败时返回默认值
func ParseRFC3339WithFallback(timeStr string, fallback time.Time) time.Time {
	if timeStr == "" {
		return fallback
	}
	t, err := time.Parse(time.RFC3339, timeStr)
	if err != nil {
		return fallback
	}
	return t
}

// FormatRFC3339 格式化时间为 RFC3339 字符串
func FormatRFC3339(t time.Time) string {
	return t.Format(time.RFC3339)
}

// FormatRFC3339Ptr 格式化时间指针为 RFC3339 字符串，nil 返回空字符串
func FormatRFC3339Ptr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}
