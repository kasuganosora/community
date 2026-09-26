package utils

import (
	"os"
	"path/filepath"
)

// ResolveAppPath resolves a runtime file or directory such as ".env",
// "resources" or "frontend/dist".
//
// Absolute paths are returned unchanged. Relative paths are looked up in, in
// order: $APP_ROOT, the current working directory, the directory of the
// running executable, and finally the source directory the binary was built
// from (so `go run` / `go test` from any directory keep working). The first
// existing candidate wins; if none exists the path under $APP_ROOT (or the
// working directory) is returned.
//
// Previously these paths were resolved against the compile-time source
// directory only, so a binary built in a Docker build stage (/src) never read
// /app/.env or served /app/frontend/dist at runtime.
func ResolveAppPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}

	candidates := appPathCandidates(path)
	for _, c := range candidates {
		if IsFileExists(c) {
			return c
		}
	}

	return candidates[0]
}

func appPathCandidates(path string) []string {
	var roots []string

	if root := os.Getenv("APP_ROOT"); root != "" {
		roots = append(roots, root)
	}

	if wd, err := os.Getwd(); err == nil {
		roots = append(roots, wd)
	}

	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}

		roots = append(roots, filepath.Dir(exe))
	}

	if sourceDir != "" {
		roots = append(roots, sourceDir)
	}

	if len(roots) == 0 {
		return []string{path}
	}

	candidates := make([]string, 0, len(roots))
	for _, r := range roots {
		candidates = append(candidates, filepath.Join(r, path))
	}

	return candidates
}
