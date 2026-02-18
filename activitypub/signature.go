package activitypub

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/log"
)

// SignWithRSA signs data using RSA-SHA256
func SignWithRSA(privateKey *rsa.PrivateKey, data []byte) ([]byte, error) {
	hash := sha256.Sum256(data)
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return nil, fmt.Errorf("signing with RSA: %w", err)
	}
	return signature, nil
}

// VerifyWithRSA verifies an RSA-SHA256 signature
func VerifyWithRSA(publicKey *rsa.PublicKey, data, signature []byte) error {
	hash := sha256.Sum256(data)
	return rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, hash[:], signature)
}

// ParsePublicKey parses a PEM-encoded public key
func ParsePublicKey(pemStr string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("failed to parse PEM block")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing public key: %w", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not an RSA public key")
	}

	return rsaPub, nil
}

// SignatureHeader represents parsed HTTP Signature header
type SignatureHeader struct {
	KeyID     string
	Algorithm string
	Headers   []string
	Signature string
}

// ParseSignatureHeader parses the Signature header
func ParseSignatureHeader(header string) (*SignatureHeader, error) {
	sig := &SignatureHeader{}
	parts := strings.Split(header, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}

		key := kv[0]
		value := strings.Trim(kv[1], `"`)

		switch key {
		case "keyId":
			sig.KeyID = value
		case "algorithm":
			sig.Algorithm = value
		case "headers":
			sig.Headers = strings.Split(value, " ")
		case "signature":
			sig.Signature = value
		}
	}

	if sig.KeyID == "" || sig.Signature == "" {
		return nil, fmt.Errorf("missing required signature fields")
	}

	return sig, nil
}

// BuildSigningString builds the string to be signed/verified
func BuildSigningString(req *http.Request, headers []string) (string, error) {
	var lines []string

	for _, header := range headers {
		switch strings.ToLower(header) {
		case "(request-target)":
			method := strings.ToLower(req.Method)
			path := req.URL.Path
			if req.URL.RawQuery != "" {
				path += "?" + req.URL.RawQuery
			}
			lines = append(lines, fmt.Sprintf("(request-target): %s %s", method, path))
		case "host":
			lines = append(lines, fmt.Sprintf("host: %s", req.Host))
		case "date":
			lines = append(lines, fmt.Sprintf("date: %s", req.Header.Get("Date")))
		case "digest":
			lines = append(lines, fmt.Sprintf("digest: %s", req.Header.Get("Digest")))
		default:
			value := req.Header.Get(header)
			lines = append(lines, fmt.Sprintf("%s: %s", strings.ToLower(header), value))
		}
	}

	return strings.Join(lines, "\n"), nil
}

// VerifyRequest verifies an HTTP request signature
func VerifyRequest(req *http.Request, publicKey *rsa.PublicKey) error {
	sigHeader := req.Header.Get("Signature")
	if sigHeader == "" {
		return fmt.Errorf("no signature header")
	}

	sig, err := ParseSignatureHeader(sigHeader)
	if err != nil {
		return fmt.Errorf("parsing signature header: %w", err)
	}

	signingString, err := BuildSigningString(req, sig.Headers)
	if err != nil {
		return fmt.Errorf("building signing string: %w", err)
	}

	signature, err := base64.StdEncoding.DecodeString(sig.Signature)
	if err != nil {
		return fmt.Errorf("decoding signature: %w", err)
	}

	if err := VerifyWithRSA(publicKey, []byte(signingString), signature); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}

// PublicKeyCache caches public keys with TTL
type PublicKeyCache struct {
	keys  map[string]*cachedKey
	mu    sync.RWMutex
	ttl   time.Duration
}

type cachedKey struct {
	key       *rsa.PublicKey
	expiresAt time.Time
}

// Global public key cache
var globalKeyCache = &PublicKeyCache{
	keys: make(map[string]*cachedKey),
	ttl:  1 * time.Hour,
}

// Get retrieves a cached key
func (c *PublicKeyCache) Get(keyID string) (*rsa.PublicKey, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	if cached, ok := c.keys[keyID]; ok {
		if time.Now().Before(cached.expiresAt) {
			return cached.key, true
		}
	}
	return nil, false
}

// Set stores a key in cache
func (c *PublicKeyCache) Set(keyID string, key *rsa.PublicKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	c.keys[keyID] = &cachedKey{
		key:       key,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// SignatureVerifier provides complete HTTP signature verification
type SignatureVerifier struct {
	cache *PublicKeyCache
}

// NewSignatureVerifier creates a new signature verifier
func NewSignatureVerifier() *SignatureVerifier {
	return &SignatureVerifier{
		cache: globalKeyCache,
	}
}

// VerifyRequestWithKeyID verifies a request by fetching the public key from the actor's keyId
func (v *SignatureVerifier) VerifyRequestWithKeyID(req *http.Request, body []byte) error {
	sigHeader := req.Header.Get("Signature")
	if sigHeader == "" {
		return fmt.Errorf("no signature header")
	}

	sig, err := ParseSignatureHeader(sigHeader)
	if err != nil {
		return fmt.Errorf("parsing signature header: %w", err)
	}

	// Get public key (from cache or fetch)
	publicKey, err := v.getPublicKey(req.Context(), sig.KeyID)
	if err != nil {
		return fmt.Errorf("getting public key: %w", err)
	}

	// Verify digest if present
	if digest := req.Header.Get("Digest"); digest != "" && body != nil {
		if err := verifyDigest(body, digest); err != nil {
			return fmt.Errorf("digest verification failed: %w", err)
		}
	}

	// Build and verify signature
	signingString, err := BuildSigningString(req, sig.Headers)
	if err != nil {
		return fmt.Errorf("building signing string: %w", err)
	}

	signature, err := base64.StdEncoding.DecodeString(sig.Signature)
	if err != nil {
		return fmt.Errorf("decoding signature: %w", err)
	}

	if err := VerifyWithRSA(publicKey, []byte(signingString), signature); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}

// getPublicKey retrieves a public key from cache or fetches it
func (v *SignatureVerifier) getPublicKey(ctx context.Context, keyID string) (*rsa.PublicKey, error) {
	// Check cache first
	if key, ok := v.cache.Get(keyID); ok {
		return key, nil
	}

	// Fetch the actor to get the public key
	client := NewClient(nil, "")
	
	// KeyID is typically the actor URI with #main-key suffix
	actorURI := strings.TrimSuffix(keyID, "#main-key")
	
	actor, err := client.FetchActor(ctx, actorURI)
	if err != nil {
		return nil, fmt.Errorf("fetching actor: %w", err)
	}

	// Parse the public key
	if actor.PublicKey == nil || actor.PublicKey.PublicKeyPem == "" {
		return nil, fmt.Errorf("actor has no public key")
	}

	publicKey, err := ParsePublicKey(actor.PublicKey.PublicKeyPem)
	if err != nil {
		return nil, fmt.Errorf("parsing public key: %w", err)
	}

	// Cache the key
	v.cache.Set(keyID, publicKey)
	log.Info("Cached public key for %s", keyID)

	return publicKey, nil
}

// verifyDigest verifies the SHA-256 digest header
func verifyDigest(body []byte, digestHeader string) error {
	// Parse digest header (format: SHA-256=base64hash)
	parts := strings.SplitN(digestHeader, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid digest format")
	}

	if parts[0] != "SHA-256" {
		return fmt.Errorf("unsupported digest algorithm: %s", parts[0])
	}

	expectedHash, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("decoding digest: %w", err)
	}

	// Calculate actual hash
	actualHash := sha256.Sum256(body)

	// Compare
	if !equalSlices(expectedHash, actualHash[:]) {
		return fmt.Errorf("digest mismatch")
	}

	return nil
}

// equalSlices compares two byte slices
func equalSlices(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// VerifyActivitySignature is a convenience function for verifying inbox requests
func VerifyActivitySignature(c interface{ GetHeader(string) string }, body []byte) (string, error) {
	// Type assert to get the underlying request
	var req *http.Request
	
	// Gin context has a Request method
	if ginCtx, ok := c.(interface{ Request() *http.Request }); ok {
		req = ginCtx.Request()
	} else {
		return "", fmt.Errorf("unsupported context type")
	}

	sigHeader := req.Header.Get("Signature")
	if sigHeader == "" {
		return "", fmt.Errorf("no signature header")
	}

	sig, err := ParseSignatureHeader(sigHeader)
	if err != nil {
		return "", fmt.Errorf("parsing signature header: %w", err)
	}

	verifier := NewSignatureVerifier()
	if err := verifier.VerifyRequestWithKeyID(req, body); err != nil {
		return "", err
	}

	// Extract actor URI from keyId
	actorURI := strings.TrimSuffix(sig.KeyID, "#main-key")
	return actorURI, nil
}
