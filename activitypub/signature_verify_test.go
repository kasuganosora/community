package activitypub

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyActivitySignatureNoSignatureHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/inbox", nil)

	_, err := VerifyActivitySignature(c, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no signature header")
}

func TestVerifyActivitySignatureUnsupportedContext(t *testing.T) {
	// VerifyActivitySignature only accepts *gin.Context
	mockCtx := &mockHeaderProvider{headers: map[string]string{}}
	_, err := VerifyActivitySignature(mockCtx, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported context type")
}

// mockHeaderProvider implements the GetHeader method for testing unsupported context types.
type mockHeaderProvider struct {
	headers map[string]string
}

func (m *mockHeaderProvider) GetHeader(key string) string {
	return m.headers[key]
}

func TestVerifyRequestWithKeyIDNoSignatureHeader(t *testing.T) {
	verifier := NewSignatureVerifier()
	req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
	err := verifier.VerifyRequestWithKeyID(req, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no signature header")
}

func TestVerifyRequestWithKeyIDMalformedSignature(t *testing.T) {
	verifier := NewSignatureVerifier()
	req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
	req.Header.Set("Signature", "malformed")
	err := verifier.VerifyRequestWithKeyID(req, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing signature header")
}

func TestVerifyRequestWithKeyIDDateNotSigned(t *testing.T) {
	verifier := NewSignatureVerifier()
	now := time.Now().UTC().Format(http.TimeFormat)
	sigHeader := fmt.Sprintf(`keyId="https://example.com/users/alice#main-key",algorithm="rsa-sha256",headers="(request-target)",signature="abc"`)
	req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
	req.Header.Set("Signature", sigHeader)
	req.Header.Set("Date", now)

	err := verifier.VerifyRequestWithKeyID(req, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "date header must be signed")
}

func TestVerifyRequestWithKeyIDMissingDateHeader(t *testing.T) {
	verifier := NewSignatureVerifier()
	sigHeader := fmt.Sprintf(`keyId="https://example.com/users/alice#main-key",algorithm="rsa-sha256",headers="(request-target) date",signature="abc"`)
	req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
	req.Header.Set("Signature", sigHeader)
	// No Date header set

	err := verifier.VerifyRequestWithKeyID(req, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "date")
}

func TestVerifyRequestWithKeyIDInvalidBase64Signature(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pubKey, err := ParsePublicKey(string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: mustMarshalPublicKey(t, privateKey),
	})))
	require.NoError(t, err)

	keyID := "https://actor.example.com/users/badbase64#main-key"
	globalKeyCache.Add(keyID, pubKey)

	now := time.Now().UTC().Format(http.TimeFormat)
	// Invalid base64 in signature
	sigHeader := fmt.Sprintf(`keyId="%s",algorithm="rsa-sha256",headers="(request-target) date",signature="!!!not-base64!!!"`,
		keyID)
	req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
	req.Header.Set("Signature", sigHeader)
	req.Header.Set("Date", now)

	verifier := NewSignatureVerifier()
	err = verifier.VerifyRequestWithKeyID(req, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding signature")
}

func TestVerifyRequestWithKeyIDSignatureMismatch(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pubKey, err := ParsePublicKey(string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: mustMarshalPublicKey(t, privateKey),
	})))
	require.NoError(t, err)

	keyID := "https://actor.example.com/users/mismatch#main-key"
	globalKeyCache.Add(keyID, pubKey)

	now := time.Now().UTC().Format(http.TimeFormat)
	// Sign with a different signing string than what will be verified
	wrongSig, err := SignWithRSA(privateKey, []byte("wrong signing string"))
	require.NoError(t, err)

	sigHeader := fmt.Sprintf(`keyId="%s",algorithm="rsa-sha256",headers="(request-target) date",signature="%s"`,
		keyID, base64.StdEncoding.EncodeToString(wrongSig))
	req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
	req.Header.Set("Signature", sigHeader)
	req.Header.Set("Date", now)

	verifier := NewSignatureVerifier()
	err = verifier.VerifyRequestWithKeyID(req, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature verification failed")
}

func TestVerifyRequestWithKeyIDInvalidKeyIDURI(t *testing.T) {
	verifier := NewSignatureVerifier()
	now := time.Now().UTC().Format(http.TimeFormat)
	// Use a non-HTTP keyId URI to trigger validateFederationURI error
	// We need to get past algorithm/date checks first
	sigHeader := fmt.Sprintf(`keyId="ftp://bad.example.com/key#main-key",algorithm="rsa-sha256",headers="(request-target) date",signature="abc"`)
	req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
	req.Header.Set("Signature", sigHeader)
	req.Header.Set("Date", now)

	err := verifier.VerifyRequestWithKeyID(req, nil)
	require.Error(t, err)
	// Should fail when trying to get public key (invalid URI)
	assert.Error(t, err)
}

func TestVerifyRequestNoSignatureHeader(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
	// No Signature header
	err = VerifyRequest(req, &privateKey.PublicKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no signature header")
}

func TestVerifyRequestInvalidSignatureBase64(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	sigHeader := `keyId="https://example.com/users/test#main-key",algorithm="rsa-sha256",headers="(request-target) host",signature="!!!invalid!!!"`
	req := httptest.NewRequest(http.MethodPost, "/inbox", nil)
	req.Header.Set("Signature", sigHeader)

	err = VerifyRequest(req, &privateKey.PublicKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding signature")
}

func TestVerifyDigestVariousFormats(t *testing.T) {
	body := []byte("hello world")

	t.Run("valid SHA-256 digest", func(t *testing.T) {
		hash := sha256.Sum256(body)
		digest := "SHA-256=" + base64.StdEncoding.EncodeToString(hash[:])
		assert.NoError(t, verifyDigest(body, digest))
	})

	t.Run("missing equals sign", func(t *testing.T) {
		err := verifyDigest(body, "SHA-256")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid digest format")
	})

	t.Run("unsupported algorithm", func(t *testing.T) {
		err := verifyDigest(body, "MD5=abc123")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported digest algorithm")
	})
}

func TestHeaderContains(t *testing.T) {
	headers := []string{"(request-target)", "Host", "Date", "Digest"}

	assert.True(t, headerContains(headers, "host"))
	assert.True(t, headerContains(headers, "(request-target)"))
	assert.True(t, headerContains(headers, "DATE"))
	assert.False(t, headerContains(headers, "authorization"))
	assert.False(t, headerContains(headers, ""))
}

func TestVerifyAlgorithmEdgeCases(t *testing.T) {
	assert.NoError(t, verifyAlgorithm("rsa-sha256"))
	assert.NoError(t, verifyAlgorithm("hs2019"))
	assert.Error(t, verifyAlgorithm(""))
	assert.Error(t, verifyAlgorithm("md5"))

	err := verifyAlgorithm("rsa-sha512")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported signature algorithm")
}

func TestVerifyRequestWithKeyIDFullSuccess(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pubKey, err := ParsePublicKey(string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: mustMarshalPublicKey(t, privateKey),
	})))
	require.NoError(t, err)

	keyID := "https://actor.example.com/users/fullsuccess#main-key"
	globalKeyCache.Add(keyID, pubKey)

	host := "inbox.example.com"
	body := []byte(`{"type":"Follow"}`)
	hash := sha256.Sum256(body)
	digest := "SHA-256=" + base64.StdEncoding.EncodeToString(hash[:])
	date := time.Now().UTC().Format(http.TimeFormat)
	inboxPath := "/users/alice/inbox"

	// Build signing string with all required headers
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
}

func TestVerifyActivitySignatureSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pubKey, err := ParsePublicKey(string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: mustMarshalPublicKey(t, privateKey),
	})))
	require.NoError(t, err)

	keyID := "https://actor.example.com/users/ginverify#rsa-2d1bfa24a05cd8d9"
	globalKeyCache.Add(keyID, pubKey)

	host := "inbox.example.com"
	body := []byte(`{"type":"Like"}`)
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

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, inboxPath, nil)
	c.Request.Host = host
	c.Request.Header.Set("Signature", sigHeader)
	c.Request.Header.Set("Date", date)
	c.Request.Header.Set("Digest", digest)

	actorURI, err := VerifyActivitySignature(c, body)
	require.NoError(t, err)
	assert.Equal(t, "https://actor.example.com/users/ginverify", actorURI)
}

func TestVerifyActivitySignatureBadHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/inbox", nil)
	c.Request.Header.Set("Signature", "invalid header no keyid")

	_, err := VerifyActivitySignature(c, nil)
	require.Error(t, err)
}

// Test that errors.Is works with ActivityPubError
func TestActivityPubErrorIs(t *testing.T) {
	err := ErrInvalidAcctFormat
	assert.True(t, errors.Is(err, ErrInvalidAcctFormat))
	assert.False(t, errors.Is(err, ErrWebFingerFailed))
}
