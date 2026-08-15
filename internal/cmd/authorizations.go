package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/loginweb"
	"github.com/tamcore/garmin-mcp/internal/oauthserver"
)

// authorizations drives the authorization server from the browser login pages.
//
// The two packages are deliberately ignorant of each other: internal/loginweb
// declares the narrow interface it needs, internal/oauthserver exposes the
// protocol operations, and this adapter is the only place that knows both. That
// is what keeps a client record, a PKCE challenge, a consent row and an
// authorization code out of the page layer entirely.
//
// The capability crosses this seam as an opaque string, because that is what a
// cookie carries. It is wrapped in an [oauthserver.Secret] on the way in, so it is
// redacted from the moment it is parsed and never reaches a log line or an error.
type authorizations struct {
	server  *oauthserver.Server
	clients *configClients
}

// The assertion this type exists for.
var _ loginweb.Authorizations = (*authorizations)(nil)

// newAuthorizations binds the adapter to the server and the client registry.
func newAuthorizations(
	server *oauthserver.Server, clients *configClients,
) (*authorizations, error) {
	if server == nil || clients == nil {
		return nil, errors.New("cmd: the authorization adapter needs a server and a client registry")
	}
	return &authorizations{server: server, clients: clients}, nil
}

// Begin validates an authorization request and opens a transaction.
//
// A refusal is returned unchanged when it is an [oauthserver.AuthorizeError],
// because that type already carries the decision the page layer needs: whether the
// refusal may be delivered to the client's redirect URI at all. Anything else is
// returned as an opaque error, which the page layer renders locally.
func (a *authorizations) Begin(
	ctx context.Context, query url.Values,
) (loginweb.Authorization, error) {
	request, err := oauthserver.ParseAuthorizeQuery(query)
	if err != nil {
		return loginweb.Authorization{}, err
	}

	authorization, err := a.server.BeginAuthorization(ctx, request)
	if err != nil {
		return loginweb.Authorization{}, err
	}
	return loginweb.Authorization{
		Capability: authorization.Capability.Reveal(),
		Disclosure: disclosureOf(authorization.Disclosure),
		ExpiresAt:  authorization.ExpiresAt,
	}, nil
}

// Disclose reports what the pages must state about a live transaction.
//
// The display name comes from the registry rather than from the transaction,
// because a transaction stores the client identifier and the registry is the
// authority on what that identifier is called. A client removed from configuration
// since the transaction opened therefore discloses no name rather than a stale one.
func (a *authorizations) Disclose(
	ctx context.Context, capability string,
) (loginweb.Disclosure, error) {
	tx, err := a.server.Transaction(ctx, oauthserver.SecretFromString(capability))
	if err != nil {
		return loginweb.Disclosure{}, translateTransactionError(err)
	}

	name := ""
	if client, lookupErr := a.clients.Client(ctx, tx.ClientID); lookupErr == nil {
		name = client.Name()
	}

	redirect := tx.RedirectURI.String()
	host := ""
	if parsed, parseErr := url.Parse(redirect); parseErr == nil {
		host = parsed.Host
	}
	return loginweb.Disclosure{
		ClientID:     tx.ClientID,
		ClientName:   name,
		RedirectURI:  redirect,
		RedirectHost: host,
		Resource:     tx.Resource.String(),
		Scopes:       tx.Scopes.Strings(),
	}, nil
}

// AttachPrincipal records the principal a completed Garmin login resolved to.
//
// The identifier is validated here rather than trusted, so a defect in the page
// layer could not bind a transaction to something that is not a principal.
func (a *authorizations) AttachPrincipal(ctx context.Context, capability, principal string) error {
	resolved, err := identity.NewPrincipal(principal)
	if err != nil {
		return fmt.Errorf("binding the transaction to a principal: %w", err)
	}
	if _, err := a.server.AttachPrincipal(
		ctx, oauthserver.SecretFromString(capability), resolved); err != nil {
		return translateTransactionError(err)
	}
	return nil
}

// Grant records consent and issues the authorization code, which makes the
// transaction terminal.
func (a *authorizations) Grant(ctx context.Context, capability string) (loginweb.Completion, error) {
	completion, err := a.server.GrantConsent(ctx, oauthserver.SecretFromString(capability))
	if err != nil {
		return loginweb.Completion{}, translateTransactionError(err)
	}
	return loginweb.Completion{RedirectTo: completion.RedirectTo}, nil
}

// Deny ends the transaction without persisting anything, discarding whatever the
// login produced.
func (a *authorizations) Deny(ctx context.Context, capability string) (loginweb.Completion, error) {
	completion, err := a.server.DenyAuthorization(ctx, oauthserver.SecretFromString(capability))
	if err != nil {
		return loginweb.Completion{}, translateTransactionError(err)
	}
	return loginweb.Completion{RedirectTo: completion.RedirectTo}, nil
}

// disclosureOf projects the server's disclosure onto the page layer's.
func disclosureOf(in oauthserver.Disclosure) loginweb.Disclosure {
	scopes := make([]string, 0, len(in.Scopes))
	for _, scope := range in.Scopes {
		scopes = append(scopes, string(scope))
	}
	return loginweb.Disclosure{
		ClientID:     in.ClientID,
		ClientName:   in.ClientName,
		RedirectURI:  in.RedirectURI,
		RedirectHost: in.RedirectHost,
		Resource:     in.Resource,
		Scopes:       scopes,
	}
}

// translateTransactionError maps the server's outcomes onto the two the page layer
// branches on.
//
// Everything that is not an expiry collapses to ErrNoTransaction, including a
// storage failure and a transaction in the wrong stage. That is deliberate: the
// page layer answers a generic 404 for it, and a page that distinguished "no such
// transaction" from "not at that step yet" would let a probe map the flow.
func translateTransactionError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, oauthserver.ErrTransactionExpired):
		return fmt.Errorf("%w: %w", loginweb.ErrTransactionExpired, err)
	default:
		return fmt.Errorf("%w: %w", loginweb.ErrNoTransaction, err)
	}
}
