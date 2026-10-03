package activitypub

import (
	"crypto/tls"
	"net/http"
	"time"
)

// NewAuthenticatedMediaHTTPClient shares federation's DNS-pinned SSRF policy
// and exact lab-peer exceptions. Callers supply trust roots, never a disabled
// verifier. HTTP redirects are not followed: authenticated Matrix media uses
// its own signed destination, and must not become an arbitrary URL proxy.
func NewAuthenticatedMediaHTTPClient(tlsConfig *tls.Config) *http.Client {
	transport := ssrfSafeTransport.Clone()
	if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig.Clone()
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: labCheckedTransport{next: transport}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}
