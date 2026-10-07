package smtp

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jhillyerd/enmime"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/email/types"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
)

// isValidEmailAddress validates an email address using the standard library
// net/mail parser, which performs basic RFC 5322 address validation.
func isValidEmailAddress(addr string) bool {
	_, err := mail.ParseAddress(addr)

	return err == nil
}

// tlsPlainAuth implements smtp.Auth for PLAIN authentication without the
// built-in TLS connection check. This is safe because we ensure the
// connection is encrypted at the transport level before calling Auth
// (either via implicit TLS or STARTTLS upgrade).
type tlsPlainAuth struct {
	identity, username, password, host string
}

func (a *tlsPlainAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	// Encode as: identity\0username\0password per RFC 4616
	resp := []byte(a.identity + "\x00" + a.username + "\x00" + a.password)

	return "PLAIN", resp, nil
}
func (a *tlsPlainAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		// We've already sent everything.
		return nil, errors.New("unexpected server challenge")
	}

	return nil, nil
}

type Drive struct {
	server  string
	auth    smtp.Auth
	useTLS  bool
	timeout time.Duration
}

func NewDrive() *Drive {
	if config.C.SmtpServer == "" {
		return nil
	}

	port := config.C.SmtpPort
	if port == "" {
		port = "587"
	}

	server := net.JoinHostPort(config.C.SmtpServer, port)

	// Parse connection timeout (seconds, default 10)
	timeoutSec := 10

	if config.C.SmtpTimeout != "" {
		if t, err := strconv.Atoi(config.C.SmtpTimeout); err == nil && t > 0 {
			timeoutSec = t
		}
	}

	// Determine TLS mode (default: true, encrypted)
	useTLS := ImplicitTLS(config.C.SmtpTLS)

	var auth smtp.Auth
	if config.C.SmtpUsername != "" {
		auth = Authentication(config.C.SmtpAuthMethod, config.C.SmtpUsername, config.C.SmtpPassword, config.C.SmtpServer)
	}

	return &Drive{
		server:  server,
		auth:    auth,
		useTLS:  useTLS,
		timeout: time.Duration(timeoutSec) * time.Second,
	}
}

// sendWithTLS sends email over an implicit TLS connection (typically port 465).
// Connection timeout is controlled by Drive.timeout, SMTP operations have a 30s deadline.
func (d Drive) sendWithTLS(from string, to []string, body []byte) error {
	tlsConfig := &tls.Config{
		ServerName: config.C.SmtpServer, MinVersion: tls.VersionTLS12,
	}

	dialer := &net.Dialer{Timeout: d.timeout}

	conn, err := (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(context.Background(), "tcp", d.server)
	if err != nil {
		return errors.Wrap(err, "TLS dial failed")
	}

	defer func() { _ = conn.Close() }()

	// 30s deadline for all subsequent SMTP operations
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	client, err := smtp.NewClient(conn, config.C.SmtpServer)
	if err != nil {
		return errors.Wrap(err, "create SMTP client failed")
	}

	defer func() { _ = client.Close() }()

	return d.doSend(client, from, to, body)
}

// sendWithSTARTTLS requires a verified STARTTLS upgrade for remote servers.
// Only an explicitly configured loopback relay may use plaintext.
// Connection timeout is controlled by Drive.timeout, SMTP operations have a 30s deadline.
func (d Drive) sendWithSTARTTLS(from string, to []string, body []byte) error {
	conn, err := (&net.Dialer{Timeout: d.timeout}).DialContext(context.Background(), "tcp", d.server)
	if err != nil {
		return errors.Wrap(err, "dial failed")
	}

	defer func() { _ = conn.Close() }()

	// 30s deadline for all subsequent SMTP operations (including STARTTLS handshake)
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	client, err := smtp.NewClient(conn, config.C.SmtpServer)
	if err != nil {
		return errors.Wrap(err, "create SMTP client failed")
	}

	defer func() { _ = client.Close() }()

	// Attempt STARTTLS upgrade if server supports it
	if ok, _ := client.Extension("STARTTLS"); ok {
		tlsConfig := &tls.Config{ServerName: config.C.SmtpServer, MinVersion: tls.VersionTLS12}
		err := client.StartTLS(tlsConfig)
		if err != nil {
			// Never downgrade a failed TLS negotiation to plaintext.
			return errors.Wrap(err, "SMTP STARTTLS failed")
		}
	} else {
		host := config.C.SmtpServer
		if !LoopbackHost(host) {
			return errors.New("SMTP server does not support STARTTLS")
		}
		log.Warn("Using plaintext SMTP on explicitly configured loopback server")
	}

	return d.doSend(client, from, to, body)
}

// doSend performs SMTP authentication and sends the email on an established client.
func (d Drive) doSend(client *smtp.Client, from string, to []string, body []byte) error {
	// Authenticate
	if d.auth != nil {
		if err := client.Auth(d.auth); err != nil {
			return errors.Wrap(err, "SMTP auth failed")
		}
	}

	// Set sender
	if err := client.Mail(from); err != nil {
		return errors.Wrap(err, "SMTP MAIL FROM failed")
	}

	// Set recipients
	for _, addr := range to {
		err := client.Rcpt(addr)
		if err != nil {
			return errors.Wrapf(err, "SMTP RCPT TO %s failed", addr)
		}
	}

	// Write message body
	w, err := client.Data()
	if err != nil {
		return errors.Wrap(err, "SMTP DATA failed")
	}

	if _, err := w.Write(body); err != nil {
		return errors.Wrap(err, "write email body failed")
	}

	if err := w.Close(); err != nil {
		return errors.Wrap(err, "close data writer failed")
	}

	return client.Quit()
}

func (d Drive) Send(to string, cc string, body []byte) (err error) {
	if d.server == "" {
		return
	}

	toAddrs := strings.Split(to, ";")
	ccAddrs := strings.Split(cc, ";")

	addrs := make([]string, 0, len(toAddrs)+len(ccAddrs))
	if len(toAddrs) > 0 {
		addrs = append(addrs, toAddrs...)
	}

	if len(ccAddrs) > 0 {
		addrs = append(addrs, ccAddrs...)
	}

	addrs = utils.FilterDuplicateAndEmptyString(addrs)

	if len(addrs) == 0 {
		return
	}

	// Validate recipient email addresses to avoid sending malformed requests
	// to the SMTP server (which could cause connection instability).
	for _, addr := range addrs {
		if !isValidEmailAddress(addr) {
			return errors.Errorf("invalid recipient email address: %s", addr)
		}
	}

	_, sender, senderErr := configuredSender()
	if senderErr != nil {
		return senderErr
	}

	if d.useTLS {
		err = d.sendWithTLS(sender, addrs, body)
	} else {
		err = d.sendWithSTARTTLS(sender, addrs, body)
	}

	return
}

func (d Drive) SendMail(to string, cc string, mailObject *types.Mail) (err error) {
	defer func() {
		for _, att := range mailObject.Attachments {
			_ = att.File.Close()
		}
	}()

	senderName, senderAddress, senderErr := configuredSender()
	if senderErr != nil {
		return senderErr
	}
	msg := enmime.Builder().From(senderName, senderAddress)

	toAddrs := utils.FilterDuplicateAndEmptyString(strings.Split(to, ";"))

	for _, addr := range toAddrs {
		msg = msg.To("", addr)
	}

	ccAddrs := utils.FilterDuplicateAndEmptyString(strings.Split(cc, ";"))

	for _, addr := range ccAddrs {
		msg = msg.CC("", addr)
	}

	msg = msg.Subject(mailObject.Subject)

	if mailObject.TextBody != "" {
		msg = msg.Text([]byte(mailObject.TextBody))
	}

	if mailObject.Body != "" {
		msg = msg.HTML([]byte(mailObject.Body))
	}

	for _, att := range mailObject.Attachments {
		var fileStat fs.FileInfo

		fileStat, err = att.File.Stat()
		if err != nil {
			return err
		}

		contentType := mime.TypeByExtension(filepath.Ext(fileStat.Name()))
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		var fileData []byte

		fileData, err = io.ReadAll(att.File)
		if err != nil {
			return err
		}

		if att.Name == "" {
			att.Name = fileStat.Name()
		}

		if att.ContentId != "" {
			msg = msg.AddInline(fileData, contentType, att.Name, att.ContentId)
		} else {
			msg = msg.AddAttachment(fileData, contentType, att.Name)
		}
	}

	buf := &bytes.Buffer{}

	root, err := msg.Build()
	if err != nil {
		err = errors.WithMessagef(err, "build mail failed")

		return err
	}

	err = root.Encode(buf)
	if err != nil {
		err = errors.WithMessagef(err, "encode mail failed")

		return err
	}

	err = d.Send(to, cc, buf.Bytes())
	if err != nil {
		err = errors.WithMessagef(err, "send mail failed")
	}

	return err
}

// Use the same address for the visible header and SMTP envelope. Authentication
// usernames need not be email addresses; an explicit SMTP_FROM takes precedence.
func configuredSender() (name, address string, err error) {
	if strings.TrimSpace(config.C.SmtpFrom) == "" {
		return "", config.C.SmtpUsername, nil
	}
	parsed, parseErr := mail.ParseAddress(config.C.SmtpFrom)
	if parseErr != nil {
		return "", "", errors.New("invalid SMTP sender address")
	}
	return parsed.Name, parsed.Address, nil
}
