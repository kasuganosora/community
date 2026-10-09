// Package clientip decides which address c.ClientIP() reports. Every
// per-client feature (rate limits, audit and request logs, captcha, signup
// records, the media proxy limits) reads the address through gin's
// ClientIP(), so configuring the engine once here keeps them consistent.
//
// Forwarding headers are only believed when the direct TCP peer is one of
// the trusted proxies. A client that talks to the server (or sends its own
// X-Forwarded-For through the proxy) cannot pick its address: X-Real-IP is
// overwritten by the reverse proxy from $remote_addr, and X-Forwarded-For is
// read from the right, skipping trusted hops.
package clientip

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
)

// Defaults: loopback and private ranges (a proxy on the same host or on a
// Docker bridge) and the headers our nginx sets.
const (
	DefaultTrustedProxies = "127.0.0.1,::1,172.16.0.0/12,10.0.0.0/8,192.168.0.0/16"
	DefaultHeaders        = "X-Real-IP,X-Forwarded-For"
)

func split(raw, def string) []string {
	if strings.TrimSpace(raw) == "" {
		raw = def
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}

// TrustedProxies returns the configured CIDRs/IPs (TRUSTED_PROXIES).
func TrustedProxies() []string { return split(config.C.TrustedProxies, DefaultTrustedProxies) }

// Headers returns the forwarding headers consulted, in order
// (TRUSTED_PROXY_HEADERS).
func Headers() []string {
	h := split(config.C.TrustedProxyHeaders, DefaultHeaders)
	for i := range h {
		h[i] = http.CanonicalHeaderKey(h[i])
	}

	return h
}

// Configure applies the trusted proxy settings to an engine.
func Configure(app *gin.Engine) error {
	if err := app.SetTrustedProxies(TrustedProxies()); err != nil {
		return fmt.Errorf("TRUSTED_PROXIES: %w", err)
	}
	app.RemoteIPHeaders = Headers()
	app.ForwardedByClientIP = true

	return nil
}
