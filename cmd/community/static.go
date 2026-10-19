package main

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Cache policies for frontend files. Everything under /assets/ carries a
// content hash in its name (vite build), so it can be cached forever; other
// files keep stable names and must be revalidated (Last-Modified).
const (
	cacheImmutable  = "public, max-age=31536000, immutable"
	cacheRevalidate = "no-cache"
)

// precompressedEncodings lists the sidecar files written by the frontend
// build (frontend/build/precompress.ts), in server preference order.
var precompressedEncodings = []struct{ token, ext string }{
	{"br", ".br"},
	{"gzip", ".gz"},
}

// acceptsEncoding reports whether an Accept-Encoding header allows token
// (explicitly or via "*") with a non-zero quality.
func acceptsEncoding(header, token string) bool {
	wildcard := false
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		name = strings.ToLower(strings.TrimSpace(name))
		if name != token && name != "*" {
			continue
		}
		ok := true
		for _, p := range strings.Split(params, ";") {
			k, v, found := strings.Cut(strings.TrimSpace(p), "=")
			if found && strings.EqualFold(strings.TrimSpace(k), "q") {
				if q, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && q <= 0 {
					ok = false
				}
			}
		}
		if name == token {
			return ok
		}
		wildcard = ok
	}

	return wildcard
}

// serveFrontendFile writes a built frontend file, preferring a precompressed
// .br/.gz sidecar the client accepts, with cache headers chosen by path.
func serveFrontendFile(c *gin.Context, abs, requestPath string) {
	switch {
	case strings.HasPrefix(requestPath, "/assets/"):
		c.Header("Cache-Control", cacheImmutable)
	case requestPath == "/sw.js":
		// The browser byte-compares the worker on every update check; a CDN
		// or HTTP-cache copy would pin clients to the old build until purged.
		c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		c.Header("Service-Worker-Allowed", "/")
	case requestPath == "/version.json":
		// The client compares this build id with its own to detect a deploy:
		// neither the browser nor the CDN may keep a copy.
		c.Header("Cache-Control", "no-store")
	default:
		c.Header("Cache-Control", cacheRevalidate)
	}

	if ct, ok := frontendMIMETypes[strings.ToLower(path.Ext(abs))]; ok {
		c.Header("Content-Type", ct)
	}

	servePath := abs
	if !strings.HasSuffix(abs, ".br") && !strings.HasSuffix(abs, ".gz") {
		var anyVariant bool
		ae := c.GetHeader("Accept-Encoding")
		for _, enc := range precompressedEncodings {
			info, err := os.Stat(abs + enc.ext)
			if err != nil || info.IsDir() {
				continue
			}
			anyVariant = true
			if acceptsEncoding(ae, enc.token) {
				servePath = abs + enc.ext
				c.Header("Content-Encoding", enc.token)

				break
			}
		}
		if anyVariant {
			c.Header("Vary", "Accept-Encoding")
		}
	}

	f, err := os.Open(servePath) //nolint:gosec // G304: path confined to the dist directory by the caller
	if err != nil {
		c.Status(http.StatusNotFound)

		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		c.Status(http.StatusNotFound)

		return
	}
	// Name the content by the original file so ServeContent never sniffs the
	// compressed bytes for a Content-Type.
	http.ServeContent(c.Writer, c.Request, filepath.Base(abs), info.ModTime(), f)
}

// frontendFileHandler serves requestPath-relative files below frontendDir.
// archiveDirs are fallbacks (previous builds' /assets, see asset_archive.go).
func frontendFileHandler(frontendDir string, archiveDirs ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		if hasDotSegment(p) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})

			return
		}
		file, ok := frontendAsset(frontendDir, p)
		if !ok && strings.HasPrefix(p, "/assets/") {
			for _, archive := range archiveDirs {
				if archive == "" {
					continue
				}
				if file, ok = frontendAsset(archive, strings.TrimPrefix(p, "/assets")); ok {
					break
				}
			}
		}
		if !ok {
			// A hashed asset can be requested moments before the new build
			// is on disk (or after an old one is gone). Without a header the
			// CDN caches this 404 for hours (Cloudflare: 4h), breaking the
			// next deploy's pages; never let it be cached.
			c.Header("Cache-Control", "no-store")
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})

			return
		}
		serveFrontendFile(c, file, p)
	}
}
