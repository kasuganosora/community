package smtp

import (
	"fmt"
	"net"
	"net/smtp"
	"strings"
)

// Authentication implements the authentication modes offered by the admin UI.
// The caller must establish verified TLS before using it on a remote server.
func Authentication(method, username, password, host string) smtp.Auth {
	var auth smtp.Auth
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "LOGIN":
		auth = &loginAuth{username: username, password: password}
	case "CRAM-MD5":
		auth = smtp.CRAMMD5Auth(username, password)
	case "", "PLAIN":
		auth = &tlsPlainAuth{username: username, password: password, host: host}
	default:
		return &guardedAuth{host: host}
	}
	return &guardedAuth{host: host, next: auth}
}

// ImplicitTLS normalizes the shared sender/diagnostic TLS setting. False/0
// selects STARTTLS, never an unverified remote plaintext transport.
func ImplicitTLS(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value != "false" && value != "0"
}
func LoopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

type guardedAuth struct {
	host string
	next smtp.Auth
}

func (a *guardedAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if a.next == nil {
		return "", nil, fmt.Errorf("unsupported SMTP authentication method")
	}
	if server == nil || server.Name != a.host {
		return "", nil, fmt.Errorf("SMTP authentication host mismatch")
	}
	if !server.TLS && !LoopbackHost(a.host) {
		return "", nil, fmt.Errorf("SMTP authentication requires TLS")
	}
	return a.next.Start(server)
}
func (a *guardedAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if a.next == nil {
		return nil, fmt.Errorf("unsupported SMTP authentication method")
	}
	return a.next.Next(challenge, more)
}

type loginAuth struct {
	username, password string
	step               int
}

func (a *loginAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	a.step = 0
	return "LOGIN", nil, nil
}
func (a *loginAuth) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch a.step {
	case 0:
		a.step++
		return []byte(a.username), nil
	case 1:
		a.step++
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("unexpected SMTP LOGIN challenge")
	}
}
