package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// grantedScopes reports what the caller's verified access token authorizes.
//
// It is the seam the tool policy declared and stdio leaves empty. The scopes come
// from the token the protected-resource middleware verified, and from nothing
// else: there is no parameter through which a tool argument, a header, or a
// session identifier could reach this decision.
//
// A request that carries no verified grant is an error rather than an empty scope
// set. The policy fails closed on an error, whereas an empty set would read as a
// caller who legitimately holds nothing — a different and weaker answer.
type grantedScopes struct {
	authorizer *mcpserver.OAuthAuthorizer
}

// The assertion this type exists for.
var _ policy.ScopeSource = grantedScopes{}

// newGrantedScopes binds the source to the authorizer that verified the token.
func newGrantedScopes(authorizer *mcpserver.OAuthAuthorizer) (grantedScopes, error) {
	if authorizer == nil {
		return grantedScopes{}, errors.New("cmd: the scope source needs an authorizer")
	}
	return grantedScopes{authorizer: authorizer}, nil
}

// GrantedScopes returns the scopes the request's verified token carries.
func (g grantedScopes) GrantedScopes(ctx context.Context) ([]policy.Scope, error) {
	grant, err := g.authorizer.Grant(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the granted scopes: %w", err)
	}

	scopes := make([]policy.Scope, 0, len(grant.Scopes))
	for _, scope := range grant.Scopes {
		scopes = append(scopes, policy.Scope(scope))
	}
	return scopes, nil
}
