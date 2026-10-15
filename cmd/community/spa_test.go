package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
)

func newSPATestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	for name, body := range map[string]string{"index.html": "<!doctype html><title>app</title>", "theme-init.js": "/*js*/", "favicon.ico": "ico"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=x"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := gin.New()
	app.GET("/.well-known/nodeinfo", activitypub.NodeInfo)
	app.GET("/api/v1/instance", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	app.NoRoute(spaFallback(dir, []byte("<!doctype html><title>app</title>")))

	return app
}

func TestSPAFallbackServesFrontendRoutes(t *testing.T) {
	app := newSPATestEngine(t)

	for _, p := range []string{
		"/", "/login", "/about", "/explore", "/notifications", "/settings", "/settings/data",
		"/groups", "/groups/", "/groups/!web_room:example.org", "/groups/%21opaque%2Froom%3Aexample.org?via=example.org",
		"/messages", "/messages/groups", "/messages/groups/!web_room:example.org",
		"/@alice", "/@alice@remote.example", "/@alice/status/123", "/@alice@mastodon.social/followers",
		"/tags/golang", "/lists/7", "/admin", "/admin/settings/storage", "/notes/abc123", "/public", "/public/local", "/federated", "/timeline/public",
	} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
			t.Errorf("%s: got %d %q, want SPA shell", p, w.Code, w.Header().Get("Content-Type"))
		}
	}

	for p, want := range map[string]string{"/theme-init.js": "/*js*/", "/favicon.ico": "ico"} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		if w.Code != http.StatusOK || w.Body.String() != want {
			t.Errorf("%s: got %d %q, want the built file", p, w.Code, w.Body.String())
		}
	}

	// Unknown pages render the app's not-found page with a real 404.
	for _, p := range []string{"/some/unknown-page", "/web/timelines", "/publicx", "/timeline/home", "/groups-other"} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		if w.Code != http.StatusNotFound || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
			t.Errorf("%s: got %d %q, want SPA shell with 404", p, w.Code, w.Header().Get("Content-Type"))
		}
	}

	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil))
	if w.Code != http.StatusOK {
		t.Errorf("registered API route: %d", w.Code)
	}
}

func TestSPAFallbackReturns404ForServerPaths(t *testing.T) {
	app := newSPATestEngine(t)

	for _, p := range []string{
		"/api/v1/nope", "/api", "/oauth/unknown", "/nodeinfo/2.0", "/nodeinfo/3.0", "/nodeinfo",
		"/.well-known/unknown", "/.well-known/security.txt", "/users/alice/unknown", "/ap/users/x/y",
		"/_matrix/client/v3/nope", "/_matrix", "/media/123/missing.jpg", "/emoji-assets/nope.png",
		"/assets/index-deadbeef.js", "/inbox", "/health/x",
		"/.env", "/.git/HEAD", "/.git/config", "/foo/.htaccess", "/.DS_Store",
		"/missing.js", "/style.css", "/wp-login.php", "/backup.sql", "/config.yml", "/robots.txt",
	} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: got %d %q, want 404", p, w.Code, w.Header().Get("Content-Type"))
		}
		if ct := w.Header().Get("Content-Type"); ct == "text/html; charset=utf-8" {
			t.Errorf("%s: must not return the HTML shell", p)
		}
	}

	// Non-GET requests never get the shell.
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/@alice", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("POST /@alice: got %d", w.Code)
	}
}

func TestFrontendAssetContentTypes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"manifest.webmanifest": "application/manifest+json",
		"logo.svg":             "image/svg+xml",
		"assets/app.js":        "text/javascript; charset=utf-8",
		"assets/worker.mjs":    "text/javascript; charset=utf-8",
		"assets/data.json":     "application/json",
		"assets/font.woff2":    "font/woff2",
	}
	for name := range want {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	app := gin.New()
	app.Static("/assets", filepath.Join(dir, "assets"))
	app.NoRoute(spaFallback(dir, []byte("<!doctype html>")))

	for name, typ := range want {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/"+name, nil))
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != typ {
			t.Errorf("/%s: code %d, Content-Type %q, want %q", name, w.Code, w.Header().Get("Content-Type"), typ)
		}
	}
}
