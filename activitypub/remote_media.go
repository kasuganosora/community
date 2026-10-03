package activitypub

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// FetchRemoteMedia downloads bounded media through the same DNS-pinned,
// redirect-checked transport and trusted TLS configuration as federation.
// The caller must still validate the bytes as its supported image formats.
func FetchRemoteMedia(ctx context.Context, rawURL string, limit int64) ([]byte, string, error) {
	return fetchRemoteMedia(ctx, rawURL, limit, ssrfSafeHTTPClient)
}

func remoteMediaURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Opaque != "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("invalid remote image URL")
	}
	return validateLabURL(u)
}

func fetchRemoteMedia(ctx context.Context, rawURL string, limit int64, base *http.Client) ([]byte, string, error) {
	if limit <= 0 || limit > maxResponseSize {
		return nil, "", fmt.Errorf("invalid media size limit")
	}
	if err := remoteMediaURL(rawURL); err != nil {
		return nil, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// Copy per request; never mutate the shared client's redirect policy.
	client := *base
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many image redirects")
		}
		if err := remoteMediaURL(req.URL.String()); err != nil {
			return err
		}
		if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return fmt.Errorf("image redirect downgraded HTTPS")
		}
		if base.CheckRedirect != nil {
			return base.CheckRedirect(req, via)
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "image/png, image/apng, image/jpeg, image/gif, image/webp")
	req.Header.Set("User-Agent", "Community/1.0 Emoji Import")
	response, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetch image: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("image server returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return nil, "", fmt.Errorf("image exceeds %d bytes", limit)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > limit {
		return nil, "", fmt.Errorf("image exceeds %d bytes", limit)
	}
	return data, response.Header.Get("Content-Type"), nil
}
