package smtp

import (
	"bytes"
	"crypto/tls"
	"io"
	"io/fs"
	"mime"
	"net"
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

	server := config.C.SmtpServer + ":" + port

	// Parse connection timeout (seconds, default 10)
	timeoutSec := 10
	if config.C.SmtpTimeout != "" {
		if t, err := strconv.Atoi(config.C.SmtpTimeout); err == nil && t > 0 {
			timeoutSec = t
		}
	}

	// Determine TLS mode (default: true, encrypted)
	useTLS := strings.ToLower(strings.TrimSpace(config.C.SmtpTLS)) != "false" &&
		strings.TrimSpace(config.C.SmtpTLS) != "0"

	auth := &tlsPlainAuth{
		identity: "",
		username: config.C.SmtpUsername,
		password: config.C.SmtpPassword,
		host:     config.C.SmtpServer,
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
		ServerName: config.C.SmtpServer,
	}

	dialer := &net.Dialer{Timeout: d.timeout}

	conn, err := tls.DialWithDialer(dialer, "tcp", d.server, tlsConfig)
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

// sendWithSTARTTLS sends email over a plaintext connection with optional STARTTLS upgrade.
// If the server supports STARTTLS, the connection is upgraded to TLS; otherwise it falls
// back to plaintext transmission (with a warning log).
// Connection timeout is controlled by Drive.timeout, SMTP operations have a 30s deadline.
func (d Drive) sendWithSTARTTLS(from string, to []string, body []byte) error {
	conn, err := net.DialTimeout("tcp", d.server, d.timeout)
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
		tlsConfig := &tls.Config{ServerName: config.C.SmtpServer}
		if err := client.StartTLS(tlsConfig); err != nil {
			// STARTTLS failed; log warning and fall back to plaintext
			log.Warn("SMTP STARTTLS failed, falling back to plaintext: %v", err)
		}
	} else {
		log.Warn("SMTP server does not support STARTTLS, sending in plaintext")
	}

	return d.doSend(client, from, to, body)
}

// doSend performs SMTP authentication and sends the email on an established client.
func (d Drive) doSend(client *smtp.Client, from string, to []string, body []byte) error {
	// Authenticate
	if err := client.Auth(d.auth); err != nil {
		return errors.Wrap(err, "SMTP auth failed")
	}

	// Set sender
	if err := client.Mail(from); err != nil {
		return errors.Wrap(err, "SMTP MAIL FROM failed")
	}

	// Set recipients
	for _, addr := range to {
		if err := client.Rcpt(addr); err != nil {
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

	if d.useTLS {
		err = d.sendWithTLS(config.C.SmtpUsername, addrs, body)
	} else {
		err = d.sendWithSTARTTLS(config.C.SmtpUsername, addrs, body)
	}

	return
}

func (d Drive) SendMail(to string, cc string, mailObject *types.Mail) (err error) {
	defer func() {
		for _, att := range mailObject.Attachments {
			_ = att.File.Close()
		}
	}()

	msg := enmime.Builder().
		From("", config.C.SmtpUsername)

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

	return
}