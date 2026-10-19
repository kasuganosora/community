// Package secretredact keeps a request's own credentials out of error
// responses and logs. Error text can carry fragments of the request (driver
// errors, wrapped parse errors, dev-mode details); whatever path produced it,
// the bearer token the client sent must never be echoed back or logged.
package secretredact

import (
	"bufio"
	"encoding/base64"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const contextKey = "secretredact.secrets"

// Placeholder replaces every occurrence of a request secret.
const Placeholder = "[REDACTED]"

// minSecretLen avoids replacing short, common substrings.
const minSecretLen = 12

// Collect returns the credentials a request presented: the Authorization
// value (and its bearer part), access_token in the query, the app-token
// cookie and WebSocket subprotocol tokens.
func Collect(r *http.Request) []string {
	if r == nil {
		return nil
	}
	found := []string{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if len(v) >= minSecretLen {
			found = append(found, v)
		}
	}
	if auth := r.Header.Get("Authorization"); auth != "" {
		add(auth)
		if _, token, ok := strings.Cut(auth, " "); ok {
			add(token)
		}
	}
	if r.URL != nil {
		for _, v := range r.URL.Query()["access_token"] {
			add(v)
		}
	}
	if cookie, err := r.Cookie("app-token"); err == nil {
		add(cookie.Value)
	}
	for _, v := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
		if strings.TrimSpace(v) == PublicSubprotocol {
			continue
		}
		add(v)
		if token, ok := DecodeBearerSubprotocol(strings.TrimSpace(v)); ok {
			add(token)
		}
	}
	return found
}

// Apply replaces every secret in s.
func Apply(s string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" && strings.Contains(s, secret) {
			s = strings.ReplaceAll(s, secret, Placeholder)
		}
	}
	return s
}

// Scrub removes the current request's credentials from s.
func Scrub(c *gin.Context, s string) string {
	if c == nil {
		return s
	}
	if v, ok := c.Get(contextKey); ok {
		if secrets, ok := v.([]string); ok {
			return Apply(s, secrets)
		}
	}
	return Apply(s, Collect(c.Request))
}

// Middleware records the request's secrets, scrubs them from every response
// header (see scrubHeaders) and from any error response body (status >= 400). Successful bodies are untouched: a login or
// token endpoint legitimately returns a new token.
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		secrets := Collect(c.Request)
		c.Set(contextKey, secrets)
		if len(secrets) == 0 {
			c.Next()
			return
		}
		c.Writer = &writer{ResponseWriter: c.Writer, secrets: secrets}
		c.Next()
	}
}

type writer struct {
	gin.ResponseWriter
	secrets         []string
	headersScrubbed bool
}

// PublicSubprotocol is the non-secret subprotocol our clients offer next to
// the bearer one; it must never be treated (and redacted) as a credential.
const PublicSubprotocol = "community.v1"

// ScrubHeader removes secrets from every response header value except
// Set-Cookie (a login/refresh may legitimately set the app-token cookie).
// It returns the names of the headers it changed.
func ScrubHeader(h http.Header, secrets []string) []string {
	var changed []string
	for name, values := range h {
		if http.CanonicalHeaderKey(name) == "Set-Cookie" {
			continue
		}
		for i, v := range values {
			if out := Apply(v, secrets); out != v {
				values[i] = out
				changed = append(changed, name)
			}
		}
	}
	return changed
}

// scrubHeaders runs once, right before the status line and headers go out,
// for every status: no response header should ever carry the request's own
// credential. Hijacked connections (WebSocket) write their own handshake,
// so the streaming handlers must also never select a token subprotocol.
func (w *writer) scrubHeaders() {
	if w.headersScrubbed {
		return
	}
	w.headersScrubbed = true
	ScrubHeader(w.ResponseWriter.Header(), w.secrets)
}

func (w *writer) WriteHeader(code int) {
	w.scrubHeaders()
	w.ResponseWriter.WriteHeader(code)
}

func (w *writer) WriteHeaderNow() {
	w.scrubHeaders()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *writer) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.scrubHeaders()
	return w.ResponseWriter.Hijack()
}

func (w *writer) scrub(data []byte) []byte {
	if w.ResponseWriter.Status() < 400 {
		return data
	}
	s := string(data)
	if out := Apply(s, w.secrets); out != s {
		w.ResponseWriter.Header().Del("Content-Length")
		return []byte(out)
	}
	return data
}

func (w *writer) Write(data []byte) (int, error) {
	w.scrubHeaders()
	if _, err := w.ResponseWriter.Write(w.scrub(data)); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (w *writer) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

// BearerSubprotocolPrefix marks a WebSocket subprotocol that carries an
// access token as unpadded base64url (browsers cannot send Authorization on
// a WebSocket, and raw tokens are not valid subprotocol tokens).
const BearerSubprotocolPrefix = "community.bearer."

// DecodeBearerSubprotocol returns the token carried by a
// "community.bearer.<base64url>" subprotocol.
func DecodeBearerSubprotocol(v string) (string, bool) {
	if !strings.HasPrefix(v, BearerSubprotocolPrefix) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(v, BearerSubprotocolPrefix))
	if err != nil || len(raw) == 0 {
		return "", false
	}
	return string(raw), true
}
