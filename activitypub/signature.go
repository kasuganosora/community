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
	"strings"
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
