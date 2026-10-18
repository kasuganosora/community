package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestOldBuildAssetsServedFromArchive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	archive := t.TempDir()
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()

	// Release 1 is archived.
	v1 := t.TempDir()
	write(filepath.Join(v1, "assets", "js", "chunk-old.js"), "OLD")
	if err := archiveFrontendAssets(filepath.Join(v1, "assets"), archive, now); err != nil {
		t.Fatal(err)
	}
	// Release 2 replaces the image; an archived file nobody shipped for a
	// long time is pruned.
	v2 := t.TempDir()
	write(filepath.Join(v2, "assets", "js", "chunk-new.js"), "NEW")
	write(filepath.Join(v2, "version.json"), `{"build":"2"}`)
	write(filepath.Join(archive, "js", "ancient.js"), "ANCIENT")
	old := now.Add(-assetArchiveRetention - time.Hour)
	_ = os.Chtimes(filepath.Join(archive, "js", "ancient.js"), old, old)
	if err := archiveFrontendAssets(filepath.Join(v2, "assets"), archive, now); err != nil {
		t.Fatal(err)
	}

	r := gin.New()
	h := frontendFileHandler(v2, archive)
	r.GET("/assets/*filepath", h)
	r.NoRoute(spaFallback(v2, []byte("<html>")))
	get := func(p string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		return w
	}
	for p, body := range map[string]string{"/assets/js/chunk-old.js": "OLD", "/assets/js/chunk-new.js": "NEW"} {
		w := get(p)
		if w.Code != 200 || w.Body.String() != body || w.Header().Get("Cache-Control") != cacheImmutable {
			t.Errorf("%s: %d %q cc=%q", p, w.Code, w.Body.String(), w.Header().Get("Cache-Control"))
		}
	}
	if w := get("/assets/js/ancient.js"); w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("pruned asset: %d cc=%q", w.Code, w.Header().Get("Cache-Control"))
	}
	if w := get("/assets/../version.json"); w.Code == 200 {
		t.Errorf("traversal served %q", w.Body.String())
	}
	if w := get("/version.json"); w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("version.json: %d cc=%q", w.Code, w.Header().Get("Cache-Control"))
	}
}
