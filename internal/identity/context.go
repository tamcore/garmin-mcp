package identity

import (
	"context"
	"fmt"
)

// principalKey is the context key under which a resolved principal is carried.
//
// The type is unexported and unique to this package, so no other package can
// produce a value that collides with it. A caller outside this package therefore
// cannot plant a principal on a context with context.WithValue; the only way in
// is WithPrincipal, which accepts a Principal, which in turn can only come from
// NewPrincipal.
type principalKey struct{}

// WithPrincipal returns a copy of ctx carrying principal.
//
// An invalid principal is not stored. The resulting context reads as unresolved,
// so a mistake at the resolution seam surfaces as ErrNoPrincipal in the handler
// rather than as a silently empty principal.
func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	if !principal.IsValid() {
		return ctx
	}
	return context.WithValue(ctx, principalKey{}, principal)
}

// FromContext returns the principal the server resolved for this request.
//
// It returns ErrNoPrincipal, and the zero Principal, when ctx carries none.
// There is no default principal and no fallback: a handler that would reach
// Garmin without a resolved principal must fail instead.
func FromContext(ctx context.Context) (Principal, error) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	if !ok || !principal.IsValid() {
		return Principal{}, fmt.Errorf("request context carries no principal: %w", ErrNoPrincipal)
	}
	return principal, nil
}
