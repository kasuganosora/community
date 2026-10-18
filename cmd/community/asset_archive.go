package main

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/utils"
)

// Hashed frontend assets of previous releases are kept in an archive on a
// persistent volume so a tab opened before a deploy can still load its lazy
// chunks afterwards (the new image only contains the new build). Files are
// immutable by name, so serving an old one is always correct.
const assetArchiveRetention = 14 * 24 * time.Hour

// assetArchiveDir returns the archive directory, or "" when disabled
// (FRONTEND_ASSET_ARCHIVE_DIR=off).
func assetArchiveDir() string {
	dir := strings.TrimSpace(os.Getenv("FRONTEND_ASSET_ARCHIVE_DIR"))
	if strings.EqualFold(dir, "off") {
		return ""
	}
	if dir == "" {
		dir = utils.ResolveAppPath("uploads/.frontend-asset-archive")
	}
	return dir
}

// archiveFrontendAssets copies dist/assets into archive (refreshing the
// mtime of files the current build still ships) and removes archived files
// no build has shipped for the retention period.
func archiveFrontendAssets(assetsDir, archive string, now time.Time) error {
	if archive == "" {
		return nil
	}
	err := filepath.WalkDir(assetsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(assetsDir, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(archive, rel)
		if _, statErr := os.Stat(dst); statErr != nil {
			if err := copyFile(p, dst); err != nil {
				return err
			}
		}
		return os.Chtimes(dst, now, now)
	})
	if err != nil {
		return err
	}
	return filepath.WalkDir(archive, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if info, infoErr := d.Info(); infoErr == nil && now.Sub(info.ModTime()) > assetArchiveRetention {
			_ = os.Remove(p)
		}
		return nil
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src) //nolint:gosec // G304: walking our own dist directory
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp) //nolint:gosec // G304: below the archive directory
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// setupAssetArchive archives the current build and returns the directory to
// fall back to for missing /assets files ("" when unavailable).
func setupAssetArchive(frontendDir string) string {
	archive := assetArchiveDir()
	if archive == "" {
		return ""
	}
	if err := archiveFrontendAssets(filepath.Join(frontendDir, "assets"), archive, time.Now()); err != nil {
		log.Warn("Frontend asset archive %s unavailable: %v", archive, err)
		return ""
	}
	return archive
}
