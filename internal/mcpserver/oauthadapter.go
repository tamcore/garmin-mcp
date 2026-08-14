package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/tamcore/garmin-mcp/internal/oauthserver"
)

// An OAuthAuthorizer adapts the OAuth protected-resource server to the seams
// this package consumes.
//
// It is deliberately thin. The challenge wording, the missing-versus-invalid
// distinction, the exact audience check, and the RFC 9728 document all live in
// internal/oauthserver and are used from here rather than reimplemented: a
// second copy of a WWW-Authenticate builder is a second thing to get wrong, and
// only one of the two would be the copy the conformance tests read.
//
// Three entry points are used and nothing else:
//
//   - [oauthserver.Server.RequireBearerToken] authenticates the request. It
//     reads the Authorization header and nowhere else, and it emits the RFC 6750
//     challenge with resource_metadata: bare for a request that presented no
//     credential, error="invalid_token" for one that presented an unusable
//     credential, and 403 error="insufficient_scope" for a grant that is too
//     narrow.
//   - [oauthserver.TokenInfoFromContext] reads back what that middleware
//     verified.
//   - [oauthserver.Server.ProtectedResourceMetadataHandler] serves the RFC 9728
//     document.
type OAuthAuthorizer struct {
	server   *oauthserver.Server
	required []oauthserver.Scope
}

// NewOAuthAuthorizer adapts server, requiring the given scopes on every request.
//
// The required scopes are the floor for reaching the transport at all. Per-tool
// scope is a separate, narrower gate the policy applies inside the middleware
// chain; this one only decides whether a caller may speak MCP here.
//
// The scope slice is copied, so a caller that keeps mutating its own slice
// cannot widen or narrow the requirement of an authorizer already in service.
func NewOAuthAuthorizer(
	server *oauthserver.Server, required ...oauthserver.Scope,
) (*OAuthAuthorizer, error) {
	if server == nil {
		return nil, fmt.Errorf("oauth server is nil: %w", ErrMissingDependency)
	}
	return &OAuthAuthorizer{server: server, required: slices.Clone(required)}, nil
}

// Middleware returns the protected-resource middleware.
func (a *OAuthAuthorizer) Middleware() func(http.Handler) http.Handler {
	return a.server.RequireBearerToken(a.required...)
}

// Grant returns what the verified token on ctx authorizes.
//
// It fails when ctx carries no verified token, which is the case for a handler
// wired without the middleware. Returning a zero Grant instead would hand the
// transport an unattributed request that looks attributed.
func (a *OAuthAuthorizer) Grant(ctx context.Context) (Grant, error) {
	info, err := oauthserver.TokenInfoFromContext(ctx)
	if err != nil {
		return Grant{}, fmt.Errorf("reading the verified token: %w", err)
	}
	return Grant{
		Principal: info.Principal,
		ClientID:  info.ClientID,
		Resource:  info.Resource.String(),
		Scopes:    info.Scopes.Strings(),
		Family:    string(info.Family),
	}, nil
}

// ProtectedResourceMetadataHandler serves the RFC 9728 document.
func (a *OAuthAuthorizer) ProtectedResourceMetadataHandler() http.Handler {
	return a.server.ProtectedResourceMetadataHandler()
}
