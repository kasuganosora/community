package utils

import (
	"crypto/md5"  //nolint:gosec // G501: reviewed and safe
	"crypto/sha1" //nolint:gosec // G505: reviewed and safe
	"crypto/sha256"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHashString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		hashFunc string
		expected string
	}{
		{
			name:     "MD5 hash of 'hello'",
			input:    "hello",
			hashFunc: "md5",
			expected: "5d41402abc4b2a76b9719d911017c592",
		},
		{
			name:     "SHA1 hash of 'hello'",
			input:    "hello",
			hashFunc: "sha1",
			expected: "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d",
		},
		{
			name:     "SHA256 hash of 'hello'",
			input:    "hello",
			hashFunc: "sha256",
			expected: "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
		},
		{
			name:     "MD5 hash of empty string",
			input:    "",
			hashFunc: "md5",
			expected: "d41d8cd98f00b204e9800998ecf8427e",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result string

			switch tt.hashFunc {
			case "md5":
				result = Hash(md5.New(), tt.input) //nolint:gosec // G401: reviewed and safe
			case "sha1":
				result = Hash(sha1.New(), tt.input) //nolint:gosec // G401: reviewed and safe
			case "sha256":
				result = Hash(sha256.New(), tt.input)
			}

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHashBytes(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		hashFunc string
		expected string
	}{
		{
			name:     "MD5 hash of []byte('hello')",
			input:    []byte("hello"),
			hashFunc: "md5",
			expected: "5d41402abc4b2a76b9719d911017c592",
		},
		{
			name:     "SHA256 hash of []byte('world')",
			input:    []byte("world"),
			hashFunc: "sha256",
			expected: "486ea46224d1bb4fb680f34f7c9ad96a8f24ec88be73ea8e5a6c65260e9cb8a7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result string

			switch tt.hashFunc {
			case "md5":
				result = Hash(md5.New(), tt.input) //nolint:gosec // G401: reviewed and safe
			case "sha256":
				result = Hash(sha256.New(), tt.input)
			}

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHashPanic(t *testing.T) {
	// Test that Hash panics with unsupported type
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected Hash to panic with unsupported type")
		}
	}()

	Hash(md5.New(), 123) //nolint:gosec // test code: intentionally testing weak hash
}
