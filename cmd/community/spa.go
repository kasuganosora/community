package main

import (
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// nonSPAPrefixes are server-side namespaces (APIs, federation, Matrix,
// media, health). An unknown path below them is a real 404, never the
// frontend shell: remote software parsing the HTML as JSON breaks (e.g.
// Misskey's "Unexpected token '<'" on a missing /nodeinfo/2.0).
var nonSPAPrefixes = []string{
	"/api/", "/oauth/", "/miauth/", "/auth/",
	"/.well-known/", "/nodeinfo/", "/users/", "/ap/", "/actor/", "/inbox", "/outbox",
	"/_matrix/", "/_synapse/",
	"/media/", "/emoji-assets/", "/card-images/", "/system/", "/assets/", "/proxy/",
	"/health", "/live", "/ready", "/metrics", "/admin/logs/",
}

// nonSPAExact are exact server-side paths without a trailing segment.
var nonSPAExact = map[string]bool{
	"/api": true, "/oauth": true, "/nodeinfo": true, "/actor": true, "/media": true, "/users": true,
	"/_matrix": true, "/emoji-assets": true, "/card-images": true, "/assets": true,
}

// staticExtensions mark requests for files: a missing one must not be
// answered with index.html (browsers would execute/parse HTML as JS/CSS and
// scanners would get a 200).
var staticExtensions = map[string]bool{
	".js": true, ".mjs": true, ".css": true, ".map": true, ".json": true, ".xml": true, ".txt": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".ico": true, ".webp": true, ".avif": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".webmanifest": true,
	".php": true, ".asp": true, ".aspx": true, ".jsp": true, ".cgi": true, ".env": true,
	".sql": true, ".bak": true, ".old": true, ".zip": true, ".gz": true, ".tar": true, ".rar": true, ".7z": true,
	".yml": true, ".yaml": true, ".ini": true, ".conf": true, ".config": true, ".log": true,
}

// isSPARoute reports whether an unmatched request may be answered with the
// frontend's index.html. Real frontend routes (/, /@user, /@user@domain,
// /@user/status/1, /tags/x, /admin/..., /settings, /notes/...) pass.
func isSPARoute(method, p string) bool {
	if method != http.MethodGet && method != http.MethodHead {
		return false
	}

	if nonSPAExact[strings.TrimRight(p, "/")] {
		return false
	}

	for _, prefix := range nonSPAPrefixes {
		if strings.HasPrefix(p, prefix) {
			return false
		}
	}

	if hasDotSegment(p) {
		return false
	}

	segments := strings.Split(strings.Trim(p, "/"), "/")
	last := segments[len(segments)-1]
	// Handles like @user@example.social contain dots but are routes.
	if !strings.HasPrefix(last, "@") && staticExtensions[strings.ToLower(path.Ext(last))] {
		return false
	}

	return true
}

// spaSections are the first path segments the Vue router knows (see
// frontend/src/shared/router/routes.ts); /@user... routes are matched
// separately. /public, /public/local, /federated and /timeline/public are
// web aliases for the explore timelines.
var spaSections = map[string]bool{
	"": true, "login": true, "register": true, "about": true, "announcements": true,
	"settings": true, "explore": true, "notifications": true, "messages": true, "groups": true,
	"bookmarks": true, "search": true, "lists": true, "tags": true, "admin": true,
	"public": true, "notes": true, "federated": true,
}

// spaPaths are single frontend routes whose first segment is not a section.
var spaPaths = map[string]bool{"/timeline/public": true}

// isKnownSPAPage reports whether the frontend has a page for p. Other paths
// still get the shell (so the app renders its own not-found page) but with
// a 404 status instead of a soft 404.
func isKnownSPAPage(p string) bool {
	first, _, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	return strings.HasPrefix(first, "@") || spaSections[first] || spaPaths[strings.TrimSuffix(p, "/")]
}

// spaFallback serves built files, the SPA shell for frontend routes and a
// JSON 404 for everything else.
// frontendMIMETypes pins Content-Types for frontend assets. Go falls back to
// the host's mime.types, which lacks .webmanifest (served as text/plain) and
// varies across images for the rest.
var frontendMIMETypes = map[string]string{
	".webmanifest": "application/manifest+json",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".json":        "application/json",
	".svg":         "image/svg+xml",
	".woff2":       "font/woff2",
	".woff":        "font/woff",
	".css":         "text/css; charset=utf-8",
	".wasm":        "application/wasm",
	".png":         "image/png",
	".ico":         "image/x-icon",
}

func init() {
	for ext, typ := range frontendMIMETypes {
		if err := mime.AddExtensionType(ext, typ); err != nil {
			panic(err)
		}
	}
}

func spaFallback(frontendDir string, indexHTML []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path

		if !hasDotSegment(p) && (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) {
			if file, ok := frontendAsset(frontendDir, p); ok {
				c.File(file)

				return
			}
		}

		if !isSPARoute(c.Request.Method, p) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})

			return
		}

		code := http.StatusOK
		if !isKnownSPAPage(p) {
			code = http.StatusNotFound
		}

		c.Data(code, "text/html; charset=utf-8", indexHTML)
	}
}

// hasDotSegment reports dotfile probes: /.env, /.git/HEAD, /x/.htaccess.
func hasDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}

	return false
}
