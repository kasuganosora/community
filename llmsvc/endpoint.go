package llmsvc

import (
	"net/url"
	"regexp"
	"strings"
)

// DefaultChatPath is the Chat Completions path used when a base URL carries
// no version segment and no explicit chat path is configured.
const DefaultChatPath = "/v1/chat/completions"

// versionSegment matches a trailing API version segment such as "v1", "v4"
// or "v1beta" (e.g. https://open.bigmodel.cn/api/paas/v4).
var versionSegment = regexp.MustCompile(`^v\d+([a-z]+\d*)?$`)

// baseHasVersion reports whether the last path segment of base is a version.
func baseHasVersion(base string) bool {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return false
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	return versionSegment.MatchString(strings.ToLower(segs[len(segs)-1]))
}

// ResolveChatPath returns the path (or absolute URL) handed to ThinkBot's
// openai.WithChatPath for an openai_compatible provider, mirroring ThinkBot's
// config.resolveChatPath:
//   - an explicit chatPath wins (a path relative to base, or a full http(s) URL);
//   - a base URL already ending in a version segment (/v1, /v4, /api/paas/v4)
//     gets "/chat/completions" so the version is not repeated;
//   - a base URL that already is a chat completions endpoint is used as is;
//   - otherwise DefaultChatPath.
func ResolveChatPath(base, chatPath string) string {
	chatPath = strings.TrimSpace(chatPath)
	if chatPath != "" {
		if isAbsoluteURL(chatPath) {
			return chatPath
		}
		return "/" + strings.TrimLeft(chatPath, "/")
	}
	trimmed := strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(strings.ToLower(trimmed), "/chat/completions") {
		return trimmed
	}
	if baseHasVersion(trimmed) {
		return "/chat/completions"
	}
	return DefaultChatPath
}

// ChatEndpoint returns the full URL a Chat Completions request is sent to
// (same joining rule as ThinkBot's util/http client). It contains no key.
func ChatEndpoint(base, chatPath string) string {
	path := ResolveChatPath(base, chatPath)
	if isAbsoluteURL(path) {
		return path
	}
	return strings.TrimRight(strings.TrimSpace(base), "/") + "/" + strings.TrimLeft(path, "/")
}

// Endpoint returns the request URL of p for display (connection test). Only
// openai_compatible providers have a resolvable chat endpoint; other types
// return their base URL (or "" when the library default is used).
func (p ProviderConfig) Endpoint() string {
	if p.Type == TypeOpenAICompatible {
		return redactURL(ChatEndpoint(redactURL(p.BaseURL), p.ChatPath))
	}
	return redactURL(p.BaseURL)
}

// redactURL drops userinfo and query strings, which could carry credentials.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || raw == "" {
		return ""
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

func isAbsoluteURL(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// validChatPath accepts "", a path starting with "/" or a full http(s) URL.
func validChatPath(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > 512 || strings.ContainsAny(s, " \t\r\n?#") {
		return false
	}
	if isAbsoluteURL(s) {
		u, err := url.Parse(s)
		return err == nil && u.Host != "" && u.User == nil
	}
	return strings.HasPrefix(s, "/") && !strings.Contains(s, "..")
}
