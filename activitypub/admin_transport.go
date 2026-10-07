package activitypub

import (
	"context"
	"net"
)

// DialPublicEndpoint reuses the DNS-pinned federation policy for administrative
// SMTP diagnostics. It never resolves a validated name for a second dial.
func DialPublicEndpoint(ctx context.Context, network, address string) (net.Conn, error) {
	return ssrfSafeDialContext(ctx, network, address)
}
