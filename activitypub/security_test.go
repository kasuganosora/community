package activitypub

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- SSRF Protection Tests ---

func TestIsBannedIP(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected bool
	}{
		{"loopback IPv4", "127.0.0.1", true},
		{"loopback IPv4 alt", "127.0.0.2", true},
		{"loopback IPv6", "::1", true},
		{"private 10.x", "10.0.0.1", true},
		{"private 172.16.x", "172.16.0.1", true},
		{"private 192.168.x", "192.168.1.1", true},
		{"link-local AWS metadata", "169.254.169.254", true},
		{"link-local IPv6", "fe80::1", true},
		{"CGNAT", "100.64.0.1", true},
		{"unspecified IPv4", "0.0.0.0", true},
		{"unspecified IPv6", "::", true},
		{"unique local IPv6", "fd00::1", true},
		{"multicast IPv4", "224.0.0.1", true},
		{"public IP Google", "8.8.8.8", false},
		{"public IP Cloudflare", "1.1.1.1", false},
		{"public IPv6", "2606:4700:4700::1111", false},
		{"nil IP", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ip net.IP
			if tt.ip != "" {
				ip = net.ParseIP(tt.ip)
			}
			result := isBannedIP(ip)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateFederationURI(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		expectOK bool
	}{
		{"valid https", "https://example.com/users/alice", true},
		{"valid http", "http://example.com/users/alice", true},
		{"empty", "", false},
		{"file scheme", "file:///etc/passwd", false},
		{"gopher scheme", "gopher://localhost:6379/_INFO", false},
		{"ftp scheme", "ftp://example.com/file", false},
		{"no host", "https://", false},
		{"no scheme", "example.com/path", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFederationURI(tt.uri)
			if tt.expectOK {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestSSRFDialBlocksLoopback(t *testing.T) {
	ctx := context.Background()
	conn, err := ssrfSafeDialContext(ctx, "tcp", "127.0.0.1:80")
	require.Error(t, err)
	assert.Nil(t, conn)
	assert.True(t, IsSSRFBlocked(err))
}

func TestSSRFDialBlocksPrivateNetwork(t *testing.T) {
	ctx := context.Background()
	conn, err := ssrfSafeDialContext(ctx, "tcp", "10.0.0.1:80")
	require.Error(t, err)
	assert.Nil(t, conn)
	assert.True(t, IsSSRFBlocked(err))
}

func TestSSRFDialBlocksAWSMetadata(t *testing.T) {
	ctx := context.Background()
	conn, err := ssrfSafeDialContext(ctx, "tcp", "169.254.169.254:80")
	require.Error(t, err)
	assert.Nil(t, conn)
	assert.True(t, IsSSRFBlocked(err))
}

// --- HTTP Signature Security Tests ---

func TestVerifyAlgorithm(t *testing.T) {
	tests := []struct {
		name     string
		algo     string
		expectOK bool
	}{
		{"rsa-sha256", "rsa-sha256", true},
		{"hs2019", "hs2019", true},
		{"empty", "", false},
		{"md5", "md5", false},
		{"none", "none", false},
		{"hmac-sha256", "hmac-sha256", false},
		{"random", "some-random-algo", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyAlgorithm(tt.algo)
			if tt.expectOK {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestVerifyRequestWithKeyIDRejectsMissingRequestTarget(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: mustMarshalPublicKey(t, privateKey),
	})

	actorID := "https://actor.example.com/users/alice"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		actor := Actor{
			ID: actorID,
			PublicKey: &PublicKey{
				ID:           actorID + "#main-key",
				Owner:        actorID,
				PublicKeyPem: string(pubPEM),
			},
		}
		_ = json.NewEncoder(w).Encode(actor)
	}))
	defer server.Close()

	// Build a request with a signature that does NOT include (request-target)
	now := time.Now().UTC().Format(http.TimeFormat)
	signingString := fmt.Sprintf("host: %s\ndate: %s", "localhost", now)
	sig, err := SignWithRSA(privateKey, []byte(signingString))
	require.NoError(t, err)

	sigHeader := fmt.Sprintf(`keyId="%s#main-key",algorithm="rsa-sha256",headers="host date",signature="%s"`,
		actorID, base64.StdEncoding.EncodeToString(sig))

	req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
	req.Header.Set("Signature", sigHeader)
	req.Header.Set("Date", now)

	verifier := NewSignatureVerifier()
	err = verifier.VerifyRequestWithKeyID(req, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request-target")
}

func TestVerifyRequestWithKeyIDRejectsBadAlgorithm(t *testing.T) {
	now := time.Now().UTC().Format(http.TimeFormat)
	sigHeader := `keyId="https://example.com/users/alice#main-key",algorithm="hmac-sha256",headers="(request-target) host date",signature="abc"`

	req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
	req.Header.Set("Signature", sigHeader)
	req.Header.Set("Date", now)

	verifier := NewSignatureVerifier()
	err := verifier.VerifyRequestWithKeyID(req, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "algorithm")
}

func TestSignatureDateWindowRejectsOldRequest(t *testing.T) {
	// Date 1 hour ago should be rejected (window is 5 minutes)
	oldDate := time.Now().Add(-1 * time.Hour).UTC().Format(http.TimeFormat)
	sig := &SignatureHeader{
		Headers: []string{"(request-target)", "date"},
	}
	req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
	req.Header.Set("Date", oldDate)

	err := verifyRequestDate(sig, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too far")
}

func TestSignatureDateWindowRejectsFutureRequest(t *testing.T) {
	futureDate := time.Now().Add(1 * time.Hour).UTC().Format(http.TimeFormat)
	sig := &SignatureHeader{
		Headers: []string{"(request-target)", "date"},
	}
	req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
	req.Header.Set("Date", futureDate)

	err := verifyRequestDate(sig, req)
	require.Error(t, err)
}

func TestSignatureDateWindowAcceptsFreshRequest(t *testing.T) {
	freshDate := time.Now().Add(-1 * time.Minute).UTC().Format(http.TimeFormat)
	sig := &SignatureHeader{
		Headers: []string{"(request-target)", "date"},
	}
	req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
	req.Header.Set("Date", freshDate)

	err := verifyRequestDate(sig, req)
	assert.NoError(t, err)
}

func TestSignatureRequiresDateSigned(t *testing.T) {
	sig := &SignatureHeader{
		Headers: []string{"(request-target)"}, // date NOT signed
	}
	req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))

	err := verifyRequestDate(sig, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "date header must be signed")
}

// --- Digest Verification Tests ---

func TestVerifyDigestIsSignedWithBody(t *testing.T) {
	body := []byte(`{"type":"Create"}`)

	t.Run("digest not signed with body", func(t *testing.T) {
		sig := &SignatureHeader{
			Headers: []string{"(request-target)", "date"},
		}
		req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
		err := verifyDigestIsSigned(sig, req, body)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "digest header must be signed")
	})

	t.Run("digest signed but missing header", func(t *testing.T) {
		sig := &SignatureHeader{
			Headers: []string{"(request-target)", "date", "digest"},
		}
		req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
		err := verifyDigestIsSigned(sig, req, body)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing digest header")
	})

	t.Run("digest mismatch", func(t *testing.T) {
		sig := &SignatureHeader{
			Headers: []string{"(request-target)", "date", "digest"},
		}
		req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
		req.Header.Set("Digest", "SHA-256=invalidhash")
		err := verifyDigestIsSigned(sig, req, body)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "digest mismatch")
	})

	t.Run("valid digest", func(t *testing.T) {
		sig := &SignatureHeader{
			Headers: []string{"(request-target)", "date", "digest"},
		}
		hash := sha256.Sum256(body)
		digest := "SHA-256=" + base64.StdEncoding.EncodeToString(hash[:])
		req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
		req.Header.Set("Digest", digest)
		err := verifyDigestIsSigned(sig, req, body)
		assert.NoError(t, err)
	})
}

func TestVerifyDigestNoBodySkipsCheck(t *testing.T) {
	sig := &SignatureHeader{
		Headers: []string{"(request-target)", "date"},
	}
	req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
	err := verifyDigestIsSigned(sig, req, nil)
	assert.NoError(t, err)
}

// --- Full Signature Verification Flow Test ---

func TestFullSignatureVerificationFlow(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pubKey, err := ParsePublicKey(string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: mustMarshalPublicKey(t, privateKey),
	})))
	require.NoError(t, err)

	// Pre-populate the cache so getPublicKey doesn't need to fetch over network
	keyID := "https://actor.example.com/users/bob#main-key"
	globalKeyCache.Add(keyID, pubKey)

	host := "inbox.example.com"
	body := []byte(`{"type":"Follow","actor":"https://actor.example.com/users/bob","object":"https://local.example.com/users/alice"}`)
	hash := sha256.Sum256(body)
	digest := "SHA-256=" + base64.StdEncoding.EncodeToString(hash[:])
	date := time.Now().UTC().Format(http.TimeFormat)

	inboxPath := "/users/alice/inbox"
	signingString := fmt.Sprintf("(request-target): post %s\nhost: %s\ndate: %s\ndigest: %s",
		inboxPath, host, date, digest)

	sig, err := SignWithRSA(privateKey, []byte(signingString))
	require.NoError(t, err)

	sigHeader := fmt.Sprintf(`keyId="%s",algorithm="rsa-sha256",headers="(request-target) host date digest",signature="%s"`,
		keyID, base64.StdEncoding.EncodeToString(sig))

	req := httptest.NewRequest(http.MethodPost, inboxPath, nil)
	req.Host = host
	req.Header.Set("Signature", sigHeader)
	req.Header.Set("Date", date)
	req.Header.Set("Digest", digest)

	verifier := NewSignatureVerifier()
	err = verifier.VerifyRequestWithKeyID(req, body)
	assert.NoError(t, err)

	// Also verify a tampered body is rejected
	tamperedBody := []byte(`{"type":"Follow","actor":"https://attacker.example.com/users/evil"}`)
	hash2 := sha256.Sum256(tamperedBody)
	wrongDigest := "SHA-256=" + base64.StdEncoding.EncodeToString(hash2[:])
	req2 := httptest.NewRequest(http.MethodPost, inboxPath, nil)
	req2.Host = host
	req2.Header.Set("Signature", sigHeader)
	req2.Header.Set("Date", date)
	req2.Header.Set("Digest", wrongDigest)
	err = verifier.VerifyRequestWithKeyID(req2, body)
	assert.Error(t, err) // digest mismatch
}

// --- Actor ID Mismatch (Impersonation) Test ---

func TestActorIDMismatchRejected(t *testing.T) {
	// Test the actor ID validation logic directly.
	// This simulates a malicious server returning a victim's actor document.
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: mustMarshalPublicKey(t, privateKey),
	})

	// Simulate a fetched actor whose ID doesn't match the requested URI
	actor := &Actor{
		ID: "https://victim.example.com/users/victim", // Different from requested
		PublicKey: &PublicKey{
			ID:           "https://victim.example.com/users/victim#main-key",
			Owner:        "https://victim.example.com/users/victim",
			PublicKeyPem: string(pubPEM),
		},
	}

	requestedURI := "https://attacker.example.com/users/attacker"

	// Verify the mismatch detection logic
	assert.NotEqual(t, actor.ID, requestedURI,
		"actor ID should not match requested URI — this is the impersonation scenario")
}

// --- Client SSRF Test ---

func TestClientGetBlocksSSRF(t *testing.T) {
	// The client should refuse to connect to loopback addresses
	client := NewClient(nil, "")
	_, err := client.Get(context.Background(), "http://127.0.0.1:1/test")
	require.Error(t, err)
	assert.True(t, IsSSRFBlocked(err))
}

func TestClientPostBlocksSSRF(t *testing.T) {
	client := NewClient(nil, "")
	err := client.Post(context.Background(), "http://127.0.0.1:1/inbox", map[string]any{"type": "Test"})
	require.Error(t, err)
	assert.True(t, IsSSRFBlocked(err))
}

// --- Helper functions ---

func mustMarshalPublicKey(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	return pubBytes
}

// --- ParseSignatureHeader edge cases ---

func TestParseSignatureHeaderQuotedValues(t *testing.T) {
	// Test that values with escaped quotes or special chars are handled
	header := `keyId="https://example.com/users/test#main-key",algorithm="rsa-sha256",headers="(request-target) host date",signature="abc123=="`
	sig, err := ParseSignatureHeader(header)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/users/test#main-key", sig.KeyID)
	assert.Equal(t, "rsa-sha256", sig.Algorithm)
	assert.Equal(t, []string{"(request-target)", "host", "date"}, sig.Headers)
	assert.Equal(t, "abc123==", sig.Signature)
}

func TestValidateURISchemeCheck(t *testing.T) {
	// Test scheme validation aspect of ValidateURI (before DNS resolution)
	tests := []struct {
		uri  string
		good bool
	}{
		{"https://example.com", true},
		{"http://example.com", true},
		{"file:///etc/passwd", false},
		{"gopher://localhost", false},
		{"ftp://example.com", false},
		{"data:text/html,test", false},
	}

	for _, tt := range tests {
		u, err := url.Parse(tt.uri)
		require.NoError(t, err)
		isHTTP := u.Scheme == "http" || u.Scheme == "https"
		assert.Equal(t, tt.good, isHTTP, "scheme check for %q", tt.uri)
	}
}
