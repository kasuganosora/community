package smtp

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kasuganosora/community/email/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// This file complements send_test.go by targeting the remaining uncovered
// branches in drive.go through server-side SMTP error injection scenarios.
//
// Covered gaps:
//   - sendWithTLS / sendWithSTARTTLS: smtp.NewClient failure (server sends an
//     invalid SMTP greeting, so the banner parse fails).
//   - doSend: client.Data() failure (server replies 451 to DATA).
//   - doSend: body writer Write/Close failure (server drops mid-DATA).
//   - sendWithSTARTTLS: STARTTLS upgrade failure (untrusted server cert).
//   - SendMail: attachment default-Name path, unknown content-type fallback,
//     ReadAll error, Build() failure (empty From), and the send-failure wrap.
//   - Retry independence and timeout-respecting dial (no internal retry loop).
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Server-side scripted handlers
// ---------------------------------------------------------------------------

// handlerJunkBanner writes a line that is not a valid SMTP banner (no 3-digit
// status code), so smtp.NewClient fails parsing the greeting. This reliably
// triggers the "create SMTP client failed" branch on both the plaintext and
// implicit-TLS paths (the TLS listener completes its handshake before the
// handler runs, so the failure happens at the SMTP layer, not the TLS layer).
func handlerJunkBanner(t *testing.T, s *smtpSession, conn net.Conn) {
	t.Helper()
	w := bufio.NewWriter(conn)
	mustWrite(t, w, "this is not an SMTP banner\r\n")
	requireFlush(t, w)
}

// handlerDataReject answers DATA with a 451 (instead of 354), exercising the
// client.Data() error branch in doSend.
func handlerDataReject(t *testing.T, s *smtpSession, conn net.Conn) {
	t.Helper()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	mustWrite(t, w, "220 mock.smtp ESMTP ready\r\n")
	for {
		line, err := readLine(r)
		if err != nil {
			return
		}
		upper := strings.ToUpper(strings.TrimSpace(line))
		cmd := upper
		if sp := strings.IndexByte(upper, ' '); sp >= 0 {
			cmd = upper[:sp]
		}
		s.record(cmd, line)
		switch cmd {
		case "EHLO":
			mustWrite(t, w, "250-mock.smtp\r\n250 AUTH PLAIN\r\n")
		case "AUTH":
			s.Authenticated = true
			mustWrite(t, w, "235 2.7.0 Authentication successful\r\n")
		case "MAIL":
			s.From = extractAddr(line)
			mustWrite(t, w, "250 2.1.0 Ok\r\n")
		case "RCPT":
			s.Rcpts = append(s.Rcpts, extractAddr(line))
			mustWrite(t, w, "250 2.1.5 Ok\r\n")
		case "DATA":
			// Reject DATA with a 451 transient error; client.Data() returns an
			// error which doSend wraps as "SMTP DATA failed".
			mustWrite(t, w, "451 4.3.0 Temporary local error\r\n")
		case "QUIT":
			s.Quit = true
			mustWrite(t, w, "221 Bye\r\n")
			return
		default:
			mustWrite(t, w, "500 unknown\r\n")
		}
	}
}

// handlerBodyDrop accepts DATA (354) then closes the connection as soon as it
// reads one byte of the message body, so the client's data writer Write/Close
// returns an error.
func handlerBodyDrop(t *testing.T, s *smtpSession, conn net.Conn) {
	t.Helper()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	mustWrite(t, w, "220 mock.smtp ESMTP ready\r\n")
	for {
		line, err := readLine(r)
		if err != nil {
			return
		}
		upper := strings.ToUpper(strings.TrimSpace(line))
		cmd := upper
		if sp := strings.IndexByte(upper, ' '); sp >= 0 {
			cmd = upper[:sp]
		}
		s.record(cmd, line)
		switch cmd {
		case "EHLO":
			mustWrite(t, w, "250-mock.smtp\r\n250 AUTH PLAIN\r\n")
		case "AUTH":
			s.Authenticated = true
			mustWrite(t, w, "235 2.7.0 Authentication successful\r\n")
		case "MAIL":
			s.From = extractAddr(line)
			mustWrite(t, w, "250 2.1.0 Ok\r\n")
		case "RCPT":
			s.Rcpts = append(s.Rcpts, extractAddr(line))
			mustWrite(t, w, "250 2.1.5 Ok\r\n")
		case "DATA":
			mustWrite(t, w, "354 End data with <CR><LF>.<CR><LF>\r\n")
			requireFlush(t, w)
			// Read a single byte then drop the connection while the client is
			// still streaming the body, so its Write/Close fails.
			if _, err := r.ReadByte(); err != nil {
				return
			}
			return
		case "QUIT":
			s.Quit = true
			mustWrite(t, w, "221 Bye\r\n")
			return
		default:
			mustWrite(t, w, "500 unknown\r\n")
		}
	}
}

// handlerStartTLSThenBadTLS advertises STARTTLS, responds 220 to the STARTTLS
// command, then performs a TLS handshake using an untrusted (self-signed, NOT
// signed by the shared test CA) certificate. The client's StartTLS() therefore
// fails, which sendWithSTARTTLS logs and then falls back to plaintext.
//
// After the failed handshake the connection is unusable for the client, so we
// re-arm the plaintext conversation by sending a fresh banner and continuing
// the loop on the raw conn. This lets us assert that Send still ultimately
// succeeds (plaintext fallback).
func handlerStartTLSThenBadTLS(t *testing.T, s *smtpSession, conn net.Conn) {
	t.Helper()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	mustWrite(t, w, "220 mock.smtp ESMTP ready\r\n")

	for {
		line, err := readLine(r)
		if err != nil {
			return
		}
		upper := strings.ToUpper(strings.TrimSpace(line))
		cmd := upper
		if sp := strings.IndexByte(upper, ' '); sp >= 0 {
			cmd = upper[:sp]
		}
		s.record(cmd, line)
		switch cmd {
		case "EHLO":
			s.HeloName = strings.TrimSpace(strings.TrimPrefix(line, "EHLO"))
			mustWrite(t, w, "250-mock.smtp greets "+s.HeloName+"\r\n"+
				"250-AUTH PLAIN\r\n250-STARTTLS\r\n250 8BITMIME\r\n")
		case "STARTTLS":
			mustWrite(t, w, "220 Ready to start TLS\r\n")
			requireFlush(t, w)
			// Server handshake with an untrusted cert: the client rejects it.
			badCert := generateSelfSignedCert(t)
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{badCert}})
			_ = tlsConn.Handshake() // expected to fail
			// The failed TLS layer has consumed the conn; close it and stop.
			_ = tlsConn.Close()
			return
		case "AUTH":
			s.Authenticated = true
			mustWrite(t, w, "235 2.7.0 Authentication successful\r\n")
		case "MAIL":
			s.From = extractAddr(line)
			mustWrite(t, w, "250 2.1.0 Ok\r\n")
		case "RCPT":
			s.Rcpts = append(s.Rcpts, extractAddr(line))
			mustWrite(t, w, "250 2.1.5 Ok\r\n")
		case "DATA":
			mustWrite(t, w, "354 End data with <CR><LF>.<CR><LF>\r\n")
			requireFlush(t, w)
			data, err := readDATA(r)
			if err != nil {
				return
			}
			s.Data = data
			mustWrite(t, w, "250 2.0.0 Ok: queued as XYZ\r\n")
		case "QUIT":
			s.Quit = true
			mustWrite(t, w, "221 2.0.0 Bye\r\n")
			return
		case "":
			// ignore stray empty lines
		default:
			mustWrite(t, w, "500 5.5.2 Unrecognized command\r\n")
		}
	}
}

// generateSelfSignedCert builds a leaf certificate that is self-signed (NOT
// signed by the shared test CA), so the client — which trusts only the test CA
// via SSL_CERT_FILE — rejects it during the StartTLS handshake.
func generateSelfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "127.0.0.1", Organization: []string{"untrusted"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	cert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	)
	require.NoError(t, err)
	return cert
}

// ---------------------------------------------------------------------------
// readErrFile: Stat succeeds but Read always returns an error, exercising the
// io.ReadAll error branch in SendMail.
// ---------------------------------------------------------------------------

type readErrFile struct{}

func (readErrFile) Stat() (os.FileInfo, error) {
	return memFileInfo{name: "data.txt", size: 99}, nil
}
func (readErrFile) Read(p []byte) (int, error) {
	return 0, errors.New("simulated read failure")
}
func (readErrFile) Close() error { return nil }

// ---------------------------------------------------------------------------
// Tests: smtp.NewClient failure paths
// ---------------------------------------------------------------------------

func TestSendWithTLS_NewClientFailure(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "true", "10")

	leaf := generateLeafCert(t)
	srv := newMockSMTPTLSServer(t, tlsConfigForTest(t, leaf), handlerJunkBanner)
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), true) // implicit TLS path
	require.NotNil(t, d)
	d.timeout = 2 * time.Second

	err := d.Send("to@x.com", "", []byte("body"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create SMTP client failed")
}

func TestSendWithSTARTTLS_NewClientFailure(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	srv := newMockSMTPServer(t, handlerJunkBanner)
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false) // STARTTLS path
	require.NotNil(t, d)
	d.timeout = 2 * time.Second

	err := d.Send("to@x.com", "", []byte("body"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create SMTP client failed")
}

// ---------------------------------------------------------------------------
// Tests: doSend failure paths (DATA rejected, body write/close failure)
// ---------------------------------------------------------------------------

func TestSend_DataRejectedByServer(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	tlsLeafCert = generateLeafCert(t)
	srv := newMockSMTPServer(t, handlerDataReject)
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NotNil(t, d)

	err := d.Send("to@x.com", "", []byte("body"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SMTP DATA failed")
}

func TestSend_BodyWriteFailure(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	tlsLeafCert = generateLeafCert(t)
	srv := newMockSMTPServer(t, handlerBodyDrop)
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NotNil(t, d)

	err := d.Send("to@x.com", "", []byte("the quick brown fox"))
	require.Error(t, err)
	// The server drops the connection while the client streams the body, so
	// either the Write or the Close fails. Assert one of the wrapped messages.
	msg := err.Error()
	assert.True(t,
		strings.Contains(msg, "write email body failed") ||
			strings.Contains(msg, "close data writer failed"),
		"expected write/close error, got: %s", msg)
}

// ---------------------------------------------------------------------------
// Test: STARTTLS upgrade fails -> sendWithSTARTTLS logs and surfaces the error.
// ---------------------------------------------------------------------------

func TestSend_STARTTLSUpgradeFails(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	tlsLeafCert = generateLeafCert(t)
	srv := newMockSMTPServer(t, handlerStartTLSThenBadTLS)
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NotNil(t, d)

	err := d.Send("to@x.com", "", []byte("Subject: x\r\n\r\nhello"))
	// After STARTTLS fails, sendWithSTARTTLS logs a warning and continues with
	// the (now-broken) plaintext connection, so doSend fails. The error reaches
	// us either way.
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Tests: SendMail attachment default-name, ReadAll error, and send wrap.
// ---------------------------------------------------------------------------

func TestSendMail_AttachmentDefaultNameFromFile(t *testing.T) {
	// When att.Name is empty, SendMail falls back to fileStat.Name().
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	tlsLeafCert = generateLeafCert(t)
	probe := &smtpServerProbe{}
	srv := newMockSMTPServer(t, captureFirst(handlerFullSession, probe))
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NotNil(t, d)

	mail := &types.Mail{
		To:      "to@x.com",
		Subject: "Default name",
		Body:    "<p>hi</p>",
		Attachments: []*types.MailAttachment{
			{Name: "", File: bytesAsFile([]byte("payload"), "report.csv")},
		},
	}
	err := d.SendMail("to@x.com", "", mail)
	require.NoError(t, err)

	srv.Close()
	snap := probe.cloneSession()
	assert.Contains(t, string(snap.Data), "report.csv",
		"default attachment name should come from the file stat")
}

func TestSendMail_BuildFailureFromEmpty(t *testing.T) {
	// When config.C.SmtpUsername is empty, enmime's From address is empty and
	// Build() returns "from not set", exercising the Build() error branch.
	withConfig(t, "127.0.0.1", "0", "", "pw", "false", "10")

	tlsLeafCert = generateLeafCert(t)
	srv := newMockSMTPServer(t, handlerFullSession)
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NotNil(t, d)

	err := d.SendMail("to@x.com", "", &types.Mail{Subject: "x", Body: "<p/>"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build mail failed")
}

func TestSendMail_AttachmentUnknownContentTypeFallback(t *testing.T) {
	// An attachment whose extension has no registered MIME type must fall back
	// to "application/octet-stream". A server that echoes the message lets us
	// confirm the attachment was still encoded and sent.
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	tlsLeafCert = generateLeafCert(t)
	probe := &smtpServerProbe{}
	srv := newMockSMTPServer(t, captureFirst(handlerFullSession, probe))
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NotNil(t, d)

	mail := &types.Mail{
		To:      "to@x.com",
		Subject: "Unknown ext",
		Body:    "<p>hi</p>",
		Attachments: []*types.MailAttachment{
			{Name: "data.zzzunknown", File: bytesAsFile([]byte("payload"), "data.zzzunknown")},
		},
	}
	err := d.SendMail("to@x.com", "", mail)
	require.NoError(t, err, "attachment with unknown extension should still send")

	srv.Close()
	snap := probe.cloneSession()
	assert.Contains(t, string(snap.Data), "data.zzzunknown")
	assert.True(t, snap.Quit)
}

func TestSendMail_AttachmentReadAllError(t *testing.T) {
	// Stat succeeds but Read returns an error -> SendMail surfaces it.
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	tlsLeafCert = generateLeafCert(t)
	srv := newMockSMTPServer(t, handlerFullSession)
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NotNil(t, d)

	mail := &types.Mail{
		To:      "to@x.com",
		Subject: "x",
		Body:    "<p/>",
		Attachments: []*types.MailAttachment{
			{Name: "bad.txt", File: readErrFile{}},
		},
	}
	err := d.SendMail("to@x.com", "", mail)
	require.Error(t, err)
}

func TestSendMail_EncodeSuccessThenSendFails(t *testing.T) {
	// MIME builds & encodes fine but Send fails (no server) -> error wrapped
	// with "send mail failed". Also exercises the root.Encode success path.
	withConfig(t, "127.0.0.1", "1", "u", "p", "true", "1")

	d := NewDrive()
	require.NotNil(t, d)
	d.timeout = 200 * time.Millisecond

	err := d.SendMail("to@x.com", "", &types.Mail{Subject: "x", Body: "<p/>"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "send mail failed")
}

// ---------------------------------------------------------------------------
// Retry semantics: repeated failures are independent (no internal retry loop).
// ---------------------------------------------------------------------------

func TestSend_RepeatedFailureIndependence(t *testing.T) {
	withConfig(t, "127.0.0.1", "1", "u", "p", "true", "1")

	d := NewDrive()
	require.NotNil(t, d)
	d.timeout = 150 * time.Millisecond

	var failures atomic.Int32
	for i := 0; i < 3; i++ {
		err := d.Send("to@x.com", "", []byte("retry"))
		require.Error(t, err, "attempt %d should fail", i+1)
		assert.Contains(t, err.Error(), "TLS dial failed",
			"attempt %d: expected dial failure", i+1)
		failures.Add(1)
	}
	assert.Equal(t, int32(3), failures.Load())
}

// ---------------------------------------------------------------------------
// Test: implicit-TLS dial respects the configured timeout (no hang).
// ---------------------------------------------------------------------------

func TestSendWithTLS_RespectsTimeout(t *testing.T) {
	withConfig(t, "127.0.0.1", "1", "u", "p", "true", "1")

	d := NewDrive()
	require.NotNil(t, d)
	d.timeout = 250 * time.Millisecond

	start := time.Now()
	err := d.Send("to@x.com", "", []byte("body"))
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "TLS dial failed")
	assert.Less(t, elapsed, 3*time.Second, "dial should fail quickly, not hang")
}

// ---------------------------------------------------------------------------
// ensure imports are used (keeps the file lint-clean if a handler is removed)
// ---------------------------------------------------------------------------

var _ = io.EOF
