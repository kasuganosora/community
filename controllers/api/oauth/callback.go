package oauth

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

const (
	oobURI     = "urn:ietf:wg:oauth:2.0:oob"
	oobAutoURI = "urn:ietf:wg:oauth:2.0:oob:auto"
)

func isOOB(uri string) bool {
	return uri == oobURI || uri == oobAutoURI
}

// isAllowedRedirect reports whether a redirect or callback URL may receive a code or token.
// https, loopback http, the two Mastodon OOB URNs, and private-use app schemes are allowed.
// javascript:, data:, file: and protocol-relative URLs are not.
func isAllowedRedirect(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n\t\\") {
		return false
	}

	if raw == oobURI || raw == oobAutoURI {
		return true
	}

	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.User != nil || u.Fragment != "" {
		return false
	}

	switch strings.ToLower(u.Scheme) {
	case "https":
		return u.Host != ""
	case "http":
		host := strings.ToLower(u.Hostname())

		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	case "javascript", "data", "file", "mailto", "tel", "vbscript", "blob", "about":
		return false
	default:
		// Native clients (Tusky, Ice Cubes, Milktea) register a private scheme.
		return u.Host != "" || u.Opaque != "" || u.Path != ""
	}
}

func splitRedirects(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == ' '
	})

	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if _, ok := seen[part]; ok {
			continue
		}

		seen[part] = struct{}{}
		out = append(out, part)
	}

	return out
}

func isValidRedirectUri(registeredUris, requestedUri string) bool {
	requestedUri = strings.TrimSpace(requestedUri)
	if requestedUri == "" || !isAllowedRedirect(requestedUri) {
		return false
	}

	for _, uri := range splitRedirects(registeredUris) {
		if uri == requestedUri {
			return true
		}
	}

	return false
}

func issuer() string {
	return strings.TrimRight(systemsettings.GetBaseURL(), "/")
}

// appendQuery sets query parameters the way Mastodon and Misskey clients read them:
// code/state/iss on the OAuth redirect, session on MiAuth, token on legacy Misskey.
func appendQuery(raw string, params map[string]string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}

	q := u.Query()
	for k, v := range params {
		if v == "" {
			continue
		}

		q.Set(k, v)
	}

	u.RawQuery = q.Encode()

	return u.String(), nil
}

func buildRedirectUrl(baseUri, code, state string) string {
	loc, err := appendQuery(baseUri, map[string]string{
		"code":  code,
		"state": state,
		"iss":   issuer(),
	})
	if err != nil {
		return baseUri
	}

	return loc
}

func buildDenyRedirectUrl(baseUri, state string) string {
	return buildDenyRedirect(baseUri, "access_denied", "The user denied the request", state)
}

func buildDenyRedirect(baseUri, code, description, state string) string {
	loc, err := appendQuery(baseUri, map[string]string{
		"error":             code,
		"error_description": description,
		"state":             state,
		"iss":               issuer(),
	})
	if err != nil {
		return baseUri
	}

	return loc
}

func loginReturnURL(c *gin.Context) string {
	path := c.Request.URL.RequestURI()
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, "/\\") {
		path = "/"
	}

	return "/login?redirect=" + url.QueryEscape(path)
}

func displayCallback(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || isOOB(raw) || !isAllowedRedirect(raw) {
		return ""
	}

	return raw
}

func scopeAllowed(granted models.ApplicationScope, requested string) bool {
	req := parseScopes(requested)
	// Apps created before scopes were stored are unrestricted, except admin.
	if granted.ReadScope == 0 && granted.WriteScope == 0 && granted.AdminScope == 0 && granted.OtherScope == 0 {
		return req.AdminScope == 0
	}

	return req.ReadScope&^granted.ReadScope == 0 &&
		req.WriteScope&^granted.WriteScope == 0 &&
		req.AdminScope&^granted.AdminScope == 0 &&
		req.OtherScope&^granted.OtherScope == 0
}

func normalizeScope(app *OAuthApplication, requested string) (string, bool) {
	requested = strings.Join(strings.Fields(strings.ReplaceAll(requested, ",", " ")), " ")
	if requested == "" {
		if app != nil {
			if s := app.Scopes.OAuthScopeString(); s != "" {
				return s, true
			}
		}

		return "read", true
	}

	// IndieAuth clients are not pre-registered. Never hand them admin.
	if app == nil {
		if parseScopes(requested).AdminScope != 0 {
			return "", false
		}

		return requested, true
	}

	if !scopeAllowed(app.Scopes, requested) {
		return "", false
	}

	return requested, true
}

func scopeStringSubset(granted, requested string) bool {
	return scopeAllowed(parseScopes(granted), requested)
}

func tokenHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))

	return hex.EncodeToString(sum[:])
}

func scopeLabels(scope string) []string {
	labels := map[string]string{
		"read":         "读取你的账户、时间线和通知",
		"write":        "以你的身份发帖、互动和修改资料",
		"follow":       "管理你的关注、屏蔽和静音",
		"push":         "为你订阅推送通知",
		"admin":        "管理这台服务器",
		"write:notes":  "发布和删除帖子",
		"read:account": "读取你的账户信息",
	}

	var out []string

	seen := map[string]struct{}{}

	for _, part := range strings.Fields(strings.ReplaceAll(scope, ",", " ")) {
		label, ok := labels[part]
		if !ok {
			switch {
			case strings.HasPrefix(part, "read:"):
				label = "读取：" + strings.TrimPrefix(part, "read:")
			case strings.HasPrefix(part, "write:"):
				label = "写入：" + strings.TrimPrefix(part, "write:")
			default:
				label = part
			}
		}

		if _, ok := seen[label]; ok {
			continue
		}

		seen[label] = struct{}{}
		out = append(out, label)
	}

	if len(out) == 0 {
		out = []string{"读取你的基本账户信息"}
	}

	return out
}
