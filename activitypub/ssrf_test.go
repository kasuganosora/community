package activitypub

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateURIBlockedIPLiteral(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		wantErr bool
	}{
		{"loopback IP literal", "https://127.0.0.1/path", true},
		{"private IP literal", "https://10.0.0.1/path", true},
		{"link-local IP literal", "https://169.254.169.254/path", true},
		{"empty scheme", "gopher://localhost", true},
		{"empty host", "https://", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURI(tt.uri)
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, IsSSRFBlocked(err))
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestIsSSRFBlockedNil(t *testing.T) {
	assert.False(t, IsSSRFBlocked(nil))
}

func TestIsSSRFBlockedNonSSRFError(t *testing.T) {
	err := errors.New("some other error")
	assert.False(t, IsSSRFBlocked(err))
}

func TestIsSSRFBlockedSSRFError(t *testing.T) {
	err := errors.New("ssrf: blocked connection to internal address")
	assert.True(t, IsSSRFBlocked(err))
}

func TestContainsSubstr(t *testing.T) {
	assert.True(t, containsSubstr("hello world", "world"))
	assert.True(t, containsSubstr("hello", ""))    // empty substring matches
	assert.False(t, containsSubstr("hi", "hello")) // s shorter than sub after empty check
	assert.True(t, containsSubstr("abcdef", "cde"))
	assert.False(t, containsSubstr("abcdef", "xyz"))
}

func TestSSRFSafeDialContextInvalidAddress(t *testing.T) {
	// Invalid address (no port) should return an error
	_, err := ssrfSafeDialContext(context.Background(), "tcp", "no-port")
	require.Error(t, err)
}

func TestSSRFSafeDialContextIPLiteral(t *testing.T) {
	// An IP literal that is banned should be rejected
	_, err := ssrfSafeDialContext(context.Background(), "tcp", "192.168.1.1:80")
	require.Error(t, err)
	assert.True(t, IsSSRFBlocked(err))
}

func TestIsBannedIPv6Addresses(t *testing.T) {
	// Pure IPv6 addresses
	assert.True(t, isBannedIP(net.ParseIP("::1")))
	assert.True(t, isBannedIP(net.ParseIP("fe80::1")))
	assert.True(t, isBannedIP(net.ParseIP("fc00::1")))
	assert.False(t, isBannedIP(net.ParseIP("2606:4700:4700::1111")))
}
