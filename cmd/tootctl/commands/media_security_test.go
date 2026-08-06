package commands

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsBlockedAddress(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected bool
	}{
		{"loopback IPv4", "127.0.0.1", true},
		{"loopback IPv4 high", "127.255.255.254", true},
		{"loopback IPv6", "::1", true},
		{"private 10.x", "10.0.0.1", true},
		{"private 172.16.x", "172.16.0.1", true},
		{"private 192.168.x", "192.168.1.1", true},
		{"link-local IPv4", "169.254.169.254", true}, // cloud metadata
		{"link-local IPv6", "fe80::1", true},
		{"unspecified", "0.0.0.0", true},
		{"multicast", "224.0.0.1", true},
		{"public IPv4", "8.8.8.8", false},
		{"public IPv4 2", "1.1.1.1", false},
		{"public IPv6", "2001:4860:4860::8888", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			require.NotNil(t, ip, "invalid test IP: %s", tt.ip)
			assert.Equal(t, tt.expected, isBlockedAddress(ip))
		})
	}
}

func TestIsBlockedAddress_NilIP(t *testing.T) {
	assert.True(t, isBlockedAddress(nil))
}

func TestValidateRemoteURL_InvalidSchemes(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"file scheme", "file:///etc/passwd"},
		{"ftp scheme", "ftp://example.com/file"},
		{"gopher scheme", "gopher://example.com/"},
		{"javascript scheme", "javascript:alert(1)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRemoteURL(tt.url)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "scheme")
		})
	}
}

func TestValidateRemoteURL_EmptyHost(t *testing.T) {
	err := validateRemoteURL("http:///path")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host")
}

func TestValidateRemoteURL_BlockLoopbackIP(t *testing.T) {
	err := validateRemoteURL("http://127.0.0.1/admin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blocked")
}

func TestValidateRemoteURL_BlockMetadataIP(t *testing.T) {
	// AWS/cloud metadata endpoint.
	err := validateRemoteURL("http://169.254.169.254/latest/meta-data/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blocked")
}

func TestValidateRemoteURL_BlockPrivateIP(t *testing.T) {
	err := validateRemoteURL("http://10.0.0.1/internal-service")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blocked")
}

func TestValidateRemoteURL_BadURL(t *testing.T) {
	err := validateRemoteURL("://not-a-url")
	require.Error(t, err)
}

func TestMaxRemoteMediaSizeConstant(t *testing.T) {
	assert.Equal(t, int64(50*1024*1024), maxRemoteMediaSize)
}
