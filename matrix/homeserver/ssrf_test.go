package homeserver

import (
	"net"
	"testing"
)

func TestIsBlockedAddress(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected bool
	}{
		{"loopback IPv4", "127.0.0.1", true},
		{"loopback IPv4 alt", "127.1.2.3", true},
		{"loopback IPv6", "::1", true},
		{"private 10.x", "10.0.0.1", true},
		{"private 172.16.x", "172.16.0.1", true},
		{"private 172.31.x", "172.31.255.255", true},
		{"private 192.168.x", "192.168.1.1", true},
		{"link-local 169.254.x", "169.254.169.254", true},
		{"link-local IPv6", "fe80::1", true},
		{"unspecified", "0.0.0.0", true},
		{"multicast", "224.0.0.1", true},
		{"valid public IPv4", "8.8.8.8", false},
		{"valid public IPv4 alt", "1.1.1.1", false},
		{"valid public IPv6", "2001:4860:4860::8888", false},
		{"172.32.x is NOT private", "172.32.0.1", false},
		{"nil ip", "<nil>", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ip net.IP
			if tt.ip != "<nil>" {
				ip = net.ParseIP(tt.ip)
				if ip == nil {
					t.Fatalf("failed to parse IP %s", tt.ip)
				}
			}

			result := isBlockedAddress(ip)
			if result != tt.expected {
				t.Errorf("isBlockedAddress(%s) = %v, want %v", tt.ip, result, tt.expected)
			}
		})
	}
}

func TestValidateURLForFetch(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		expectError bool
	}{
		{"invalid url", "://not-a-url", true},
		{"file scheme", "file:///etc/passwd", true},
		{"ftp scheme", "ftp://example.com/file", true},
		{"gopher scheme", "gopher://internal:6379/", true},
		{"javascript scheme", "javascript:alert(1)", true},
		{"empty host", "http://", true},
		// Note: We can't test actual DNS resolution against real/public hosts
		// reliably in unit tests, but we can verify scheme validation.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateURLForFetch(tt.url)
			hasErr := err != nil
			if hasErr != tt.expectError {
				t.Errorf("validateURLForFetch(%q) error = %v, expectError = %v",
					tt.url, err, tt.expectError)
			}
		})
	}
}
