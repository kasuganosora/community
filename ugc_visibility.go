package community

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/moderation"
)

// ugcOpenPaths stay available to logged-out visitors in every mode: they
// carry no user-generated posts or profiles.
var ugcOpenPaths = map[string]bool{
	"/api/v1/custom_emojis":     true,
	"/api/v1/instance/peers":    true,
	"/api/v1/instance/activity": true,
	"/api/v1/announcements":     true,
	"/api/v1/trends/tags":       true,
	"/api/v1/tags/:id":          true,
}

// ugcRemoteStreams are streams that would carry remote posts.
var ugcRemoteStreams = map[string]bool{
	"/streaming/public":         true,
	"/streaming/public/remote":  true,
	"/streaming/hashtag":        true,
	"/streaming/hashtag/remote": true,
}

const ugcRestrictedMessage = "This content is restricted by the server for visitors. Please sign in."

// UGCVisitorGuard enforces Misskey's ugcVisibilityForVisitor on the public
// (optionally authenticated) API for logged-out requests:
//
//   - "none": user-generated content endpoints answer 401
//     (Misskey contentRestrictedByServer);
//   - "local": public timelines are forced to local posts, remote streams
//     are refused, and JSON responses are filtered so remote statuses and
//     accounts are dropped (a single remote status or account is a 404);
//   - "all": no change.
//
// Signed-in users are never affected.
func UGCVisitorGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if controllers.GetAccount(c) != nil || ugcOpenPaths[c.FullPath()] {
			c.Next()
			return
		}
		mode := moderation.Current().UGCVisibilityForVisitor
		if mode == moderation.UGCAll {
			c.Next()
			return
		}
		path := c.FullPath()
		if mode == moderation.UGCNone {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": ugcRestrictedMessage, "code": "CONTENT_RESTRICTED_BY_SERVER"})
			return
		}
		// local
		trimmed := strings.TrimPrefix(strings.TrimPrefix(path, "/api/v1"), "/api/v2")
		if strings.HasPrefix(trimmed, "/streaming") {
			if ugcRemoteStreams[trimmed] {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": ugcRestrictedMessage, "code": "CONTENT_RESTRICTED_BY_SERVER"})
				return
			}
			c.Next()
			return
		}
		if trimmed == "/timelines/public" || trimmed == "/timelines/tag/:tag" || trimmed == "/directory" {
			q := c.Request.URL.Query()
			q.Set("local", "true")
			q.Del("remote")
			c.Request.URL.RawQuery = q.Encode()
		}
		w := &bufferedWriter{ResponseWriter: c.Writer}
		c.Writer = w
		c.Next()
		c.Writer = w.ResponseWriter
		w.flush(c)
	}
}

type bufferedWriter struct {
	gin.ResponseWriter
	buf    bytes.Buffer
	status int
}

func (w *bufferedWriter) WriteHeader(code int) { w.status = code }
func (w *bufferedWriter) WriteHeaderNow()      {}
func (w *bufferedWriter) Write(b []byte) (int, error) {
	return w.buf.Write(b)
}
func (w *bufferedWriter) WriteString(s string) (int, error) { return w.buf.WriteString(s) }
func (w *bufferedWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
func (w *bufferedWriter) Written() bool { return w.buf.Len() > 0 || w.status != 0 }
func (w *bufferedWriter) Size() int     { return w.buf.Len() }

func (w *bufferedWriter) flush(c *gin.Context) {
	status := w.Status()
	body := w.buf.Bytes()
	if status == http.StatusOK && strings.Contains(w.Header().Get("Content-Type"), "json") {
		if filtered, gone := filterRemoteUGC(body); gone {
			w.Header().Del("Content-Length")
			w.ResponseWriter.WriteHeader(http.StatusNotFound)
			_, _ = w.ResponseWriter.Write([]byte(`{"error":"Record not found"}`))
			return
		} else if filtered != nil {
			body = filtered
		}
	}
	w.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(status)
	_, _ = w.ResponseWriter.Write(body)
}

// filterRemoteUGC drops remote statuses and accounts from a Mastodon API
// JSON body. It returns gone=true when the body itself is a remote status
// or account, and nil when nothing changed.
func filterRemoteUGC(body []byte) (out []byte, gone bool) {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return nil, false
	}
	changed := false
	switch t := v.(type) {
	case []any:
		v, changed = filterList(t)
	case map[string]any:
		if isRemoteUGC(t) {
			return nil, true
		}
		for _, key := range []string{"ancestors", "descendants", "accounts", "statuses"} {
			if list, ok := t[key].([]any); ok {
				next, ch := filterList(list)
				if ch {
					t[key], changed = next, true
				}
			}
		}
	}
	if !changed {
		return nil, false
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	return b, false
}

func filterList(list []any) ([]any, bool) {
	out := make([]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok && isRemoteUGC(m) {
			continue
		}
		out = append(out, item)
	}
	return out, len(out) != len(list)
}

// isRemoteUGC recognises a serialized remote account (acct contains "@")
// or a status by / boosting a remote account.
func isRemoteUGC(m map[string]any) bool {
	if acct, ok := m["acct"].(string); ok {
		_, hasUsername := m["username"]
		return hasUsername && strings.Contains(acct, "@")
	}
	if account, ok := m["account"].(map[string]any); ok {
		if _, isStatus := m["content"]; isStatus {
			if isRemoteUGC(account) {
				return true
			}
			if reblog, ok := m["reblog"].(map[string]any); ok && isRemoteUGC(reblog) {
				return true
			}
		}
	}
	return false
}

// RobotsTxt serves /robots.txt. Like Misskey, when visitors may not see
// user content ("none"), crawlers are asked to skip profiles and posts.
func RobotsTxt(c *gin.Context) {
	body := "User-agent: *\nDisallow: /admin\nDisallow: /settings\n"
	if moderation.Current().UGCVisibilityForVisitor == moderation.UGCNone {
		body += "Disallow: /@\nDisallow: /notes\n"
	}
	c.Header("Cache-Control", "public, max-age=300")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(body))
}

// VisitorContentPolicy serves GET /api/v1/instance/visitor_policy so the
// web client can explain restricted content to logged-out visitors.
func VisitorContentPolicy(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ugc_visibility_for_visitor": moderation.Current().UGCVisibilityForVisitor})
}
