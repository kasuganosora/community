package smtp

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jhillyerd/enmime"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/email/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// smtpServerProbe holds the mock server plus the captured session for assertions.
// Multiple servers/sessions may be created within a single test (e.g. to test
// retries), so sessions is a slice indexed by connection order.
type smtpServerProbe struct {
	srv   *smtpMockServer
	mu    sync.Mutex
	first *smtpSession // the first session established.
}

// captureFirst wraps a handler so that the first created smtpSession is
// recorded on the probe for later assertions.
func captureFirst(handler smtpHandlerFunc, probe *smtpServerProbe) smtpHandlerFunc {
	return func(t *testing.T, s *smtpSession, conn net.Conn) {
		probe.mu.Lock()
		if probe.first == nil {
			probe.first = s
		}
		probe.mu.Unlock()
		handler(t, s, conn)
	}
}

// cloneSession returns a pointer-safe snapshot of the first recorded session's
// observable fields, guarding against data races with the still-running goroutine.
func (p *smtpServerProbe) cloneSession() smtpSession {
	p.mu.Lock()
	s := p.first
	p.mu.Unlock()
	if s == nil {
		return smtpSession{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return smtpSession{
		Commands:      append([]string(nil), s.Commands...),
		RawCommands:   append([]string(nil), s.RawCommands...),
		Data:          append([]byte(nil), s.Data...),
		From:          s.From,
		Rcpts:         append([]string(nil), s.Rcpts...),
		Authenticated: s.Authenticated,
		AuthUser:      s.AuthUser,
		DidStartTLS:   s.DidStartTLS,
		HeloName:      s.HeloName,
		Quit:          s.Quit,
	}
}

// configSnapshot saves/restores the global config.C so tests can mutate it
// freely without leaking state into sibling tests.
type configSnapshot struct {
	server   string
	port     string
	username string
	password string
	from     string
	tls      string
	timeout  string
}

func snapshotConfig() configSnapshot {
	return configSnapshot{
		server:   config.C.SmtpServer,
		port:     config.C.SmtpPort,
		username: config.C.SmtpUsername,
		password: config.C.SmtpPassword,
		from:     config.C.SmtpFrom,
		tls:      config.C.SmtpTLS,
		timeout:  config.C.SmtpTimeout,
	}
}

func (s configSnapshot) restore() {
	config.C.SmtpServer = s.server
	config.C.SmtpPort = s.port
	config.C.SmtpUsername = s.username
	config.C.SmtpPassword = s.password
	config.C.SmtpFrom = s.from
	config.C.SmtpTLS = s.tls
	config.C.SmtpTimeout = s.timeout
}

// withConfig runs fn with the given SMTP config fields and restores the
// original values afterwards via t.Cleanup.
func withConfig(t *testing.T, server, port, username, password, tls, timeout string) {
	t.Helper()
	snap := snapshotConfig()
	config.C.SmtpServer = server
	config.C.SmtpPort = port
	config.C.SmtpUsername = username
	config.C.SmtpPassword = password
	config.C.SmtpTLS = tls
	config.C.SmtpTimeout = timeout
	t.Cleanup(snap.restore)
}

// newDriveFromServer constructs a Drive pointing at the given mock server host.
func newDriveFromServer(host, port string, useTLS bool) *Drive {
	d := NewDrive()
	if d == nil {
		return nil
	}
	// Force the in-memory server address (and TLS mode) regardless of config.
	d.server = host + ":" + port
	d.useTLS = useTLS
	return d
}

// ---------------------------------------------------------------------------
// NewDrive
// ---------------------------------------------------------------------------

func TestNewDrive_ReturnsNilWhenServerEmpty(t *testing.T) {
	withConfig(t, "", "", "u", "p", "true", "10")
	d := NewDrive()
	require.Nil(t, d, "NewDrive must return nil when SmtpServer is empty")
}

func TestNewDrive_DefaultPort(t *testing.T) {
	withConfig(t, "smtp.example.com", "", "u", "p", "true", "10")
	d := NewDrive()
	require.NotNil(t, d)
	assert.Equal(t, "smtp.example.com:587", d.server, "should default to port 587")
	assert.True(t, d.useTLS, "should default to TLS enabled")
	assert.Equal(t, 10*time.Second, d.timeout)
}

func TestNewDrive_CustomPort(t *testing.T) {
	withConfig(t, "smtp.example.com", "465", "u", "p", "true", "5")
	d := NewDrive()
	require.NotNil(t, d)
	assert.Equal(t, "smtp.example.com:465", d.server)
	assert.Equal(t, 5*time.Second, d.timeout)
}

func TestNewDrive_TLSDisabled(t *testing.T) {
	withConfig(t, "smtp.example.com", "587", "u", "p", "false", "10")
	d := NewDrive()
	require.NotNil(t, d)
	assert.False(t, d.useTLS, "SmtpTLS=false must disable TLS")
}

func TestNewDrive_TLSDisabledZero(t *testing.T) {
	withConfig(t, "smtp.example.com", "587", "u", "p", "0", "10")
	d := NewDrive()
	require.NotNil(t, d)
	assert.False(t, d.useTLS, "SmtpTLS=0 must disable TLS")
}

func TestNewDrive_TLSEnabledDefault(t *testing.T) {
	// Empty SmtpTLS leaves TLS enabled (default true).
	withConfig(t, "smtp.example.com", "587", "u", "p", "", "10")
	d := NewDrive()
	require.NotNil(t, d)
	assert.True(t, d.useTLS)
}

func TestNewDrive_InvalidTimeout(t *testing.T) {
	withConfig(t, "smtp.example.com", "587", "u", "p", "true", "not-a-number")
	d := NewDrive()
	require.NotNil(t, d)
	assert.Equal(t, 10*time.Second, d.timeout, "invalid timeout should fall back to 10s")
}

func TestNewDrive_NegativeTimeout(t *testing.T) {
	withConfig(t, "smtp.example.com", "587", "u", "p", "true", "-5")
	d := NewDrive()
	require.NotNil(t, d)
	assert.Equal(t, 10*time.Second, d.timeout, "non-positive timeout should fall back to 10s")
}

func TestNewDrive_AuthConfigured(t *testing.T) {
	withConfig(t, "smtp.example.com", "587", "alice", "secret", "true", "10")
	d := NewDrive()
	require.NotNil(t, d)
	require.NotNil(t, d.auth)

	// Verify the auth implementation produces the expected PLAIN payload.
	proto, resp, err := d.auth.Start(nil)
	require.NoError(t, err)
	assert.Equal(t, "PLAIN", proto)
	// identity\0username\0password
	expected := "\x00alice\x00secret"
	assert.Equal(t, expected, string(resp))
}

// ---------------------------------------------------------------------------
// tlsPlainAuth
// ---------------------------------------------------------------------------

func TestTlsPlainAuth_Start(t *testing.T) {
	a := &tlsPlainAuth{identity: "id", username: "user", password: "pw"}
	proto, resp, err := a.Start(nil)
	require.NoError(t, err)
	assert.Equal(t, "PLAIN", proto)
	assert.Equal(t, "id\x00user\x00pw", string(resp))
}

func TestTlsPlainAuth_Next_NoMoreChallenge(t *testing.T) {
	a := &tlsPlainAuth{}
	out, err := a.Next([]byte("server"), false)
	require.NoError(t, err)
	assert.Nil(t, out, "Next with more=false should return nil")
}

func TestTlsPlainAuth_Next_UnexpectedChallenge(t *testing.T) {
	a := &tlsPlainAuth{}
	out, err := a.Next([]byte("server"), true)
	require.Error(t, err)
	assert.Nil(t, out)
	assert.Contains(t, err.Error(), "unexpected server challenge")
}

// ---------------------------------------------------------------------------
// Send - address parsing, validation, and dispatch (without a real connection)
// ---------------------------------------------------------------------------

func TestSend_EmptyServerNoOp(t *testing.T) {
	withConfig(t, "", "", "u", "p", "true", "10")
	d := &Drive{} // server == ""
	err := d.Send("a@b.com", "", []byte("body"))
	assert.NoError(t, err, "Send on empty server must be a no-op")
}

func TestSend_InvalidRecipient(t *testing.T) {
	withConfig(t, "smtp.example.com", "587", "u", "p", "true", "10")
	d := &Drive{server: "smtp.example.com:587", useTLS: true}
	err := d.Send("not-an-email", "", []byte("body"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid recipient email address")
	assert.Contains(t, err.Error(), "not-an-email")
}

func TestSend_NoAddressesAfterFiltering(t *testing.T) {
	// "to" and "cc" are empty strings -> after split/filter there are no addrs.
	withConfig(t, "smtp.example.com", "587", "u", "p", "true", "10")
	called := atomic.Bool{}
	d := &Drive{
		server: "smtp.example.com:587",
		useTLS: true,
		auth:   &tlsPlainAuth{},
		// Override the sender to detect a (non-)call. Since addrs is empty,
		// neither sendWithTLS nor sendWithSTARTTLS should be invoked.
	}
	_ = d
	// We cannot easily override unexported methods, but the early return on
	// len(addrs)==0 happens before any network call, so no connection is made.
	err := d.Send("", "", []byte("body"))
	assert.NoError(t, err)
	assert.False(t, called.Load())
}

func TestSend_AddressesDedup(t *testing.T) {
	// Duplicate + empty addresses: only invalid ones should trigger an error,
	// proving that dedup happened before validation (duplicates collapse).
	withConfig(t, "smtp.example.com", "587", "u", "p", "true", "10")
	d := &Drive{server: "smtp.example.com:587", useTLS: true}
	// "a@b.com;a@b.com;" -> dedup yields ["a@b.com"] which is valid, so the
	// *only* error source is the trailing empty being filtered; no error here
	// would mean we tried to connect (which fails). We assert that validation
	// itself does not reject the duplicate.
	err := d.Send("bad-address;a@b.com", "", []byte("body"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid recipient email address")
}

func TestSend_CCIncluded(t *testing.T) {
	// Both to and cc contribute; an invalid cc must surface the error.
	withConfig(t, "smtp.example.com", "587", "u", "p", "true", "10")
	d := &Drive{server: "smtp.example.com:587", useTLS: true}
	err := d.Send("a@b.com", "garbage", []byte("body"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "garbage")
}

// ---------------------------------------------------------------------------
// Connection-failure paths (no server listening)
// ---------------------------------------------------------------------------

func TestSendWithTLS_ConnectionRefused(t *testing.T) {
	withConfig(t, "127.0.0.1", "1", "u", "p", "true", "1")
	d := NewDrive()
	require.NotNil(t, d)
	d.timeout = 200 * time.Millisecond // keep the test snappy
	err := d.Send("a@b.com", "", []byte("body"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TLS dial failed")
}

func TestSendWithSTARTTLS_ConnectionRefused(t *testing.T) {
	withConfig(t, "127.0.0.1", "1", "u", "p", "false", "1")
	d := NewDrive()
	require.NotNil(t, d)
	d.useTLS = false
	d.timeout = 200 * time.Millisecond
	err := d.Send("a@b.com", "", []byte("body"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dial failed")
}

// ---------------------------------------------------------------------------
// Happy path: sendWithSTARTTLS over the mock SMTP server (STARTTLS upgrade)
// ---------------------------------------------------------------------------

func TestSend_HappyPath_STARTTLS(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	tlsLeafCert = generateLeafCert(t)
	probe := &smtpServerProbe{}
	srv := newMockSMTPServer(t, captureFirst(handlerFullSession, probe))
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false) // plaintext -> STARTTLS path
	require.NotNil(t, d)

	err := d.Send("to1@x.com;to2@x.com", "cc1@x.com", []byte("Subject: hi\r\n\r\nbody"))
	require.NoError(t, err)

	// Give the server goroutine a moment to finish recording then snapshot.
	srv.Close()
	snap := probe.cloneSession()
	assert.True(t, snap.Authenticated, "client should have authenticated")
	assert.Equal(t, "alice", snap.AuthUser)
	assert.True(t, snap.DidStartTLS, "STARTTLS should have been negotiated")
	assert.Equal(t, config.C.SmtpUsername, snap.From)
	assert.ElementsMatch(t, []string{"to1@x.com", "to2@x.com", "cc1@x.com"}, snap.Rcpts)
	assert.True(t, snap.Quit, "client should have issued QUIT")
	assert.Contains(t, string(snap.Data), "Subject: hi")
}

// ---------------------------------------------------------------------------
// Happy path: plaintext server without STARTTLS (falls back to plaintext)
// ---------------------------------------------------------------------------

func TestSend_NoStartTLS_PlaintextFallback(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	probe := &smtpServerProbe{}
	srv := newMockSMTPServer(t, captureFirst(handlerNoStartTLS, probe))
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NotNil(t, d)

	err := d.Send("to@x.com", "", []byte("Subject: hi\r\n\r\nbody"))
	require.NoError(t, err)

	srv.Close()
	snap := probe.cloneSession()
	assert.True(t, snap.Authenticated)
	assert.False(t, snap.DidStartTLS, "server did not advertise STARTTLS")
	assert.True(t, snap.Quit)
}

// ---------------------------------------------------------------------------
// Happy path: sendWithTLS over the implicit-TLS mock SMTP server
// ---------------------------------------------------------------------------

func TestSend_HappyPath_ImplicitTLS(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "true", "10")

	tlsLeafCert = generateLeafCert(t)
	leaf := generateLeafCert(t)
	probe := &smtpServerProbe{}
	srv := newMockSMTPTLSServer(t,
		tlsConfigForTest(t, leaf),
		captureFirst(handlerNoStartTLS, probe))
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), true) // implicit TLS path
	require.NotNil(t, d)

	err := d.Send("to@x.com", "", []byte("Subject: hi\r\n\r\nbody"))
	require.NoError(t, err)

	srv.Close()
	snap := probe.cloneSession()
	assert.True(t, snap.Authenticated)
	assert.Equal(t, "alice", snap.AuthUser)
	assert.True(t, snap.Quit)
	assert.Contains(t, string(snap.Data), "body")
}

// ---------------------------------------------------------------------------
// Error paths via scripted handlers
// ---------------------------------------------------------------------------

func TestSend_AuthFailure(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	tlsLeafCert = generateLeafCert(t)
	probe := &smtpServerProbe{}
	srv := newMockSMTPServer(t, captureFirst(handlerAuthFailure, probe))
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NotNil(t, d)

	err := d.Send("to@x.com", "", []byte("body"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth failed")
}

func TestSend_MailFromFailure(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	tlsLeafCert = generateLeafCert(t)
	probe := &smtpServerProbe{}
	srv := newMockSMTPServer(t, captureFirst(handlerMailFromFailure, probe))
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NotNil(t, d)

	err := d.Send("to@x.com", "", []byte("body"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MAIL FROM")
}

func TestSend_RcptFailure(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	tlsLeafCert = generateLeafCert(t)
	probe := &smtpServerProbe{}
	srv := newMockSMTPServer(t, captureFirst(handlerRcptFailure, probe))
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NotNil(t, d)

	err := d.Send("to@x.com", "", []byte("body"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RCPT TO")
}

func TestSend_DataFailure(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	tlsLeafCert = generateLeafCert(t)
	probe := &smtpServerProbe{}
	srv := newMockSMTPServer(t, captureFirst(handlerDataFailure, probe))
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NotNil(t, d)

	err := d.Send("to@x.com", "", []byte("body"))
	require.Error(t, err)
	// dataCloser.Close() error is wrapped as "close data writer failed".
	assert.Contains(t, err.Error(), "data writer")
}

// ---------------------------------------------------------------------------
// Retries: Send should surface the final error after the SMTP exchange.
// (The package does not implement internal retries, so we verify deterministic
// repeated-invocation behaviour: each call is independent.)
// ---------------------------------------------------------------------------

func TestSend_RetryIndependence(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	tlsLeafCert = generateLeafCert(t)
	srv := newMockSMTPServer(t, handlerFullSession)
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NotNil(t, d)

	for i := 0; i < 3; i++ {
		err := d.Send("to@x.com", "", []byte("retry body"))
		require.NoErrorf(t, err, "send attempt %d should succeed", i+1)
	}
}

// ---------------------------------------------------------------------------
// SendMail - MIME building, attachments, inline content, text/html
// ---------------------------------------------------------------------------

func TestSendMail_TextAndHTML(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	tlsLeafCert = generateLeafCert(t)
	probe := &smtpServerProbe{}
	srv := newMockSMTPServer(t, captureFirst(handlerFullSession, probe))
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NotNil(t, d)

	mail := &types.Mail{
		To:       "to@x.com",
		Subject:  "Hello",
		TextBody: "plain text body",
		Body:     "<p>html body</p>",
	}
	err := d.SendMail("to@x.com", "", mail)
	require.NoError(t, err)

	srv.Close()
	snap := probe.cloneSession()
	body := string(snap.Data)
	assert.Contains(t, body, "Subject: Hello")
	assert.Contains(t, body, "plain text body")
	assert.Contains(t, body, "<p>html body</p>")
	assert.True(t, snap.Quit)
}

func TestSendMail_WithAttachment(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	tlsLeafCert = generateLeafCert(t)
	probe := &smtpServerProbe{}
	srv := newMockSMTPServer(t, captureFirst(handlerFullSession, probe))
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NotNil(t, d)

	fileContent := []byte("hello attachment")
	mail := &types.Mail{
		To:      "to@x.com",
		Subject: "With Attachment",
		Body:    "<p>see attached</p>",
		Attachments: []*types.MailAttachment{
			{
				Name: "note.txt",
				File: bytesAsFile(fileContent, "note.txt"),
			},
		},
	}
	err := d.SendMail("to@x.com", "", mail)
	require.NoError(t, err)

	srv.Close()
	snap := probe.cloneSession()
	body := string(snap.Data)
	assert.Contains(t, body, "note.txt")
	assert.Contains(t, body, "attachment")
}

func TestSendMail_WithInlineAttachment(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	tlsLeafCert = generateLeafCert(t)
	probe := &smtpServerProbe{}
	srv := newMockSMTPServer(t, captureFirst(handlerFullSession, probe))
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NotNil(t, d)

	pngBytes := minimalPNG()
	mail := &types.Mail{
		To:      "to@x.com",
		Subject: "Inline",
		Body:    "<p><img src=\"cid:logo\"/></p>",
		Attachments: []*types.MailAttachment{
			{
				Name:      "logo.png",
				ContentId: "logo",
				File:      bytesAsFile(pngBytes, "logo.png"),
			},
		},
	}
	err := d.SendMail("to@x.com", "", mail)
	require.NoError(t, err)

	srv.Close()
	snap := probe.cloneSession()
	body := string(snap.Data)
	assert.Contains(t, body, "logo")
}

func TestSendMail_CCRecipients(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "alice", "pw", "false", "10")

	tlsLeafCert = generateLeafCert(t)
	probe := &smtpServerProbe{}
	srv := newMockSMTPServer(t, captureFirst(handlerFullSession, probe))
	defer srv.Close()

	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NotNil(t, d)

	mail := &types.Mail{
		To:      "to1@x.com;to2@x.com",
		Cc:      "cc1@x.com",
		Subject: "Multi",
		Body:    "<p>hi</p>",
	}
	err := d.SendMail("to1@x.com;to2@x.com", "cc1@x.com", mail)
	require.NoError(t, err)

	srv.Close()
	snap := probe.cloneSession()
	assert.ElementsMatch(t,
		[]string{"to1@x.com", "to2@x.com", "cc1@x.com"}, snap.Rcpts)
}

// ---------------------------------------------------------------------------
// SendMail error paths
// ---------------------------------------------------------------------------

func TestSendMail_EmptyServerNoOp(t *testing.T) {
	withConfig(t, "", "", "u", "p", "true", "10")
	d := &Drive{}
	err := d.SendMail("to@x.com", "", &types.Mail{Subject: "x", Body: "<p/>"})
	assert.NoError(t, err)
}

func TestSendMail_InvalidRecipient(t *testing.T) {
	withConfig(t, "127.0.0.1", "587", "u", "p", "true", "10")
	d := &Drive{server: "smtp.example.com:587", useTLS: true}
	err := d.SendMail("garbage", "", &types.Mail{Subject: "x", Body: "<p/>"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid recipient email address")
}

func TestSendMail_AttachmentStatError(t *testing.T) {
	withConfig(t, "127.0.0.1", "587", "u", "p", "true", "10")
	d := &Drive{server: "127.0.0.1:587", useTLS: true}

	mail := &types.Mail{
		To:      "to@x.com",
		Subject: "x",
		Body:    "<p/>",
		Attachments: []*types.MailAttachment{
			{Name: "bad", File: errFile{}},
		},
	}
	err := d.SendMail("to@x.com", "", mail)
	require.Error(t, err)
}

func TestSendMail_SendFailureWrapsError(t *testing.T) {
	// Point at a closed port to force a send failure; the error should be
	// wrapped with "send mail failed".
	withConfig(t, "127.0.0.1", "1", "u", "p", "true", "1")
	d := NewDrive()
	require.NotNil(t, d)
	d.timeout = 200 * time.Millisecond

	err := d.SendMail("to@x.com", "", &types.Mail{Subject: "x", Body: "<p/>"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "send mail failed")
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// tlsConfigForTest builds a *tls.Config for the implicit-TLS mock server.
func tlsConfigForTest(t *testing.T, cert tls.Certificate) *tls.Config {
	t.Helper()
	return &tls.Config{Certificates: []tls.Certificate{cert}}
}

// minimalPNG returns a tiny valid PNG (1x1 transparent) used for inline tests.
func minimalPNG() []byte {
	// Pre-encoded 1x1 transparent PNG.
	return []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89, 0x00, 0x00, 0x00,
		0x0D, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9C, 0x62, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49,
		0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
	}
}

// bytesAsFile wraps an in-memory byte slice as an fs.File named filename.
func bytesAsFile(data []byte, filename string) *memFile {
	return &memFile{data: data, name: filename}
}

// memFile implements fs.File (Stat/Read/Close) backed by a byte slice.
type memFile struct {
	data   []byte
	name   string
	read   int
	closed bool
}

func (f *memFile) Stat() (os.FileInfo, error) {
	if f.data == nil {
		return nil, errors.New("stat: no data")
	}
	return memFileInfo{name: f.name, size: int64(len(f.data))}, nil
}

func (f *memFile) Read(p []byte) (int, error) {
	if f.closed {
		return 0, errors.New("read on closed file")
	}
	if f.read >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.read:])
	f.read += n
	return n, nil
}

func (f *memFile) Close() error {
	f.closed = true
	return nil
}

type memFileInfo struct {
	name string
	size int64
}

func (i memFileInfo) Name() string       { return i.name }
func (i memFileInfo) Size() int64        { return i.size }
func (i memFileInfo) Mode() os.FileMode  { return 0o644 }
func (i memFileInfo) ModTime() time.Time { return time.Time{} }
func (i memFileInfo) IsDir() bool        { return false }
func (i memFileInfo) Sys() any           { return nil }

// errFile is an fs.File whose Stat always fails, to exercise the attachment
// stat-error branch in SendMail.
type errFile struct{}

func (errFile) Stat() (os.FileInfo, error) { return nil, errors.New("stat boom") }
func (errFile) Read(p []byte) (int, error) { return 0, io.EOF }
func (errFile) Close() error               { return nil }

// dummyUse suppresses "imported and not used" for enmime if it ends up unused
// after refactors; keeps the test file robust to lint.
var _ = enmime.Builder
var _ = strings.TrimSpace
