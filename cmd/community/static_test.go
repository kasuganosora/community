package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAcceptsEncoding(t *testing.T) {
	cases := []struct {
		header, token string
		want          bool
	}{
		{"gzip, deflate, br", "br", true},
		{"gzip", "br", false},
		{"br;q=0, gzip", "br", false},
		{"*", "br", true},
		{"*;q=0", "gzip", false},
		{"BR;q=0.5", "br", true},
		{"", "gzip", false},
	}
	for _, tc := range cases {
		if got := acceptsEncoding(tc.header, tc.token); got != tc.want {
			t.Errorf("acceptsEncoding(%q,%q)=%v want %v", tc.header, tc.token, got, tc.want)
		}
	}
}

func TestFrontendPrecompressedAndCacheHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	js := filepath.Join(dir, "assets", "js")
	if err := os.MkdirAll(js, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(js, "app-abc.js"), "RAW")
	write(filepath.Join(js, "app-abc.js.br"), "BR")
	write(filepath.Join(js, "app-abc.js.gz"), "GZ")
	write(filepath.Join(dir, "theme-init.js"), "THEME")
	write(filepath.Join(dir, "sw.js"), "SW")
	write(filepath.Join(dir, "version.json"), `{"build":"x"}`)

	r := gin.New()
	h := frontendFileHandler(dir)
	r.GET("/assets/*filepath", h)
	r.NoRoute(spaFallback(dir, []byte("<html>")))

	get := func(p, ae string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		if ae != "" {
			req.Header.Set("Accept-Encoding", ae)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		return w
	}

	for _, tc := range []struct{ ae, body, enc string }{
		{"gzip, deflate, br", "BR", "br"},
		{"gzip", "GZ", "gzip"},
		{"", "RAW", ""},
		{"br;q=0, identity", "RAW", ""},
	} {
		w := get("/assets/js/app-abc.js", tc.ae)
		if w.Code != 200 || w.Body.String() != tc.body || w.Header().Get("Content-Encoding") != tc.enc {
			t.Errorf("ae=%q: code=%d body=%q enc=%q", tc.ae, w.Code, w.Body.String(), w.Header().Get("Content-Encoding"))
		}
		if w.Header().Get("Vary") != "Accept-Encoding" {
			t.Errorf("ae=%q: missing Vary", tc.ae)
		}
		if w.Header().Get("Cache-Control") != cacheImmutable {
			t.Errorf("ae=%q: cache-control %q", tc.ae, w.Header().Get("Cache-Control"))
		}
		if ct := w.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
			t.Errorf("ae=%q: content-type %q", tc.ae, ct)
		}
	}

	if w := get("/assets/js/missing.js", "br"); w.Code != http.StatusNotFound || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("missing asset: %d cc=%q (a cached 404 outlives the next deploy)", w.Code, w.Header().Get("Cache-Control"))
	}
	if w := get("/theme-init.js", "br"); w.Body.String() != "THEME" || w.Header().Get("Cache-Control") != cacheRevalidate || w.Header().Get("Vary") != "" {
		t.Errorf("theme-init: body=%q cc=%q", w.Body.String(), w.Header().Get("Cache-Control"))
	}
	if w := get("/explore", "br"); w.Header().Get("Cache-Control") != "no-store" || w.Body.String() != "<html>" {
		t.Errorf("index: cc=%q", w.Header().Get("Cache-Control"))
	}
	// Update-controlling files must never be served from a CDN/HTTP cache.
	if w := get("/sw.js", ""); w.Body.String() != "SW" || w.Header().Get("Cache-Control") != "no-cache, no-store, must-revalidate" || w.Header().Get("Service-Worker-Allowed") != "/" {
		t.Errorf("sw.js: body=%q cc=%q swa=%q", w.Body.String(), w.Header().Get("Cache-Control"), w.Header().Get("Service-Worker-Allowed"))
	}
	if w := get("/version.json?t=1", ""); w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("version.json: cc=%q", w.Header().Get("Cache-Control"))
	}
}

func TestServiceWorkerKillSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sw.js"), []byte("// real worker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "version.json"), []byte(`{"build":"b1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.NoRoute(spaFallback(dir, []byte("<html></html>")))
	get := func(p string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		return w
	}

	if w := get("/sw.js"); w.Body.String() != "// real worker" {
		t.Fatalf("switch off: got %q", w.Body.String())
	}
	if w := get("/version.json"); w.Header().Get(swHeader) != "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("switch off: version headers %v", w.Header())
	}

	t.Setenv("FRONTEND_SERVICE_WORKER", "off")
	w := get("/sw.js")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "unregister()") || w.Header().Get("Cache-Control") != "no-cache, no-store, must-revalidate" {
		t.Fatalf("kill switch: %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	if w := get("/version.json"); w.Header().Get(swHeader) != "off" {
		t.Fatalf("kill switch: version header missing: %v", w.Header())
	}
}
