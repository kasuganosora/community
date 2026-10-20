// Package avatarcache keeps resized copies of avatars on local disk.
//
// Both local avatars (/media/:id/:file?size=N) and proxied remote avatars
// (/proxy/media/:token?size=N) go through Cache.Get: a hit is served from
// disk; a miss fetches the source once (concurrent misses share one fetch),
// scales it to a fixed size bucket, re-encodes it and stores it.
//
// Layout: <dir>/<k[0:2]>/<k>.<ext> where k = sha256(source key | size).
// The file's mtime is when it was produced (TTL revalidation), its atime is
// the last time it was served (LRU eviction; refreshed explicitly so noatime
// mounts work). A janitor keeps the total under the configured cap.
package avatarcache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "golang.org/x/image/bmp" // decoder registration
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decoder registration
	"golang.org/x/sync/singleflight"
)

// Sizes are the square edge lengths (px) avatars are cached at.
var Sizes = []int{48, 96, 192, 400}

// DefaultSize is used when a request names no size.
const DefaultSize = 400

// Limits for source images.
const (
	MaxSourceBytes  = 20 << 20
	MaxSourcePixels = 40_000_000 // decoded width*height
	MaxSourceEdge   = 16384
	jpegQuality     = 85
	touchInterval   = time.Hour
	janitorInterval = 10 * time.Minute
	evictTarget     = 0.9 // evict down to this share of the cap
)

// Errors returned for unusable sources.
var (
	ErrTooLarge    = errors.New("avatarcache: source image too large")
	ErrInvalidType = errors.New("avatarcache: source is not a supported raster image")
)

// Fetcher returns the source bytes (at most MaxSourceBytes).
type Fetcher func(ctx context.Context) ([]byte, error)

// Entry is a cached rendition on disk.
type Entry struct {
	Path        string
	ContentType string
	ModTime     time.Time
	Size        int64
}

// Cache is a size-capped on-disk avatar cache.
type Cache struct {
	dir      string
	maxBytes int64
	now      func() time.Time
	group    singleflight.Group
	total    atomic.Int64 // estimate between janitor runs
	evictMu  sync.Mutex
	kick     chan struct{}
}

// New returns a cache rooted at dir capped at maxBytes (<=0 means 5 GiB).
func New(dir string, maxBytes int64) (*Cache, error) {
	if maxBytes <= 0 {
		maxBytes = 5 << 30
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	c := &Cache{dir: dir, maxBytes: maxBytes, now: time.Now, kick: make(chan struct{}, 1)}
	return c, nil
}

// Dir returns the cache root.
func (c *Cache) Dir() string { return c.dir }

// NormalizeSize maps a requested edge (query param) onto a bucket: the
// smallest bucket >= the request, or the largest one. Empty or invalid
// values give DefaultSize.
func NormalizeSize(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return DefaultSize
	}
	for _, s := range Sizes {
		if n <= s {
			return s
		}
	}
	return Sizes[len(Sizes)-1]
}

func (c *Cache) fileBase(key string, size int) string {
	sum := sha256.Sum256([]byte(key + "|" + strconv.Itoa(size)))
	k := hex.EncodeToString(sum[:])
	return filepath.Join(c.dir, k[:2], k)
}

var exts = map[string]string{".jpg": "image/jpeg", ".png": "image/png", ".gif": "image/gif", ".webp": "image/webp", ".avif": "image/avif"}

func (c *Cache) lookup(base string) (*Entry, bool) {
	for ext, ct := range exts {
		info, err := os.Stat(base + ext)
		if err == nil && info.Mode().IsRegular() {
			return &Entry{Path: base + ext, ContentType: ct, ModTime: info.ModTime(), Size: info.Size()}, true
		}
	}
	return nil, false
}

// Get returns the rendition of key at size. ttl > 0 makes entries older
// than ttl refetch; when that refetch fails the stale entry is still served.
func (c *Cache) Get(ctx context.Context, key string, size int, ttl time.Duration, fetch Fetcher) (*Entry, error) {
	return c.GetRendered(ctx, key, size, ttl, fetch, Render)
}

// GetRendered is Get with a custom renderer (e.g. RenderStatic); key must
// differ per renderer.
func (c *Cache) GetRendered(ctx context.Context, key string, size int, ttl time.Duration, fetch Fetcher, render func([]byte, int) ([]byte, string, error)) (*Entry, error) {
	base := c.fileBase(key, size)
	stale, ok := c.lookup(base)
	if ok && (ttl <= 0 || c.now().Sub(stale.ModTime) < ttl) {
		c.touch(stale.Path)
		return stale, nil
	}
	v, err, _ := c.group.Do(base, func() (any, error) {
		// Another flight may have just finished.
		if e, ok := c.lookup(base); ok && (ttl <= 0 || c.now().Sub(e.ModTime) < ttl) {
			return e, nil
		}
		// Detach from the first caller's cancellation: others share this.
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		src, err := fetch(fctx)
		if err != nil {
			return nil, err
		}
		if len(src) > MaxSourceBytes {
			return nil, ErrTooLarge
		}
		data, ext, err := render(src, size)
		if err != nil {
			return nil, err
		}
		return c.store(base, ext, data)
	})
	if err != nil {
		if stale != nil {
			c.touch(stale.Path)
			return stale, nil
		}
		return nil, err
	}
	return v.(*Entry), nil
}

func (c *Cache) store(base, ext string, data []byte) (*Entry, error) {
	if err := os.MkdirAll(filepath.Dir(base), 0o750); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(base), ".tmp-*")
	if err != nil {
		return nil, err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	// Drop renditions of the same key in another format.
	for other := range exts {
		if other != ext {
			_ = os.Remove(base + other)
		}
	}
	if err := os.Rename(tmp.Name(), base+ext); err != nil {
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	now := c.now()
	_ = os.Chtimes(base+ext, now, now)
	if c.total.Add(int64(len(data))) > c.maxBytes {
		select {
		case c.kick <- struct{}{}:
		default:
		}
	}
	return &Entry{Path: base + ext, ContentType: exts[ext], ModTime: now, Size: int64(len(data))}, nil
}

// touch records an access (atime) at most once per touchInterval.
func (c *Cache) touch(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	now := c.now()
	if now.Sub(accessTime(info)) < touchInterval {
		return
	}
	_ = os.Chtimes(path, now, info.ModTime())
}

// Render scales src to a size x size center crop (never upscaling) and
// encodes it: PNG when the image has transparency, JPEG otherwise. Animated
// GIFs and formats Go cannot decode (animated WebP, AVIF, HEIC) are kept
// as-is so animation is not lost; they are still size-checked.
func Render(src []byte, size int) ([]byte, string, error) {
	ct := sniff(src)
	if ct == "" {
		return nil, "", ErrInvalidType
	}
	if ct == "image/avif" {
		return src, ".avif", nil
	}
	if ct == "image/webp" && isAnimatedWebP(src) {
		// Go's webp decoder reads an animated file's canvas size but cannot
		// decode its frames; keep it as-is like an animated GIF.
		return src, ".webp", nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		if ct == "image/webp" { // animated / extended WebP
			return src, ".webp", nil
		}
		return nil, "", ErrInvalidType
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxSourceEdge || cfg.Height > MaxSourceEdge ||
		int64(cfg.Width)*int64(cfg.Height) > MaxSourcePixels {
		return nil, "", ErrTooLarge
	}
	if format == "gif" {
		g, err := gif.DecodeAll(bytes.NewReader(src))
		if err != nil {
			return nil, "", ErrInvalidType
		}
		if len(g.Image) > 1 {
			return src, ".gif", nil
		}
	}
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		if ct == "image/webp" {
			// Extended WebP variants the decoder does not handle are still
			// valid images for browsers: serve the original bytes.
			return src, ".webp", nil
		}
		return nil, "", ErrInvalidType
	}
	return encodeScaled(img, size)
}

// encodeScaled center-crops and scales img, as PNG when it has transparency
// and JPEG otherwise.
func encodeScaled(img image.Image, size int) ([]byte, string, error) {
	out := scale(img, size)
	var buf bytes.Buffer
	if hasAlpha(out) {
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		if err := enc.Encode(&buf, out); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), ".png", nil
	}
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), ".jpg", nil
}

// isAnimatedWebP reports whether src is an extended (VP8X) WebP with the
// animation flag set or an ANIM chunk.
func isAnimatedWebP(src []byte) bool {
	if len(src) < 21 || string(src[0:4]) != "RIFF" || string(src[8:12]) != "WEBP" || string(src[12:16]) != "VP8X" {
		return false
	}
	if src[20]&0x02 != 0 {
		return true
	}
	// Walk the chunks for ANIM/ANMF in case the flag is missing.
	for off := 12; off+8 <= len(src); {
		id := string(src[off : off+4])
		n := int(src[off+4]) | int(src[off+5])<<8 | int(src[off+6])<<16 | int(src[off+7])<<24
		if id == "ANIM" || id == "ANMF" {
			return true
		}
		if n < 0 || n > len(src) {
			return false
		}
		off += 8 + n + n&1
	}
	return false
}

func sniff(b []byte) string {
	if len(b) >= 12 && string(b[4:8]) == "ftyp" && (string(b[8:12]) == "avif" || string(b[8:12]) == "avis") {
		return "image/avif"
	}
	t := http.DetectContentType(b)
	switch t {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp":
		return t
	}
	return ""
}

func scale(src image.Image, size int) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	edge := min(w, h)
	crop := image.Rect(b.Min.X+(w-edge)/2, b.Min.Y+(h-edge)/2, b.Min.X+(w-edge)/2+edge, b.Min.Y+(h-edge)/2+edge)
	out := min(size, edge)
	dst := image.NewNRGBA(image.Rect(0, 0, out, out))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, draw.Src, nil)
	return dst
}

func hasAlpha(img *image.NRGBA) bool {
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0xff {
			return true
		}
	}
	return false
}

// Janitor evicts least-recently-used entries while the cache is above its
// cap; it runs at start, every interval and when a store crosses the cap.
func (c *Cache) Janitor(ctx context.Context) {
	t := time.NewTicker(janitorInterval)
	defer t.Stop()
	for {
		if _, err := c.Evict(); err != nil {
			fmt.Fprintf(os.Stderr, "avatarcache: evict: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-c.kick:
		}
	}
}

type fileInfo struct {
	path  string
	size  int64
	atime time.Time
}

// Evict scans the cache, removes stale temp files and, when the total is
// above the cap, deletes least recently accessed files down to 90% of it.
// It returns the number of bytes removed.
func (c *Cache) Evict() (int64, error) {
	c.evictMu.Lock()
	defer c.evictMu.Unlock()
	var files []fileInfo
	var total int64
	err := filepath.WalkDir(c.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // skip unreadable entries
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr
		}
		if strings.HasPrefix(d.Name(), ".tmp-") {
			if c.now().Sub(info.ModTime()) > time.Hour {
				_ = os.Remove(p)
			}
			return nil
		}
		files = append(files, fileInfo{p, info.Size(), accessTime(info)})
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	var removed int64
	if total > c.maxBytes {
		sort.Slice(files, func(i, j int) bool { return files[i].atime.Before(files[j].atime) })
		target := int64(float64(c.maxBytes) * evictTarget)
		for _, f := range files {
			if total <= target {
				break
			}
			if os.Remove(f.path) == nil {
				total -= f.size
				removed += f.size
			}
		}
	}
	c.total.Store(total)
	return removed, nil
}
