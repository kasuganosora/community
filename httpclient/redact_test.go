package httpclient

import (
	"net/url"
	"testing"
)

func TestRedactURL(t *testing.T) {
	u, _ := url.Parse("https://user:pw@example.com/a/b?access_token=secret#frag")
	got := redactURL(u)
	if got != "https://example.com/a/b?[redacted]" {
		t.Fatalf("got %q", got)
	}
	u2, _ := url.Parse("https://example.com/a")
	if redactURL(u2) != "https://example.com/a" {
		t.Fatalf("got %q", redactURL(u2))
	}
}
