package activitypub

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/kasuganosora/community/log"
)

// SignWithRSA signs data using RSA-SHA256.
func SignWithRSA(privateKey *rsa.PrivateKey, data []byte) ([]byte, error) {
	hash := sha256.Sum256(data)

	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return nil, fmt.Errorf("signing with RSA: %w", err)
	}

	return signature, nil
}

// VerifyWithRSA verifies an RSA-SHA256 signature.
func VerifyWithRSA(publicKey *rsa.PublicKey, data, signature []byte) error {
	hash := sha256.Sum256(data)

	return rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, hash[:], signature)
}

// ParsePublicKey parses a PEM-encoded public key.
func ParsePublicKey(pemStr string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("failed to parse PEM block")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing public key: %w", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("not an RSA public key")
	}

	return rsaPub, nil
}

// SignatureHeader represents parsed HTTP Signature header.
type SignatureHeader struct {
	KeyID     string
	Algorithm string
	Headers   []string
	Signature string
}

// ParseSignatureHeader parses the Signature header.
func ParseSignatureHeader(header string) (*SignatureHeader, error) {
	sig := &SignatureHeader{}
	parts := strings.SplitSeq(header, ",")

	for part := range parts {
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
		return nil, errors.New("missing required signature fields")
	}

	return sig, nil
}

// BuildSigningString builds the string to be signed/verified.
func BuildSigningString(req *http.Request, headers []string) (string, error) {
	var lines []string

	for _, header := range headers {
		switch strings.ToLower(header) {
		case "(request-target)": //nolint:goconst
			method := strings.ToLower(req.Method)

			path := req.URL.Path
			if req.URL.RawQuery != "" {
				path += "?" + req.URL.RawQuery
			}

			lines = append(lines, fmt.Sprintf("(request-target): %s %s", method, path))
		case "host":
			lines = append(lines, "host: "+req.Host)
		case "date":
			lines = append(lines, "date: "+req.Header.Get("Date"))
		case "digest":
			lines = append(lines, "digest: "+req.Header.Get("Digest"))
		default:
			value := req.Header.Get(header)
			lines = append(lines, fmt.Sprintf("%s: %s", strings.ToLower(header), value))
		}
	}

	return strings.Join(lines, "\n"), nil
}

// VerifyRequest verifies an HTTP request signature.
func VerifyRequest(req *http.Request, publicKey *rsa.PublicKey) error {
	sigHeader := req.Header.Get("Signature")
	if sigHeader == "" {
		return errors.New("no signature header")
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

// Global public key cache with LRU eviction and TTL.
var globalKeyCache = expirable.NewLRU[string, *rsa.PublicKey](10000, nil, 1*time.Hour)

// SignatureVerifier provides complete HTTP signature verification.
type SignatureVerifier struct {
	cache *expirable.LRU[string, *rsa.PublicKey]
}

// NewSignatureVerifier creates a new signature verifier.
func NewSignatureVerifier() *SignatureVerifier {
	return &SignatureVerifier{
		cache: globalKeyCache,
	}
}

// VerifyRequestWithKeyID verifies a request by fetching the public key from the actor's keyId.
func (v *SignatureVerifier) VerifyRequestWithKeyID(req *http.Request, body []byte) error {
	sigHeader := req.Header.Get("Signature")
	if sigHeader == "" {
		return errors.New("no signature header")
	}

	sig, err := ParseSignatureHeader(sigHeader)
	if err != nil {
		return fmt.Errorf("parsing signature header: %w", err)
	}

	// C2: Verify request date is within acceptable time window (replay protection)
	if err := verifyRequestDate(sig, req); err != nil {
		return fmt.Errorf("request date verification failed: %w", err)
	}

	// Get public key (from cache or fetch)
	publicKey, err := v.getPublicKey(req.Context(), sig.KeyID)
	if err != nil {
		return fmt.Errorf("getting public key: %w", err)
	}

	// C3: Enforce digest is signed when body is present
	if err := verifyDigestIsSigned(sig, req, body); err != nil {
		return fmt.Errorf("digest signature verification failed: %w", err)
	}

	// Verify digest if present
	if digest := req.Header.Get("Digest"); digest != "" && body != nil {
		err := verifyDigest(body, digest)
		if err != nil {
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

// getPublicKey retrieves a public key from cache or fetches it.
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
		return nil, errors.New("actor has no public key")
	}

	// H2: Verify public key ID matches the keyId from the signature
	if actor.PublicKey.ID != "" && actor.PublicKey.ID != keyID {
		return nil, fmt.Errorf("public key ID %q does not match signature keyId %q", actor.PublicKey.ID, keyID)
	}

	// H2: Verify public key owner is the actor itself
	if actor.PublicKey.Owner != "" && actor.PublicKey.Owner != actorURI {
		return nil, fmt.Errorf("public key owner %q does not match actor URI %q", actor.PublicKey.Owner, actorURI)
	}

	publicKey, err := ParsePublicKey(actor.PublicKey.PublicKeyPem)
	if err != nil {
		return nil, fmt.Errorf("parsing public key: %w", err)
	}

	// Cache the key
	v.cache.Add(keyID, publicKey)
	log.Info("Cached public key for %s", keyID)

	return publicKey, nil
}

// verifyDigest verifies the SHA-256 digest header.
func verifyDigest(body []byte, digestHeader string) error {
	// Parse digest header (format: SHA-256=base64hash)
	parts := strings.SplitN(digestHeader, "=", 2)
	if len(parts) != 2 {
		return errors.New("invalid digest format")
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
	if !bytes.Equal(expectedHash, actualHash[:]) {
		return errors.New("digest mismatch")
	}

	return nil
}

// headerContains checks if a header name (case-insensitive) is in the signed headers list.
func headerContains(headers []string, target string) bool {
	for _, h := range headers {
		if strings.EqualFold(h, target) {
			return true
		}
	}

	return false
}

// verifyRequestDate checks that the Date header is signed and within an acceptable time window.
func verifyRequestDate(sig *SignatureHeader, req *http.Request) error {
	// 1. Check date is in the signed headers list
	if !headerContains(sig.Headers, "date") {
		return errors.New("date header must be signed")
	}
	// 2. Must have Date header
	dateStr := req.Header.Get("Date")
	if dateStr == "" {
		return errors.New("missing date header")
	}
	// 3. Use http.ParseTime to parse (supports all HTTP date formats)
	date, err := http.ParseTime(dateStr)
	if err != nil {
		return fmt.Errorf("invalid date format: %w", err)
	}
	// 4. Check time window
	skew := 5 * time.Minute

	now := time.Now()

	if date.Before(now.Add(-skew)) || date.After(now.Add(skew)) {
		return errors.New("request date too far from current time")
	}

	return nil
}

// verifyDigestIsSigned enforces that the Digest header is signed and valid when a body is present.
func verifyDigestIsSigned(sig *SignatureHeader, req *http.Request, body []byte) error {
	if len(body) == 0 {
		return nil // No body, no digest needed
	}

	if !headerContains(sig.Headers, "digest") {
		return errors.New("digest header must be signed when body is present")
	}

	digest := req.Header.Get("Digest")
	if digest == "" {
		return errors.New("missing digest header for request with body")
	}

	digestHash := sha256.Sum256(body)

	expected := "SHA-256=" + base64.StdEncoding.EncodeToString(
		digestHash[:],
	)
	if digest != expected {
		return errors.New("digest mismatch")
	}

	return nil
}

// VerifyActivitySignature is a convenience function for verifying inbox requests.
func VerifyActivitySignature(c interface{ GetHeader(string) string }, body []byte) (string, error) {
	// Type assert to get the underlying request
	var req *http.Request

	// Gin context has a Request method
	if ginCtx, ok := c.(interface{ Request() *http.Request }); ok {
		req = ginCtx.Request()
	} else {
		return "", errors.New("unsupported context type")
	}

	sigHeader := req.Header.Get("Signature")
	if sigHeader == "" {
		return "", errors.New("no signature header")
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
