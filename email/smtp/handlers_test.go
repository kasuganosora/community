package smtp

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"strings"
	"testing"
)

// Package-level leaf certificate used by STARTTLS handlers. Generated once in
// TestMain (after the CA) so all STARTTLS upgrades share the same cert.
var tlsLeafCert tls.Certificate

// sessionLoop is a reusable command loop implementing the happy-path SMTP
// conversation (220 banner, EHLO, AUTH PLAIN, MAIL/RCPT/DATA, QUIT). It also
// supports an optional STARTTLS upgrade: when advertiseSTARTTLS is true and
// the client sends STARTTLS, the underlying conn is upgraded to TLS and the
// loop continues on the encrypted connection.
//
// The "rw" pair lets the loop read commands and write responses. On STARTTLS
// we replace rw with a new pair backed by the *tls.Conn.
func sessionLoop(t *testing.T, s *smtpSession, conn net.Conn, advertiseSTARTTLS bool) {
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
			s.HeloName = strings.TrimSpace(line[len("EHLO"):])
			resp := "250-mock.smtp greets " + s.HeloName + "\r\n" +
				"250-AUTH PLAIN\r\n"
			if advertiseSTARTTLS {
				resp += "250-STARTTLS\r\n"
			}
			resp += "250 8BITMIME\r\n"
			mustWrite(t, w, resp)

		case "STARTTLS":
			if !advertiseSTARTTLS {
				mustWrite(t, w, "502 5.5.1 STARTTLS not supported\r\n")
				continue
			}
			mustWrite(t, w, "220 Ready to start TLS\r\n")
			// Flush the plaintext 220 response before the handshake.
			requireNoErr := func(err error) {
				if err != nil {
					t.Fatalf("flush before STARTTLS: %v", err)
				}
			}
			requireNoErr(w.Flush())
			// Upgrade the connection. Any bytes already buffered in r would be
			// stale plaintext; but the client only sends its ClientHello after
			// receiving our 220, so r's buffer is empty here.
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{tlsLeafCert}})
			if err := tlsConn.Handshake(); err != nil {
				_ = tlsConn.Close()
				return
			}
			s.DidStartTLS = true
			// Continue the session on the encrypted stream.
			r = bufio.NewReader(tlsConn)
			w = bufio.NewWriter(tlsConn)

		case "AUTH":
			handleAuth(t, s, r, w, line)

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
			mustWrite(t, w, "250 2.0.0 Ok: queued as ABCDEF\r\n")

		case "RSET":
			mustWrite(t, w, "250 2.0.0 Ok\r\n")

		case "NOOP":
			mustWrite(t, w, "250 2.0.0 Ok\r\n")

		case "QUIT":
			s.Quit = true
			mustWrite(t, w, "221 2.0.0 Bye\r\n")
			return

		case "HELO":
			mustWrite(t, w, "250 mock.smtp\r\n")

		case "":
			// ignore stray empty lines

		default:
			mustWrite(t, w, "500 5.5.2 Unrecognized command\r\n")
		}
	}
}

// handleAuth processes an AUTH command. The project's tlsPlainAuth sends the
// initial response inline ("AUTH PLAIN <base64>"), so we decode it directly.
// We also tolerate the two-step challenge form for robustness.
func handleAuth(t *testing.T, s *smtpSession, r *bufio.Reader, w io.Writer, line string) {
	parts := strings.Fields(line)
	switch {
	case len(parts) >= 3 && strings.EqualFold(parts[1], "PLAIN"):
		raw, err := base64.StdEncoding.DecodeString(parts[2])
		if err != nil {
			mustWrite(t, w, "535 5.7.8 auth decode error\r\n")
			return
		}
		fields := strings.Split(string(raw), "\x00")
		if len(fields) == 3 {
			s.AuthUser = fields[1]
		}
		s.Authenticated = true
		mustWrite(t, w, "235 2.7.0 Authentication successful\r\n")

	case len(parts) >= 2 && strings.EqualFold(parts[1], "PLAIN"):
		mustWrite(t, w, "334\r\n")
		requireFlush(t, w.(*bufio.Writer))
		respLine, err := readLine(r)
		if err != nil {
			return
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(respLine))
		if err == nil {
			fields := strings.Split(string(raw), "\x00")
			if len(fields) == 3 {
				s.AuthUser = fields[1]
			}
			s.Authenticated = true
			mustWrite(t, w, "235 2.7.0 Authentication successful\r\n")
		} else {
			mustWrite(t, w, "535 5.7.8 auth decode error\r\n")
		}

	default:
		mustWrite(t, w, "504 5.5.2 Unrecognized authentication type\r\n")
	}
}

func requireFlush(t *testing.T, w *bufio.Writer) {
	t.Helper()
	if err := w.Flush(); err != nil {
		t.Fatalf("flush writer: %v", err)
	}
}

// --- scripted handlers exposed to tests ---

// handlerFullSession is the canonical happy-path handler with STARTTLS support.
func handlerFullSession(t *testing.T, s *smtpSession, conn net.Conn) {
	sessionLoop(t, s, conn, true)
}

// handlerNoStartTLS is the happy-path handler that does NOT advertise STARTTLS,
// forcing the sendWithSTARTTLS code path to fall back to plaintext.
func handlerNoStartTLS(t *testing.T, s *smtpSession, conn net.Conn) {
	sessionLoop(t, s, conn, false)
}

// handlerAuthFailure answers EHLO (with STARTTLS) then rejects AUTH (535).
func handlerAuthFailure(t *testing.T, s *smtpSession, conn net.Conn) {
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
			mustWrite(t, w, "535 5.7.8 Authentication credentials invalid\r\n")
		case "QUIT":
			s.Quit = true
			mustWrite(t, w, "221 Bye\r\n")
			return
		default:
			mustWrite(t, w, "500 unknown\r\n")
		}
	}
}

// handlerMailFromFailure accepts AUTH then rejects MAIL FROM (550).
func handlerMailFromFailure(t *testing.T, s *smtpSession, conn net.Conn) {
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
			mustWrite(t, w, "550 5.1.0 Sender address rejected\r\n")
		case "RCPT":
			mustWrite(t, w, "250 2.1.5 Ok\r\n")
		case "QUIT":
			s.Quit = true
			mustWrite(t, w, "221 Bye\r\n")
			return
		default:
			mustWrite(t, w, "500 unknown\r\n")
		}
	}
}

// handlerRcptFailure accepts AUTH + MAIL but rejects RCPT TO (550).
func handlerRcptFailure(t *testing.T, s *smtpSession, conn net.Conn) {
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
			mustWrite(t, w, "250 2.1.0 Ok\r\n")
		case "RCPT":
			mustWrite(t, w, "550 5.1.1 Recipient address rejected\r\n")
		case "QUIT":
			s.Quit = true
			mustWrite(t, w, "221 Bye\r\n")
			return
		default:
			mustWrite(t, w, "500 unknown\r\n")
		}
	}
}

// handlerDataFailure accepts everything up to DATA but returns 554 after the
// body, covering the dataCloser.Close() error path in doSend.
func handlerDataFailure(t *testing.T, s *smtpSession, conn net.Conn) {
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
			mustWrite(t, w, "250 2.1.0 Ok\r\n")
		case "RCPT":
			mustWrite(t, w, "250 2.1.5 Ok\r\n")
		case "DATA":
			mustWrite(t, w, "354 End data with <CR><LF>.<CR><LF>\r\n")
			requireFlush(t, w)
			_, _ = readDATA(r)
			mustWrite(t, w, "554 5.7.1 Message rejected\r\n")
		case "QUIT":
			s.Quit = true
			mustWrite(t, w, "221 Bye\r\n")
			return
		default:
			mustWrite(t, w, "500 unknown\r\n")
		}
	}
}
