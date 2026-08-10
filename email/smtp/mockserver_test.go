package smtp

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// smtpSession records everything the client sent during a conversation.
type smtpSession struct {
	mu sync.Mutex

	Commands      []string // Command verbs received from the client (uppercased).
	RawCommands   []string // Raw command lines as received.
	Data          []byte   // The raw DATA payload (message body) submitted by the client.
	From          string   // The "from" address seen in MAIL FROM (after "<...>" trimming).
	Rcpts         []string // The list of recipient addresses seen in RCPT TO.
	Authenticated bool     // Whether the client authenticated (AUTH succeeded).
	AuthUser      string   // The username decoded from the PLAIN-auth payload.
	DidStartTLS   bool     // Whether STARTTLS was negotiated.
	HeloName      string   // The HELO/EHLO client name.
	Quit          bool     // Whether the client issued QUIT.
}

func (s *smtpSession) record(cmd, raw string) {
	s.mu.Lock()
	s.Commands = append(s.Commands, cmd)
	s.RawCommands = append(s.RawCommands, raw)
	s.mu.Unlock()
}

// smtpHandlerFunc handles a single client connection. It receives the shared
// session (to record what the client did) and the raw net.Conn. The handler
// is responsible for wrapping the conn in a bufio.Reader/Writer; this allows a
// handler that performs STARTTLS to swap the conn for a *tls.Conn mid-stream.
type smtpHandlerFunc func(t *testing.T, s *smtpSession, conn net.Conn)

// smtpMockServer is a minimal RFC 5321 SMTP server backed by a real TCP
// listener. It lets us exercise the Drive code through genuine net.Dial /
// tls.Dial connections, including the STARTTLS upgrade path.
type smtpMockServer struct {
	t         *testing.T
	listener  net.Listener
	wg        sync.WaitGroup
	handler   smtpHandlerFunc
	closeOnce sync.Once
}

// newMockSMTPServer starts a plaintext (STARTTLS-capable) mock SMTP server.
func newMockSMTPServer(t *testing.T, handler smtpHandlerFunc) *smtpMockServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	return startMock(t, ln, handler)
}

// newMockSMTPTLSServer starts an implicit-TLS mock SMTP server (the listener
// itself performs the TLS handshake), matching the sendWithTLS code path.
func newMockSMTPTLSServer(t *testing.T, tlsCfg *tls.Config, handler smtpHandlerFunc) *smtpMockServer {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	require.NoError(t, err)
	return startMock(t, ln, handler)
}

func startMock(t *testing.T, ln net.Listener, handler smtpHandlerFunc) *smtpMockServer {
	srv := &smtpMockServer{t: t, listener: ln, handler: handler}
	srv.wg.Add(1)
	go srv.acceptLoop()
	return srv
}

func (s *smtpMockServer) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return // listener closed
		}
		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			defer c.Close()
			sess := &smtpSession{}
			s.handler(s.t, sess, c)
		}(conn)
	}
}

// Addr returns the host:port of the mock server.
func (s *smtpMockServer) Addr() string { return s.listener.Addr().String() }

// Host returns just the host (always 127.0.0.1).
func (s *smtpMockServer) Host() string { return "127.0.0.1" }

// Port returns just the port.
func (s *smtpMockServer) Port() string {
	_, port, _ := net.SplitHostPort(s.Addr())
	return port
}

// Close stops the server and waits for in-flight connections to finish.
func (s *smtpMockServer) Close() {
	s.closeOnce.Do(func() { _ = s.listener.Close() })
	s.wg.Wait()
}

// --- low-level line/data helpers used by scripted handlers ---

// readLine reads a single CR/LF-terminated line, trimming the trailing CRLF.
func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// readDATA reads the message body following a DATA command until the lone "."
// terminator, exactly as the SMTP protocol specifies. It undoes dot-stuffing.
func readDATA(r *bufio.Reader) ([]byte, error) {
	var sb strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF && line == "" {
				break
			}
			return nil, err
		}
		clean := strings.TrimRight(line, "\r\n")
		if clean == "." {
			break
		}
		// Undo dot-stuffing: a line beginning with ".." is really ".".
		if strings.HasPrefix(clean, "..") {
			clean = clean[1:]
		}
		sb.WriteString(clean)
		sb.WriteString("\r\n")
	}
	return []byte(sb.String()), nil
}

// mustWrite writes s to w and flushes if the writer is buffered.
func mustWrite(t *testing.T, w io.Writer, s string) {
	t.Helper()
	_, err := io.WriteString(w, s)
	require.NoErrorf(t, err, "writing %q", truncateForLog(s))
	if bf, ok := w.(*bufio.Writer); ok {
		require.NoErrorf(t, bf.Flush(), "flushing")
	}
}

func truncateForLog(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

// extractAddr pulls the email address out of a "MAIL FROM:<a@b>" /
// "RCPT TO:<a@b>" line.
func extractAddr(line string) string {
	lower := strings.ToLower(line)
	for _, prefix := range []string{"mail from:", "rcpt to:"} {
		if idx := strings.Index(lower, prefix); idx >= 0 {
			rest := line[idx+len(prefix):]
			rest = strings.TrimSpace(rest)
			if strings.HasPrefix(rest, "<") {
				if end := strings.IndexByte(rest, '>'); end > 0 {
					return rest[1:end]
				}
			}
			return rest
		}
	}
	return ""
}
