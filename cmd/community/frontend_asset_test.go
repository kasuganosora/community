package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFrontendAsset(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "theme-init.js"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, ok := frontendAsset(dir, "/theme-init.js")
	if !ok || filepath.Base(got) != "theme-init.js" {
		t.Fatalf("theme-init.js: ok=%v path=%s", ok, got)
	}
	if _, ok := frontendAsset(dir, "/missing.js"); ok {
		t.Fatal("missing file should fall through")
	}
	if _, ok := frontendAsset(dir, "/../secret"); ok {
		t.Fatal("traversal should be rejected")
	}
	if _, ok := frontendAsset(dir, "/assets"); ok {
		t.Fatal("directory should fall through")
	}
}
