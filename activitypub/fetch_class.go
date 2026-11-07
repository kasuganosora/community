package activitypub

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
)

// Stable remote-fetch classes. An unsigned actor 401 and a stale avatar 403
// are authorization failures, not an account or node suspension.
const (
	RemoteFetchTimeout         = "timeout"
	RemoteFetchFailed          = "fetch_failed"
	RemoteFetchNotFound        = "not_found"
	RemoteFetchUnauthorized    = "unauthorized"
	RemoteFetchForbidden       = "forbidden"
	RemoteFetchDomainSuspended = "domain_suspended"
)

// ClassifyRemoteFetch maps a federation GET failure to a stable class.
// Local node policy (ErrDeliveryBlocked) is domain_suspended. HTTP 401 and
// 403 stay unauthorized and forbidden. Empty string means there was no error.
func ClassifyRemoteFetch(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrDeliveryBlocked) {
		return RemoteFetchDomainSuspended
	}
	if status := fetchStatusCode(err); status != 0 {
		switch status {
		case http.StatusUnauthorized:
			return RemoteFetchUnauthorized
		case http.StatusForbidden:
			return RemoteFetchForbidden
		case http.StatusNotFound, http.StatusGone:
			return RemoteFetchNotFound
		case http.StatusRequestTimeout:
			return RemoteFetchTimeout
		default:
			return RemoteFetchFailed
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return RemoteFetchTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return RemoteFetchTimeout
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return RemoteFetchTimeout
	}
	return RemoteFetchFailed
}
