// Package reqid carries the HTTP request id through a context.Context so
// outbound calls and logs can be correlated with the incoming request.
package reqid

import (
	"context"

	"github.com/google/uuid"
)

// Header is the header name used for request ids, incoming and outgoing.
const Header = "X-Request-Id"

type key struct{}

// With returns ctx carrying id.
func With(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, key{}, id)
}

// From returns the request id in ctx, or "".
func From(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(key{}).(string)
	return s
}

// Ensure returns ctx and its request id, generating one when absent.
func Ensure(ctx context.Context) (context.Context, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	if id := From(ctx); id != "" {
		return ctx, id
	}
	id := uuid.New().String()
	return With(ctx, id), id
}
