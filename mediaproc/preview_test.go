package mediaproc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBindThumbnailUsesThePublicFileAndRefusesTheSharedName(t *testing.T) {
	dir := t.TempDir()
	generated := filepath.Join(dir, "thumb_frame.jpg")
	if err := os.WriteFile(generated, []byte("preview-a"), 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := BindThumbnail(dir, "173000_ab.webp", "thumb_frame.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if name != "thumb_173000_ab.jpg" {
		t.Fatalf("name = %s", name)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(generated, []byte("preview-b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BindThumbnail(dir, "frame.jpg", "thumb_frame.jpg"); err == nil {
		t.Fatal("shared thumbnail name was accepted")
	}
}
