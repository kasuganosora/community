package activitypub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseHTMLIcon(t *testing.T) {
	page := "https://ex.example/"
	cases := []struct{ name, html, want string }{
		{"relative icon", `<head><link rel="icon" href="/fav.png"></head>`, "https://ex.example/fav.png"},
		{"prefers smallest >=64", `<link rel="icon" sizes="32x32" href="a32.png"><link rel="icon" sizes="192x192" href="a192.png"><link rel="icon" sizes="96x96" href="a96.png">`, "https://ex.example/a96.png"},
		{"apple touch without sizes beats tiny", `<link rel="icon" sizes="16x16" href="/s.png"><link rel="apple-touch-icon" href="/apple.png">`, "https://ex.example/apple.png"},
		{"shortcut icon + base href", `<base href="https://cdn.example/x/"><link rel="shortcut icon" href="i.png">`, "https://cdn.example/x/i.png"},
		{"skips svg and data", `<link rel="icon" type="image/svg+xml" href="/i.svg"><link rel="icon" href="data:image/png;base64,AA"><link rel="icon" href="/ok.ico">`, "https://ex.example/ok.ico"},
		{"absolute protocol-relative", `<link rel="icon" href="//cdn.example/i.png">`, "https://cdn.example/i.png"},
		{"rejects javascript", `<link rel="icon" href="javascript:alert(1)">`, ""},
		{"ignores body links", `<body><link rel="icon" href="/late.png">`, ""},
		{"none", `<link rel="stylesheet" href="/s.css">`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, parseHTMLIcon(page, []byte(tc.html)))
		})
	}
}

// iconServer serves configurable endpoints and records request paths.
func iconServer(t *testing.T, routes map[string]func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, func() []string) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if h, ok := routes[r.URL.Path]; ok {
			h(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	oldClient := remoteReactionClient
	remoteReactionClient = ssrfHTTPClientForTesting()
	t.Cleanup(func() { srv.Close(); remoteReactionClient = oldClient })
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seen...) }
}

func jsonH(v any) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(v) }
}

func htmlH(s string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(s))
	}
}

func icoH(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "image/x-icon")
	_, _ = w.Write([]byte{0, 0, 1, 0})
}

func TestDiscoverInstanceIconOrder(t *testing.T) {
	ctx := context.Background()
	home := `<link rel="icon" sizes="128x128" href="/home.png">`

	t.Run("misskey nodeinfo metadata first", func(t *testing.T) {
		srv, seen := iconServer(t, map[string]func(http.ResponseWriter, *http.Request){
			"/api/meta": jsonH(map[string]any{"iconUrl": "https://m.example/meta.png", "name": "M"}),
		})
		icon, name := discoverInstanceIcon(ctx, srv.URL, "sharkey", map[string]any{"iconUrl": "https://m.example/ni.png"})
		require.Equal(t, "https://m.example/ni.png", icon)
		require.Equal(t, "M", name)
		require.NotContains(t, seen(), "GET /")
	})
	t.Run("misskey api/meta", func(t *testing.T) {
		srv, seen := iconServer(t, map[string]func(http.ResponseWriter, *http.Request){
			"/api/meta": jsonH(map[string]any{"iconUrl": "/files/icon.webp"}),
			"/":         htmlH(home),
		})
		icon, _ := discoverInstanceIcon(ctx, srv.URL, "misskey", nil)
		require.Equal(t, srv.URL+"/files/icon.webp", icon)
		require.Equal(t, []string{"POST /api/meta"}, seen())
	})
	t.Run("mastodon icon array smallest >=64", func(t *testing.T) {
		srv, seen := iconServer(t, map[string]func(http.ResponseWriter, *http.Request){
			"/api/v2/instance": jsonH(map[string]any{"icon": []any{
				map[string]any{"src": "https://s.example/36.png", "size": "36x36"},
				map[string]any{"src": "https://s.example/400.png", "size": "400x400"},
				map[string]any{"src": "https://s.example/72.png", "size": "72x72"},
			}}),
			"/": htmlH(home),
		})
		icon, _ := discoverInstanceIcon(ctx, srv.URL, "mastodon", nil)
		require.Equal(t, "https://s.example/72.png", icon)
		require.Equal(t, []string{"GET /api/v2/instance"}, seen())
	})
	t.Run("misskey without meta icon falls to homepage", func(t *testing.T) {
		srv, _ := iconServer(t, map[string]func(http.ResponseWriter, *http.Request){
			"/api/meta": jsonH(map[string]any{"iconUrl": nil}),
			"/":         htmlH(home),
		})
		icon, _ := discoverInstanceIcon(ctx, srv.URL, "misskey", nil)
		require.Equal(t, srv.URL+"/home.png", icon)
	})
	t.Run("homepage after mastodon miss", func(t *testing.T) {
		srv, seen := iconServer(t, map[string]func(http.ResponseWriter, *http.Request){
			"/":            htmlH(home),
			"/favicon.ico": icoH,
		})
		icon, _ := discoverInstanceIcon(ctx, srv.URL, "gotosocial", nil)
		require.Equal(t, srv.URL+"/home.png", icon)
		require.Equal(t, []string{"GET /api/v2/instance", "GET /"}, seen())
	})
	t.Run("favicon fallback verified", func(t *testing.T) {
		srv, _ := iconServer(t, map[string]func(http.ResponseWriter, *http.Request){
			"/":            htmlH(`<title>x</title>`),
			"/favicon.ico": icoH,
		})
		icon, _ := discoverInstanceIcon(ctx, srv.URL, "", nil)
		require.Equal(t, srv.URL+"/favicon.ico", icon)
	})
	t.Run("favicon that is html is rejected", func(t *testing.T) {
		srv, _ := iconServer(t, map[string]func(http.ResponseWriter, *http.Request){
			"/favicon.ico": htmlH(`<html>soft 404</html>`),
		})
		icon, _ := discoverInstanceIcon(ctx, srv.URL, "", nil)
		require.Equal(t, "", icon)
	})
}
