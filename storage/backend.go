package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Object keys
//
// Every stored file is addressed by a storage KEY: a slash separated,
// relative object path such as "media/2026/10/09/1760000000_ab.jpg" or
// "emojis/<uuid>.png". The same key is used for the local backend (file
// uploads/<key>) and the S3 backend (object <key> in S3_BUCKET). Database rows
// persist keys (or, for media attachments, the part of the key below
// "media/"), never a full bucket URL or a host path; public URLs are derived
// at read time from configuration.

var (
	// ErrNotFound is returned by Open/Stat when the object does not exist.
	ErrNotFound = errors.New("storage: object not found")
	// ErrInvalidKey is returned for keys that are empty, absolute or escape
	// the storage root.
	ErrInvalidKey = errors.New("storage: invalid object key")
)

// ObjectInfo describes a stored object.
type ObjectInfo struct {
	// Key is set by List; Open and Stat leave it empty.
	Key         string
	Size        int64
	ContentType string
	ModTime     time.Time
}

// Backend is the storage abstraction every upload, download and delete goes
// through. Implementations: LocalBackend (disk under uploads/) and S3Backend
// (S3 compatible object storage such as AWS S3, Cloudflare R2 or MinIO).
type Backend interface {
	// Kind is "local" or "s3".
	Kind() string
	// Put stores r under key. size may be -1 when unknown.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// Open returns the object's content. The reader also implements
	// io.Seeker for both built-in backends. Missing objects yield ErrNotFound.
	Open(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error)
	// Stat returns object metadata or ErrNotFound.
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	// Delete removes the object. Deleting a missing object is not an error.
	Delete(ctx context.Context, key string) error
	// PublicURL returns a URL that serves the object directly (CDN / public
	// bucket), or "" when the object must be served through the application.
	PublicURL(key string) string
	// Check verifies that the backend is reachable and usable.
	Check(ctx context.Context) error
	// List calls fn for every stored object whose key starts with prefix
	// ("" lists everything). Returning ErrStopList from fn stops the walk
	// without error; any other error aborts it and is returned.
	List(ctx context.Context, prefix string, fn func(ObjectInfo) error) error
}

// ErrStopList can be returned by a List callback to stop listing early.
var ErrStopList = errors.New("storage: stop listing")

// listPrefixDir returns the directory part of a key prefix ("media/2026/0"
// -> "media/2026"), validating it like a key.
func listPrefixDir(prefix string) (string, error) {
	prefix = strings.TrimLeft(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return "", nil
	}
	if strings.ContainsAny(prefix, "\\\x00") {
		return "", ErrInvalidKey
	}
	for seg := range strings.SplitSeq(prefix, "/") {
		if seg == ".." {
			return "", ErrInvalidKey
		}
	}
	dir := prefix
	if i := strings.LastIndex(dir, "/"); i >= 0 {
		dir = dir[:i]
	} else {
		dir = ""
	}
	return dir, nil
}

// CleanKey validates and canonicalises an object key.
func CleanKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, "\\\x00") || strings.HasPrefix(key, "/") {
		return "", ErrInvalidKey
	}

	for seg := range strings.SplitSeq(key, "/") {
		if seg == ".." {
			return "", ErrInvalidKey
		}
	}

	cleaned := path.Clean(key)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", ErrInvalidKey
	}

	return cleaned, nil
}

// NormalizeKey turns a legacy stored reference into an object key. It
// accepts a plain key ("media/a.jpg"), a host path relative to the working
// directory ("uploads/media/a.jpg", "/uploads/media/a.jpg") and the full
// bucket URLs older releases persisted in S3 mode
// ("https://endpoint/bucket/media/a.jpg" or "<S3_PUBLIC_URL>/media/a.jpg").
// The second result reports whether raw was a URL.
func NormalizeKey(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	isURL := false

	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		isURL = true

		u, err := url.Parse(raw)
		if err != nil {
			return "", true
		}

		p := strings.TrimPrefix(u.Path, "/")
		cfg := currentS3Config()

		if base := strings.TrimRight(cfg.PublicURL, "/"); base != "" && strings.HasPrefix(raw, base+"/") {
			p = strings.TrimPrefix(strings.SplitN(strings.TrimPrefix(raw, base+"/"), "?", 2)[0], "/")
			if unescaped, err := url.PathUnescape(p); err == nil {
				p = unescaped
			}
		}

		// Path-style bucket URLs (https://endpoint/<bucket>/<key>) and the
		// "<S3_PUBLIC_URL>/<bucket>/<key>" form older releases produced.
		if cfg.Bucket != "" && strings.HasPrefix(p, cfg.Bucket+"/") {
			p = strings.TrimPrefix(p, cfg.Bucket+"/")
		} else if first, rest, ok := strings.Cut(p, "/"); ok && !knownNamespaces[first] {
			// Bucket name unknown (e.g. the bucket was renamed): drop a
			// leading path-style bucket segment in front of a known namespace.
			if second, _, _ := strings.Cut(rest, "/"); knownNamespaces[second] {
				p = rest
			}
		}

		raw = p
	}

	raw = strings.TrimPrefix(filepath.ToSlash(raw), "/")
	raw = strings.TrimPrefix(raw, "uploads/")

	return raw, isURL
}

// knownNamespaces are the top-level key prefixes the application writes.
var knownNamespaces = map[string]bool{"media": true, "matrix": true, "emojis": true, "avatars": true, "public": true, "refreshed": true}

// ---------------------------------------------------------------------------
// Default backend selection

var (
	defaultMu      sync.Mutex
	defaultBackend Backend
)

// Default returns the configured backend (S3 when STORAGE_TYPE is s3/minio,
// otherwise the local uploads/ directory). The backend is created lazily
// from configuration on first use.
func Default() Backend {
	defaultMu.Lock()
	defer defaultMu.Unlock()

	if defaultBackend == nil {
		if isCloudStorageEnabled() {
			defaultBackend = NewS3Backend(LoadS3Config())
		} else {
			defaultBackend = &LocalBackend{Root: "uploads"}
		}
	}

	return defaultBackend
}

// ResetDefault drops the cached backend so the next Default call reads the
// current configuration. Admin saves use this so object storage changes
// apply without a process restart.
func ResetDefault() {
	defaultMu.Lock()
	defaultBackend = nil
	defaultMu.Unlock()
	resetHealthCache()
}

// SetDefault replaces the default backend (tests, tooling) and returns a
// function restoring the previous one. Passing nil re-reads configuration
// on next use.
func SetDefault(b Backend) (restore func()) {
	defaultMu.Lock()
	prev := defaultBackend
	defaultBackend = b
	defaultMu.Unlock()
	resetHealthCache()

	return func() {
		defaultMu.Lock()
		defaultBackend = prev
		defaultMu.Unlock()
		resetHealthCache()
	}
}

// IsRemote reports whether the default backend is object storage.
func IsRemote() bool { return Default().Kind() == KindS3 }

const (
	// KindLocal is the Kind of LocalBackend.
	KindLocal = "local"
	// KindS3 is the Kind of S3Backend.
	KindS3 = "s3"
)

// ---------------------------------------------------------------------------
// Local backend

// LocalBackend stores objects as files below Root.
type LocalBackend struct {
	Root string
}

// Kind implements Backend.
func (l *LocalBackend) Kind() string { return KindLocal }

func (l *LocalBackend) resolve(key string) (string, error) {
	k, err := CleanKey(key)
	if err != nil {
		return "", fmt.Errorf("%w: potential path traversal detected", err)
	}

	root := filepath.Clean(l.Root)
	full := filepath.Clean(filepath.Join(root, filepath.FromSlash(k)))

	if !isPathInsideRoot(full, root, string(filepath.Separator)) || full == root {
		return "", fmt.Errorf("%w: potential path traversal detected", ErrInvalidKey)
	}

	return full, nil
}

// Path returns the host path of key (local backend only).
func (l *LocalBackend) Path(key string) (string, error) { return l.resolve(key) }

// Put implements Backend. The file is written atomically via rename.
func (l *LocalBackend) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	full, err := l.resolve(key)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil { //nolint:gosec // G301: upload directories are world readable by design
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(full), ".upload-*")
	if err != nil {
		return err
	}

	tmpName := tmp.Name()

	if _, err = io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)

		return err
	}

	if err = tmp.Close(); err != nil {
		_ = os.Remove(tmpName)

		return err
	}

	_ = os.Chmod(tmpName, 0o644) //nolint:gosec // G302: uploaded media is public

	if err = os.Rename(tmpName, full); err != nil {
		_ = os.Remove(tmpName)

		return err
	}

	return nil
}

// Open implements Backend.
func (l *LocalBackend) Open(_ context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	full, err := l.resolve(key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}

	f, err := os.Open(full) //nolint:gosec // G304: path validated by resolve
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ObjectInfo{}, ErrNotFound
		}

		return nil, ObjectInfo{}, err
	}

	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		_ = f.Close()

		return nil, ObjectInfo{}, ErrNotFound
	}

	return f, ObjectInfo{Size: st.Size(), ModTime: st.ModTime(), ContentType: getContentType(full)}, nil
}

// Stat implements Backend.
func (l *LocalBackend) Stat(_ context.Context, key string) (ObjectInfo, error) {
	full, err := l.resolve(key)
	if err != nil {
		return ObjectInfo{}, err
	}

	st, err := os.Stat(full)
	if err != nil || !st.Mode().IsRegular() {
		return ObjectInfo{}, ErrNotFound
	}

	return ObjectInfo{Size: st.Size(), ModTime: st.ModTime(), ContentType: getContentType(full)}, nil
}

// Delete implements Backend.
func (l *LocalBackend) Delete(_ context.Context, key string) error {
	full, err := l.resolve(key)
	if err != nil {
		return err
	}

	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return nil
}

// List implements Backend. Temporary files written by Put and Check are
// skipped.
func (l *LocalBackend) List(ctx context.Context, prefix string, fn func(ObjectInfo) error) error {
	prefix = strings.TrimLeft(strings.TrimSpace(prefix), "/")
	dir, err := listPrefixDir(prefix)
	if err != nil {
		return err
	}

	root := filepath.Clean(l.Root)
	start := filepath.Join(root, filepath.FromSlash(dir))

	if _, err := os.Stat(start); errors.Is(err, os.ErrNotExist) {
		return nil
	}

	err = filepath.WalkDir(start, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".upload-") || strings.HasPrefix(name, ".health-") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		return fn(ObjectInfo{Key: key, Size: info.Size(), ModTime: info.ModTime(), ContentType: getContentType(p)})
	})
	if errors.Is(err, ErrStopList) {
		return nil
	}

	return err
}

// PublicURL implements Backend: local files are always served by the app.
func (l *LocalBackend) PublicURL(string) string { return "" }

// Check implements Backend: the root must be an existing writable directory.
func (l *LocalBackend) Check(_ context.Context) error {
	st, err := os.Stat(l.Root)
	if err != nil {
		return fmt.Errorf("upload directory %q is not accessible", l.Root)
	}

	if !st.IsDir() {
		return fmt.Errorf("upload path %q is not a directory", l.Root)
	}

	f, err := os.CreateTemp(l.Root, ".health-*")
	if err != nil {
		return fmt.Errorf("upload directory %q is not writable", l.Root)
	}

	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)

	return nil
}
