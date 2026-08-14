package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/identity"
)

// A Grant is what one request's verified access token authorizes.
//
// It is the only description of a caller the transport works from. Nothing here
// is read from a header, a cookie, a query parameter, or a session id: every
// field comes out of a token the authorizer verified.
type Grant struct {
	// Principal is the account the token acts for.
	Principal identity.Principal

	// ClientID is the OAuth client the token was issued to.
	ClientID string

	// Resource is the RFC 8707 resource indicator the token is audienced for.
	Resource string

	// Scopes are the granted scopes.
	Scopes []string

	// Family is the token family, which is the unit of revocation. It matches a
	// revocation against a live session and is deliberately not part of the
	// session binding: rotating a refresh token keeps the family, and a session
	// has to survive that.
	Family string
}

// An HTTPAuthorizer authenticates a Streamable HTTP request and reports what its
// token authorizes.
//
// The interface lives with its consumer. This package needs three things from
// the OAuth layer and states them here, so the transport can be tested without
// an authorization server and so no OAuth type leaks into the transport's own
// logic. [NewOAuthAuthorizer] adapts the real server to it.
type HTTPAuthorizer interface {
	// Middleware authenticates the request and, on success, calls the next
	// handler with the verified token on the request context. On failure it
	// writes the challenge itself and does not call next.
	//
	// An implementation must take the credential from the Authorization header
	// and from nowhere else.
	Middleware() func(http.Handler) http.Handler

	// Grant returns the grant the middleware put on ctx.
	Grant(ctx context.Context) (Grant, error)

	// ProtectedResourceMetadataHandler serves the RFC 9728 document. It is
	// served unauthenticated, which is the point of it.
	ProtectedResourceMetadataHandler() http.Handler
}

// PrincipalSource adapts an HTTPAuthorizer to identity.VerifiedTokenSource, so
// the server's principal resolver reads the same verified token the transport
// authenticated the request with.
//
// This is the one seam through which a principal reaches a tool handler in
// remote mode. A nil authorizer yields a source that always refuses, which
// surfaces as ErrNoPrincipal rather than as a nil dereference inside a request.
func PrincipalSource(authorizer HTTPAuthorizer) identity.VerifiedTokenSource {
	return principalSource{authorizer: authorizer}
}

type principalSource struct{ authorizer HTTPAuthorizer }

// PrincipalFromToken returns the principal of the verified token on ctx.
func (s principalSource) PrincipalFromToken(ctx context.Context) (identity.Principal, error) {
	if s.authorizer == nil {
		return identity.Principal{}, fmt.Errorf("no authorizer is wired: %w", ErrMissingDependency)
	}
	grant, err := s.authorizer.Grant(ctx)
	if err != nil {
		return identity.Principal{}, fmt.Errorf("reading the verified token: %w", err)
	}
	return grant.Principal, nil
}

// A Revocation names an authorization that has been withdrawn: a revoked token
// family, a withdrawn consent, a disabled client, or an unlinked Garmin account.
//
// Every field is a selector and an empty field matches everything. A revocation
// with no field set therefore selects every session, which is why it is ignored
// rather than obeyed: one buggy producer must not be able to disconnect a whole
// deployment.
type Revocation struct {
	// Principal selects one account. The zero Principal matches any.
	Principal identity.Principal

	// ClientID selects one OAuth client. The empty string matches any.
	ClientID string

	// Family selects one token family. The empty string matches any.
	Family string
}

// isEmpty reports whether the revocation names nothing at all.
func (r Revocation) isEmpty() bool {
	return !r.Principal.IsValid() && r.ClientID == "" && r.Family == ""
}

// matches reports whether the revocation covers a bound session.
func (r Revocation) matches(record sessionRecord) bool {
	if r.Principal.IsValid() && r.Principal.ID() != record.binding.principal {
		return false
	}
	if r.ClientID != "" && r.ClientID != record.binding.clientID {
		return false
	}
	if r.Family != "" && r.Family != record.family {
		return false
	}
	return true
}

// A RevocationSource reports withdrawn authorizations to the transport.
//
// The signal is produced elsewhere — by the OAuth server when a token family or
// a consent dies, and by Garmin-account unlinking — so this package consumes an
// interface rather than owning the state. A closed channel means no further
// revocations will arrive and ends the watch; ctx bounds the watch itself.
//
// An implementation may deliver a revocation more than once. Termination is
// idempotent, so a duplicate costs nothing.
type RevocationSource interface {
	// Revocations returns the channel of revocation events for ctx.
	Revocations(ctx context.Context) <-chan Revocation
}

// grantBinding reduces a grant to the comparable tuple a session is bound to.
//
// Scopes are sorted, deduplicated and joined so the tuple stays a comparable
// value: two grants carrying the same scopes in a different order are the same
// authorization, and a binding must not depend on the order an authorization
// server happened to emit them in.
func grantBinding(grant Grant) sessionBinding {
	scopes := slices.Clone(grant.Scopes)
	slices.Sort(scopes)
	return sessionBinding{
		principal: grant.Principal.ID(),
		clientID:  grant.ClientID,
		resource:  grant.Resource,
		scopes:    strings.Join(slices.Compact(scopes), " "),
	}
}
