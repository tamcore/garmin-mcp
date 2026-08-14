package identity

import (
	"context"
	"fmt"
)

// A VerifiedTokenSource yields the principal that a request's already-verified
// access token authorizes.
//
// The interface lives here, with its consumer, rather than in the package that
// verifies tokens: this package must not import the OAuth server, and does not
// need to know how a token is verified — only that it was.
//
// The single context parameter is the enforcement point. An implementation reads
// the verified token the protected-resource middleware put on the request
// context and nothing else. There is no parameter through which a tool argument,
// a header, a cookie, or a session id could travel.
type VerifiedTokenSource interface {
	// PrincipalFromToken returns the principal the verified access token on ctx
	// authorizes, or an error when ctx carries no verified token.
	PrincipalFromToken(ctx context.Context) (Principal, error)
}

// A BearerResolver resolves the principal from the verified bearer token of the
// request, and from nothing else.
//
// It is the remote-transport counterpart of [StdioResolver]: stdio binds one
// principal for the life of the process, whereas here every request carries its
// own proof. Both satisfy [Resolver], so the server depends on neither.
//
// There is no anonymous fallback and no default principal. A request whose token
// cannot be read, or whose principal does not validate, is refused.
type BearerResolver struct {
	source VerifiedTokenSource
}

// NewBearerResolver binds a resolver to source.
//
// A nil source is a wiring mistake and returns ErrNoTokenSource, because a
// resolver that cannot consult a token would have to invent an answer.
func NewBearerResolver(source VerifiedTokenSource) (*BearerResolver, error) {
	if source == nil {
		return nil, fmt.Errorf("bearer resolver: %w", ErrNoTokenSource)
	}
	return &BearerResolver{source: source}, nil
}

// Resolve returns the principal the request's verified token authorizes.
//
// Every failure mode collapses to ErrNoPrincipal, including a source that
// reports success but yields the zero Principal: from the handler's point of
// view there is exactly one outcome that is not a refusal, and that is a valid
// principal proved by a verified token.
//
// A principal already present on ctx is deliberately not consulted. Whatever the
// transport, a middleware, or a caller planted there is irrelevant; the token
// decides.
//
// The source's error is not wrapped into the returned one. It is produced by the
// token layer and may name the reason a credential failed; this error travels
// towards a caller-facing refusal, so it carries only the sentinel.
func (r *BearerResolver) Resolve(ctx context.Context) (Principal, error) {
	if r == nil || r.source == nil {
		return Principal{}, fmt.Errorf("bearer resolver holds no token source: %w", ErrNoPrincipal)
	}

	principal, err := r.source.PrincipalFromToken(ctx)
	if err != nil {
		return Principal{}, fmt.Errorf(
			"the request carries no verified token: %w", ErrNoPrincipal)
	}
	if !principal.IsValid() {
		return Principal{}, fmt.Errorf(
			"the verified token yielded no valid principal: %w", ErrNoPrincipal)
	}
	return principal, nil
}
