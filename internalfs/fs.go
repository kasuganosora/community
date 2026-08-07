// Package internalfs provides a unified filesystem abstraction over the
// application's bundled static resources (mail templates, i18n files, etc.).
//
// In development mode (config.C.DevMode == "true") the resources are read
// live from disk (the "resources/" directory next to the executable) so that
// templates can be edited without recompiling. In production the resources are
// embedded directly into the binary via go:embed, producing a self-contained,
// read-only and tamper-resistant filesystem.
//
// All paths are treated as relative to the FS root ("mails/foo.html",
// "i18n/en.toml", ...). The SafeOpen helper additionally sanitises the path to
// defend against path-traversal attempts (e.g. "mails/../../etc/passwd").
package internalfs

import (
	"embed"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/utils"
)

// embedRoot embeds the bundled resources into the binary at compile time. The
// directive resolves relative to this source file, so the resources live under
// internalfs/resources/ — a mirror of the top-level "resources/" directory
// (i18n/, mails/, oauth_authorize.html, ...). "all:" also captures files that
// would otherwise be excluded by Go's default rules (names starting with "." or
// "_").
//
//go:embed all:resources
var embedRoot embed.FS

// embeddedFS is the resources subtree exposed to callers: it is sub-rooted at
// "resources" so that the virtual paths ("mails/foo.html", "i18n/en.toml", ...)
// match exactly those used by the dev-mode os.DirFS. It is resolved once at
// init time; a failure here is a compile/build defect, so we panic early and
// loudly rather than silently serving an empty filesystem.
var embeddedFS fs.FS

func init() {
	sub, err := fs.Sub(embedRoot, "resources")
	if err != nil {
		// fs.Sub only errors for an invalid name; "resources" is a constant,
		// so this is effectively unreachable. Panic to fail fast if it ever
		// happens due to a refactor.
		panic("internalfs: cannot sub embedded resources: " + err.Error())
	}
	embeddedFS = sub
}

// Get returns the filesystem that backs the application's bundled resources.
//
// In development mode it returns a live filesystem rooted at the "resources/"
// directory on disk (so files can be edited without recompiling). In every
// other mode it returns the compile-time embedded copy, which is read-only and
// immune to on-disk tampering.
func Get() fs.FS {
	if config.C.DevMode == "true" { //nolint:goconst
		return os.DirFS(utils.GetFullPath("resources"))
	}

	return embeddedFS
}

// SafeOpen opens the named file within the bundled resources after sanitising
// the path to guarantee it cannot escape the filesystem root.
//
// It normalises the path (resolving ".", ".." and duplicate separators) and
// rejects any path that, after cleaning, is absolute, starts with "..", or
// would resolve outside the FS root. This is defence in depth: embed.FS and
// os.DirFS already reject ".." at open time, but applying an explicit,
// auditable check here makes the security contract explicit and protects any
// future backing implementation.
//
// The returned file must be closed by the caller.
func SafeOpen(fsys fs.FS, name string) (fs.File, error) {
	clean, err := SecureJoin(name)
	if err != nil {
		return nil, err
	}

	return fsys.Open(clean)
}

// SecureJoin cleans an untrusted, FS-root-relative path and verifies it stays
// within the root. It is the path-traversal-safe variant of filepath.Join for
// virtual filesystem paths.
//
// The input is interpreted relative to the FS root (forward slashes on all
// platforms, as required by io/fs). It returns the cleaned path and an error
// if the path attempts to escape the root.
func SecureJoin(name string) (string, error) {
	if name == "" {
		return "", &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	// io/fs uses forward slashes on every platform; normalise any backslashes
	// (e.g. from Windows-originated input) so they cannot smuggle a separator.
	name = strings.ReplaceAll(name, "\\", "/")

	// Reject absolute paths: an embed/os DirFS is rooted, so an absolute path
	// either means an injection attempt or a malformed request. We use path
	// (not filepath) because io/fs virtual paths always use forward slashes,
	// giving identical behaviour on every platform.
	if path.IsAbs(name) {
		return "", &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}

	// path.Clean resolves ".." and "." components lexically. Because the input
	// is relative and we already stripped leading slashes, any ".." that would
	// climb above the root survives as a leading ".." component.
	cleaned := path.Clean(name)

	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}

	// path.Clean turns "." into "."; treat it as the root, which has no file.
	if cleaned == "." {
		return "", &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	return cleaned, nil
}
