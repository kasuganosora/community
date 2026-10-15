package middleware

import (
	"bufio"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
)

// compressMinBytes: smaller bodies are not worth the encoding overhead.
const compressMinBytes = 1024

// compressibleType reports dynamic text bodies worth compressing (API and
// federation JSON, HTML, plain text). Media, already-compressed data and
// event streams are never touched.
func compressibleType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	switch {
	case ct == "application/json", strings.HasSuffix(ct, "+json"),
		ct == "application/javascript", ct == "text/javascript",
		ct == "text/html", ct == "text/plain", ct == "text/css",
		ct == "application/xml", ct == "text/xml", strings.HasSuffix(ct, "+xml"):
		return true
	}
	return false
}

// negotiateEncoding picks br, then gzip, from an Accept-Encoding header.
func negotiateEncoding(header string) string {
	if header == "" {
		return ""
	}
	if headerAccepts(header, "br") {
		return "br"
	}
	if headerAccepts(header, "gzip") {
		return "gzip"
	}
	return ""
}

func headerAccepts(header, token string) bool {
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

var (
	gzipPool   = sync.Pool{New: func() any { w, _ := gzip.NewWriterLevel(io.Discard, 5); return w }}
	brotliPool = sync.Pool{New: func() any { return brotli.NewWriterLevel(io.Discard, 4) }}
)

// APICompression compresses dynamic text responses (JSON first of all) with
// brotli or gzip per Accept-Encoding. The decision is made on the first
// body write, so handlers that set their own Content-Encoding, stream media
// or send tiny bodies are passed through untouched. WebSocket upgrades,
// SSE and the streaming API are skipped entirely; static frontend files are
// served precompressed by their own handler.
func APICompression() gin.HandlerFunc {
	return func(c *gin.Context) {
		r := c.Request
		enc := negotiateEncoding(r.Header.Get("Accept-Encoding"))
		if enc == "" || r.Method == http.MethodHead ||
			strings.EqualFold(r.Header.Get("Upgrade"), "websocket") ||
			strings.Contains(r.Header.Get("Accept"), "text/event-stream") ||
			strings.HasPrefix(r.URL.Path, "/api/v1/streaming") || strings.HasPrefix(r.URL.Path, "/api/v2/streaming") ||
			strings.HasPrefix(r.URL.Path, "/proxy/") || strings.HasPrefix(r.URL.Path, "/media/") ||
			strings.HasPrefix(r.URL.Path, "/assets/") {
			c.Next()
			return
		}
		w := &compressWriter{ResponseWriter: c.Writer, encoding: enc}
		c.Writer = w
		defer w.finish()
		c.Next()
	}
}

type compressWriter struct {
	gin.ResponseWriter
	encoding string
	decided  bool
	enc      io.WriteCloser
	flusher  interface{ Flush() error }
}

func (w *compressWriter) decide(first []byte) {
	w.decided = true
	h := w.ResponseWriter.Header()
	status := w.ResponseWriter.Status()
	if len(first) < compressMinBytes || h.Get("Content-Encoding") != "" || !compressibleType(h.Get("Content-Type")) ||
		status == http.StatusNoContent || status == http.StatusNotModified || status < 200 ||
		strings.HasPrefix(h.Get("Content-Type"), "text/event-stream") || h.Get("Content-Range") != "" {
		return
	}
	h.Del("Content-Length")
	h.Set("Content-Encoding", w.encoding)
	h.Add("Vary", "Accept-Encoding")
	if etag := h.Get("ETag"); etag != "" && !strings.HasPrefix(etag, "W/") {
		h.Set("ETag", "W/"+etag) // the representation differs from identity
	}
	if w.encoding == "br" {
		bw := brotliPool.Get().(*brotli.Writer) //nolint:forcetypeassert // pool type
		bw.Reset(w.ResponseWriter)
		w.enc, w.flusher = bw, bw
	} else {
		gw := gzipPool.Get().(*gzip.Writer) //nolint:forcetypeassert // pool type
		gw.Reset(w.ResponseWriter)
		w.enc, w.flusher = gw, gw
	}
}

func (w *compressWriter) Write(data []byte) (int, error) {
	if !w.decided {
		w.decide(data)
	}
	if w.enc == nil {
		return w.ResponseWriter.Write(data)
	}
	w.ResponseWriter.WriteHeaderNow()
	return w.enc.Write(data)
}

func (w *compressWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func (w *compressWriter) Flush() {
	if w.flusher != nil {
		_ = w.flusher.Flush()
	}
	w.ResponseWriter.Flush()
}

func (w *compressWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.Hijack()
}

func (w *compressWriter) finish() {
	if w.enc == nil {
		return
	}
	_ = w.enc.Close()
	switch e := w.enc.(type) {
	case *brotli.Writer:
		e.Reset(io.Discard)
		brotliPool.Put(e)
	case *gzip.Writer:
		e.Reset(io.Discard)
		gzipPool.Put(e)
	}
	w.enc = nil
}
