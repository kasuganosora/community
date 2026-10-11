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

// NewSafeRoundTripper returns an SSRF-checked transport (dial-time address
// validation of every DNS answer, lab-peer exceptions only) that uses
// tlsConfig for TLS. It has no overall deadline: callers bound requests with a
// context so large media can stream. Each call returns an independent
// connection pool.
func NewSafeRoundTripper(tlsConfig *tls.Config, responseHeaderTimeout time.Duration) http.RoundTripper {
	transport := ssrfSafeTransport.Clone()
	if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig.Clone()
	}
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	return labCheckedTransport{next: transport}
}

// NewMediaFetchHTTPClient is the unauthenticated, SSRF-checked client used to
// follow a remote media server's redirect to its CDN or object store. Up to
// three http(s) redirects are followed; every hop is dialed through the same
// address validation. Deadlines come from the request context.
func NewMediaFetchHTTPClient(responseHeaderTimeout time.Duration) *http.Client {
	return &http.Client{
		Transport: NewSafeRoundTripper(nil, responseHeaderTimeout),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}
