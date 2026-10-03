package activitypub

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type remoteMediaRoundTrip func(*http.Request) (*http.Response, error)

func (f remoteMediaRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRemoteMediaBoundsContentLengthAndStreamingBody(t *testing.T) {
	for _, length := range []int64{64, -1} {
		t.Run(map[bool]string{true: "declared", false: "chunked"}[length > 0], func(t *testing.T) {
			client := &http.Client{Transport: remoteMediaRoundTrip(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/png"}}, ContentLength: length, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 64))), Request: r}, nil
			})}
			data, _, err := fetchRemoteMedia(context.Background(), "https://images.example/emoji.png", 16, client)
			require.ErrorContains(t, err, "exceeds")
			require.Nil(t, data)
		})
	}
	client := &http.Client{Transport: remoteMediaRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/png"}}, ContentLength: 4, Body: io.NopCloser(strings.NewReader("test")), Request: r}, nil
	})}
	data, mime, err := fetchRemoteMedia(context.Background(), "https://images.example/emoji.png", 4, client)
	require.NoError(t, err)
	require.Equal(t, []byte("test"), data)
	require.Equal(t, "image/png", mime)
}

func TestRemoteMediaRejectsBadURLsBeforeTransport(t *testing.T) {
	called := false
	client := &http.Client{Transport: remoteMediaRoundTrip(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected transport")
	})}
	for _, raw := range []string{"file:///etc/passwd", "data:image/png,x", "https://user:password@images.example/a", "https://images.example/a#fragment", "/relative.png"} {
		_, _, err := fetchRemoteMedia(context.Background(), raw, 8, client)
		require.Error(t, err, raw)
	}
	require.False(t, called)
}

func TestRemoteMediaRedirectRestrictions(t *testing.T) {
	for _, target := range []string{"file:///etc/passwd", "https://user:password@images.example/x", "http://images.example/downgrade"} {
		t.Run(target, func(t *testing.T) {
			var calls int
			client := &http.Client{Transport: remoteMediaRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{target}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			})}
			_, _, err := fetchRemoteMedia(context.Background(), "https://images.example/start", 16, client)
			require.Error(t, err)
			require.Equal(t, 1, calls)
		})
	}
}

func TestRemoteMediaRedirectCannotReachPrivateServer(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetHits.Add(1); _, _ = w.Write([]byte("private")) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == "public-image.example:80" {
			return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(source.URL, "http://"))
		}
		return ssrfSafeDialContext(ctx, network, addr)
	}}
	defer transport.CloseIdleConnections()
	_, _, err := fetchRemoteMedia(context.Background(), "http://public-image.example/start", 16, &http.Client{Transport: transport})
	require.Error(t, err)
	require.True(t, IsSSRFBlocked(err))
	require.Zero(t, targetHits.Load())
	// The public API also blocks an initial loopback address, not just redirects.
	_, _, err = FetchRemoteMedia(context.Background(), target.URL, 16)
	require.Error(t, err)
	require.Zero(t, targetHits.Load())
}

func TestRemoteMediaHonorsCancellationAndRejectsUntrustedTLS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	client := &http.Client{Transport: remoteMediaRoundTrip(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}
	_, _, err := fetchRemoteMedia(ctx, "https://images.example/slow", 16, client)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("image")) }))
	defer tlsServer.Close()
	transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(tlsServer.URL, "https://"))
	}}
	defer transport.CloseIdleConnections()
	_, _, err = fetchRemoteMedia(context.Background(), "https://images.example/test", 16, &http.Client{Transport: transport})
	require.Error(t, err)
	require.Contains(t, err.Error(), "certificate")
}
