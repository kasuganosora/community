package activitypub

import (
	"context"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateFederationURIErrors(t *testing.T) {
	tests := []struct {
		name string
		uri  string
	}{
		{"empty", ""},
		{"file scheme", "file:///etc/passwd"},
		{"no host", "https://"},
		{"gopher", "gopher://localhost"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFederationURI(tt.uri)
			require.Error(t, err)
		})
	}
}

func TestSignRequestPath(t *testing.T) {
	tests := []struct {
		name     string
		url      *url.URL
		expected string
	}{
		{
			name:     "with path and query",
			url:      &url.URL{Path: "/users/alice/inbox", RawQuery: "foo=bar"},
			expected: "/users/alice/inbox?foo=bar",
		},
		{
			name:     "empty path defaults to root",
			url:      &url.URL{Path: ""},
			expected: "/",
		},
		{
			name:     "path only no query",
			url:      &url.URL{Path: "/inbox"},
			expected: "/inbox",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := signRequestPath(tt.url)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClientGetSSRFBlocked(t *testing.T) {
	client := NewClient(nil, "")
	_, err := client.Get(context.Background(), "http://10.0.0.1/test")
	require.Error(t, err)
	assert.True(t, IsSSRFBlocked(err))
}

func TestClientPostSSRFBlocked(t *testing.T) {
	client := NewClient(nil, "https://example.com/actor")
	err := client.Post(context.Background(), "http://192.168.1.1/inbox", map[string]any{"type": "Test"})
	require.Error(t, err)
}

func TestClientPostInvalidURI(t *testing.T) {
	client := NewClient(nil, "")
	err := client.Post(context.Background(), "ftp://example.com/inbox", map[string]any{"type": "Test"})
	require.Error(t, err)
}

func TestNewDeliveryQueue(t *testing.T) {
	q := NewDeliveryQueue(nil, "", 2)
	require.NotNil(t, q)
	assert.NotNil(t, q.stopChan)
	assert.Equal(t, 2, q.workers)
}

func TestDeliveryQueueStartStop(t *testing.T) {
	q := NewDeliveryQueue(nil, "", 1)
	q.Start()
	// Stop should not hang
	q.Stop()
}
