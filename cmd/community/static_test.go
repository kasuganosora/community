package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
}
