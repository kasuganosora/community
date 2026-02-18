package activitypub

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSignWithRSA(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.NoError(t, err)

	data := []byte("test data to sign")
	signature, err := SignWithRSA(privateKey, data)
	assert.NoError(t, err)
	assert.NotEmpty(t, signature)

	// Verify the signature
	hash := sha256.Sum256(data)
	err = rsa.VerifyPKCS1v15(&privateKey.PublicKey, crypto.SHA256, hash[:], signature)
	assert.NoError(t, err)
}

func TestVerifyWithRSA(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.NoError(t, err)

	data := []byte("test data to verify")
	signature, err := SignWithRSA(privateKey, data)
	assert.NoError(t, err)

	// Verify with correct public key
	err = VerifyWithRSA(&privateKey.PublicKey, data, signature)
	assert.NoError(t, err)

	// Verify with wrong data
	err = VerifyWithRSA(&privateKey.PublicKey, []byte("wrong data"), signature)
	assert.Error(t, err)

	// Verify with wrong signature
	wrongSig := make([]byte, len(signature))
	copy(wrongSig, signature)
	wrongSig[0] ^= 0xFF
	err = VerifyWithRSA(&privateKey.PublicKey, data, wrongSig)
	assert.Error(t, err)
}

func TestParsePublicKey(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.NoError(t, err)

	// Generate PEM encoded public key
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	assert.NoError(t, err)
	pemStr := string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubKeyBytes,
	}))

	parsed, err := ParsePublicKey(pemStr)
	assert.NoError(t, err)
	assert.NotNil(t, parsed)

	// Test with invalid PEM
	_, err = ParsePublicKey("invalid pem")
	assert.Error(t, err)

	// Test with non-RSA key
	_, err = ParsePublicKey("-----BEGIN PUBLIC KEY-----\nYW55IGNhcm5hbCBwbGVhc3VyZQ==\n-----END PUBLIC KEY-----")
	assert.Error(t, err)
}

func TestParseSignatureHeader(t *testing.T) {
	tests := []struct {
		name      string
		header    string
		expectErr bool
		expected  *SignatureHeader
	}{
		{
			name:   "valid signature header",
			header: `keyId="https://example.com/users/test#main-key",algorithm="rsa-sha256",headers="(request-target) host date digest",signature="abc123"`,
			expectErr: false,
			expected: &SignatureHeader{
				KeyID:     "https://example.com/users/test#main-key",
				Algorithm: "rsa-sha256",
				Headers:   []string{"(request-target)", "host", "date", "digest"},
				Signature: "abc123",
			},
		},
		{
			name:      "missing keyId",
			header:    `algorithm="rsa-sha256",signature="abc123"`,
			expectErr: true,
		},
		{
			name:      "missing signature",
			header:    `keyId="https://example.com/users/test#main-key",algorithm="rsa-sha256"`,
			expectErr: true,
		},
		{
			name:      "empty header",
			header:    "",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseSignatureHeader(tt.header)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected.KeyID, result.KeyID)
				assert.Equal(t, tt.expected.Algorithm, result.Algorithm)
				assert.Equal(t, tt.expected.Signature, result.Signature)
			}
		})
	}
}

func TestBuildSigningString(t *testing.T) {
	req := &http.Request{
		Method: "POST",
		URL: &url.URL{
			Path:     "/users/test/inbox",
			RawQuery: "foo=bar",
		},
		Host: "example.com",
		Header: http.Header{
			"Date":    []string{"Tue, 01 Jan 2024 00:00:00 GMT"},
			"Digest":  []string{"SHA-256=abc123"},
		},
	}

	tests := []struct {
		name     string
		headers  []string
		expected string
	}{
		{
			name:    "request-target only",
			headers: []string{"(request-target)"},
			expected: "(request-target): post /users/test/inbox?foo=bar",
		},
		{
			name:    "standard headers",
			headers: []string{"(request-target)", "host", "date", "digest"},
			expected: "(request-target): post /users/test/inbox?foo=bar\nhost: example.com\ndate: Tue, 01 Jan 2024 00:00:00 GMT\ndigest: SHA-256=abc123",
		},
		{
			name:    "host header",
			headers: []string{"host"},
			expected: "host: example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := BuildSigningString(req, tt.headers)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestVerifyRequest(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.NoError(t, err)

	// Create signing string
	signingString := "(request-target): post /users/test/inbox\nhost: example.com\ndate: Tue, 01 Jan 2024 00:00:00 GMT"
	signature, err := SignWithRSA(privateKey, []byte(signingString))
	assert.NoError(t, err)

	sigHeader := fmt.Sprintf(`keyId="https://example.com/users/test#main-key",algorithm="rsa-sha256",headers="(request-target) host date",signature="%s"`, base64.StdEncoding.EncodeToString(signature))

	req := &http.Request{
		Method: "POST",
		URL:    &url.URL{Path: "/users/test/inbox"},
		Host:   "example.com",
		Header: http.Header{
			"Signature": []string{sigHeader},
			"Date":      []string{"Tue, 01 Jan 2024 00:00:00 GMT"},
		},
	}

	err = VerifyRequest(req, &privateKey.PublicKey)
	assert.NoError(t, err)

	// Test with no signature header
	reqNoSig := &http.Request{
		Method: "POST",
		URL:    &url.URL{Path: "/users/test/inbox"},
		Header: http.Header{},
	}
	err = VerifyRequest(reqNoSig, &privateKey.PublicKey)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no signature header")
}

func TestPublicKeyCache(t *testing.T) {
	cache := &PublicKeyCache{
		keys: make(map[string]*cachedKey),
		ttl:  100 * time.Millisecond,
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.NoError(t, err)

	keyID := "https://example.com/users/test#main-key"

	// Test cache miss
	_, ok := cache.Get(keyID)
	assert.False(t, ok)

	// Set key
	cache.Set(keyID, &privateKey.PublicKey)

	// Test cache hit
	cached, ok := cache.Get(keyID)
	assert.True(t, ok)
	assert.NotNil(t, cached)

	// Wait for TTL to expire
	time.Sleep(150 * time.Millisecond)

	// Test cache expired
	_, ok = cache.Get(keyID)
	assert.False(t, ok)
}

func TestVerifyDigest(t *testing.T) {
	tests := []struct {
		name      string
		body      []byte
		digest    string
		expectErr bool
	}{
		{
			name:      "valid digest",
			body:      []byte("test body"),
			digest:    "SHA-256=" + base64.StdEncoding.EncodeToString(sha256Sum([]byte("test body"))),
			expectErr: false,
		},
		{
			name:      "invalid digest",
			body:      []byte("test body"),
			digest:    "SHA-256=" + base64.StdEncoding.EncodeToString([]byte("invalid")),
			expectErr: true,
		},
		{
			name:      "unsupported algorithm",
			body:      []byte("test body"),
			digest:    "MD5=abc123",
			expectErr: true,
		},
		{
			name:      "invalid format",
			body:      []byte("test body"),
			digest:    "invalid",
			expectErr: true,
		},
		{
			name:      "invalid base64",
			body:      []byte("test body"),
			digest:    "SHA-256=not-base64!",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyDigest(tt.body, tt.digest)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestStripPrivacyFields(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name: "removes bto and bcc",
			input: map[string]interface{}{
				"type":    "Create",
				"to":      []string{"https://www.w3.org/ns/activitystreams#Public"},
				"bto":     []string{"secret@user.com"},
				"bcc":     []string{"hidden@user.com"},
				"content": "test",
			},
			expected: map[string]interface{}{
				"type":    "Create",
				"to":      []string{"https://www.w3.org/ns/activitystreams#Public"},
				"content": "test",
			},
		},
		{
			name: "removes bto/bcc from nested object",
			input: map[string]interface{}{
				"type": "Create",
				"object": map[string]interface{}{
					"type":    "Note",
					"content": "test",
					"bto":     []string{"secret@user.com"},
					"bcc":     []string{"hidden@user.com"},
				},
			},
			expected: map[string]interface{}{
				"type": "Create",
				"object": map[string]interface{}{
					"type":    "Note",
					"content": "test",
				},
			},
		},
		{
			name: "no bto/bcc fields",
			input: map[string]interface{}{
				"type":    "Create",
				"content": "test",
			},
			expected: map[string]interface{}{
				"type":    "Create",
				"content": "test",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripPrivacyFields(tt.input)

			// Check bto and bcc are removed
			_, hasBto := result["bto"]
			_, hasBcc := result["bcc"]
			assert.False(t, hasBto)
			assert.False(t, hasBcc)

			// Check other fields preserved
			for k, v := range tt.expected {
				if k == "object" {
					objResult := result[k].(map[string]interface{})
					objExpected := v.(map[string]interface{})
					for ok, ov := range objExpected {
						assert.Equal(t, ov, objResult[ok])
					}
				} else {
					assert.Equal(t, v, result[k])
				}
			}
		})
	}
}

func TestIsValidActivityPubContentType(t *testing.T) {
	tests := []struct {
		contentType string
		expected    bool
	}{
		{"application/activity+json", true},
		{"application/ld+json", true},
		{"application/ld+json; profile=\"https://www.w3.org/ns/activitystreams\"", true},
		{"application/json", true},
		{"application/activity+json; charset=utf-8", true},
		{"text/html", false},
		{"application/xml", false},
		{"", false},
		{"APPLICATION/ACTIVITY+JSON", true}, // case insensitive
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			result := isValidActivityPubContentType(tt.contentType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEqualSlices(t *testing.T) {
	tests := []struct {
		a, b     []byte
		expected bool
	}{
		{[]byte{1, 2, 3}, []byte{1, 2, 3}, true},
		{[]byte{1, 2, 3}, []byte{1, 2, 4}, false},
		{[]byte{1, 2}, []byte{1, 2, 3}, false},
		{[]byte{}, []byte{}, true},
		{nil, nil, true},
		{[]byte{1}, nil, false},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := equalSlices(tt.a, tt.b)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Helper function
func sha256Sum(data []byte) []byte {
	hash := sha256.Sum256(data)
	return hash[:]
}
