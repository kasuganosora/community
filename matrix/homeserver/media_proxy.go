package homeserver

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"time"
)

type proxyMediaBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *proxyMediaBody) Close() error { b.cancel(); return b.ReadCloser.Close() }

// Proxy public AP attachments behind their opaque MXC identity. No bytes are
// written to disk or the object store; the same DNS-pinned SSRF policy used by
// federation is enforced by the caller's client, including redirect policy.
func openBridgeAttachment(ctx context.Context, raw string, client *http.Client) (*remoteMedia, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("invalid attachment URL")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Accept", "*/*")
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK || resp.ContentLength > matrixMediaLimit {
		resp.Body.Close()
		cancel()
		return nil, errors.New("attachment unavailable or too large")
	}
	typ, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || typ == "text/html" || typ == "application/xhtml+xml" || typ == "image/svg+xml" {
		typ = "application/octet-stream"
	}
	body := &limitedMediaBody{r: resp.Body, remaining: matrixMediaLimit, closers: []io.Closer{resp.Body}}
	return &remoteMedia{Body: &proxyMediaBody{ReadCloser: body, cancel: cancel}, ContentType: typ, Filename: bridgeDownloadFilename(resp.Header.Get("Content-Disposition")), Length: resp.ContentLength}, nil
}
