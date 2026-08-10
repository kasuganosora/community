package smtp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Package-level test CA. It is generated exactly once in TestMain and written
// to a temp file referenced by SSL_CERT_FILE.
//
// Why a single shared CA? crypto/x509 builds the system root pool lazily and
// caches it for the whole process lifetime (sync.Once in root.go). The pool
// is seeded from SSL_CERT_FILE only on that first build, so the environment
// variable is read at most once. Therefore every test in this package must
// share the same CA, and each test signs its own per-session leaf certificate
// with that CA.
var (
	testCACert  *x509.Certificate
	testCAKey   *ecdsa.PrivateKey
	testCACAPEM []byte
	caCertFile  string
)

// TestMain generates the shared test CA, points SSL_CERT_FILE at it (so the
// implicit-TLS code path — which uses the system root pool — trusts our
// self-signed server), then runs the suite.
func TestMain(m *testing.M) {
	setupTestCA()
	// IMPORTANT: SSL_CERT_FILE must be set before any TLS handshake triggers
	// the one-time system root pool load. Setting it here (before m.Run) is
	// the only safe place.
	_ = os.Setenv("SSL_CERT_FILE", caCertFile)

	code := m.Run()
	_ = os.Unsetenv("SSL_CERT_FILE")
	os.Exit(code)
}

func setupTestCA() {
	dir, err := os.MkdirTemp("", "smtp-test-ca-*")
	if err != nil {
		panic("create temp dir for test CA: " + err.Error())
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic("generate CA key: " + err.Error())
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "smtp-test-ca", Organization: []string{"community-tests"}},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic("create CA cert: " + err.Error())
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic("parse CA cert: " + err.Error())
	}

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		panic("write CA file: " + err.Error())
	}

	testCACert = cert
	testCAKey = key
	testCACAPEM = caPEM
	caCertFile = caPath
}

// generateLeafCert creates a leaf certificate valid for 127.0.0.1 / localhost,
// signed by the shared test CA. Returns a tls.Certificate ready for use as a
// server (or client) certificate.
func generateLeafCert(t *testing.T) tls.Certificate {
	t.Helper()

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "127.0.0.1", Organization: []string{"community-tests"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:     []string{"localhost", "127.0.0.1"},
	}
	der, err := x509.CreateCertificate(rand.Reader, leafTmpl, testCACert, &leafKey.PublicKey, testCAKey)
	require.NoError(t, err)

	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	require.NoError(t, err)

	cert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	)
	require.NoError(t, err)
	return cert
}
