package homeserver

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/kasuganosora/community/storage"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
)

func TestBridgeAvatarHTTPSCDNRedirect(t *testing.T) {
	hs, _ := directBridgeDB(t)
	restore := storage.SetDefault(&storage.LocalBackend{Root: t.TempDir()})
	defer restore()
	original := newBridgeAvatarHTTPClient
	defer func() { newBridgeAvatarHTTPClient = original }()
	image := bridgeAvatarPNG(t)
	requests := []string{}
	newBridgeAvatarHTTPClient = func() *http.Client {
		return &http.Client{CheckRedirect: bridgeAvatarRedirect, Transport: bridgeAvatarTransport(func(r *http.Request) (*http.Response, error) {
			requests = append(requests, r.URL.String())
			if r.URL.Host == "actor.example" {
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://cdn.example/avatar.webp"}}, Body: io.NopCloser(bytes.NewReader(nil)), Request: r}, nil
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(image)), ContentLength: int64(len(image)), Request: r}, nil
		})}
	}
	mxc, err := hs.importBridgeAvatar(context.Background(), "@ghost:test.local", "https://actor.example/avatar", false)
	require.NoError(t, err)
	require.Contains(t, mxc, "mxc://test.local/")
	require.Equal(t, []string{"https://actor.example/avatar", "https://cdn.example/avatar.webp"}, requests)
}

func TestBridgeAvatarRedirectPolicy(t *testing.T) {
	for _, raw := range []string{"http://cdn.example/avatar", "https://user:password@cdn.example/avatar", "https://cdn.example/avatar#fragment"} {
		u, err := url.Parse(raw)
		require.NoError(t, err)
		require.Error(t, bridgeAvatarRedirect(&http.Request{URL: u}, nil))
	}
	u, _ := url.Parse("https://cdn.example/avatar")
	require.Error(t, bridgeAvatarRedirect(&http.Request{URL: u}, make([]*http.Request, 5)))
}

func TestBridgeAvatarRealHTTPSRedirect(t *testing.T) {
	hs, _ := directBridgeDB(t)
	restore := storage.SetDefault(&storage.LocalBackend{Root: t.TempDir()})
	defer restore()
	original := newBridgeAvatarHTTPClient
	defer func() { newBridgeAvatarHTTPClient = original }()
	image := bridgeAvatarPNG(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/avatar" {
			http.Redirect(w, r, "/image", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(image)
	}))
	defer server.Close()
	newBridgeAvatarHTTPClient = func() *http.Client { c := server.Client(); c.CheckRedirect = bridgeAvatarRedirect; return c }
	mxc, err := hs.importBridgeAvatar(context.Background(), "@ghost:test.local", server.URL+"/avatar", false)
	require.NoError(t, err)
	require.Contains(t, mxc, "mxc://test.local/")
}
