// Package secretredact keeps a request's own credentials out of error
// responses and logs. Error text can carry fragments of the request (driver
// errors, wrapped parse errors, dev-mode details); whatever path produced it,
// the bearer token the client sent must never be echoed back or logged.
package secretredact

import (
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
		add(v)
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

// Middleware records the request's secrets and scrubs them from any error
// response body (status >= 400). Successful bodies are untouched: a login or
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
	secrets []string
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
	if _, err := w.ResponseWriter.Write(w.scrub(data)); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (w *writer) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
