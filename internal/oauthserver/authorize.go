package oauthserver

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// authorizeParams are the query parameters the authorization endpoint reads. A
// parameter that appears twice is refused rather than resolved, because two parsers
// can disagree about which occurrence wins, and an attacker who can add a second
// occurrence chooses the disagreement.
var authorizeParams = []string{
	paramResponseType, paramClientID, paramRedirectURI, paramScope,
	paramState, paramCodeChallenge, paramCodeChallengeMethod, paramResource,
}

// An AuthorizeRequest is the unvalidated authorization request, exactly as it
// arrived. Every field is a raw string: validating them is
// [Server.BeginAuthorization]'s job, and the order in which it does so is itself a
// security property.
type AuthorizeRequest struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	Scope               string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string
}

// ParseAuthorizeQuery reads an authorization request from a URL query.
//
// It refuses a duplicated security parameter with an error wrapping
// ErrDuplicateParameter. The caller must render that locally: with two client ids or
// two redirect URIs in play, there is no trustworthy redirect target.
func ParseAuthorizeQuery(values url.Values) (AuthorizeRequest, error) {
	for _, name := range authorizeParams {
		if len(values[name]) > 1 {
			return AuthorizeRequest{}, fmt.Errorf(
				"the %q parameter appears %d times: %w",
				name, len(values[name]), ErrDuplicateParameter)
		}
	}
	return AuthorizeRequest{
		ResponseType:        values.Get(paramResponseType),
		ClientID:            values.Get(paramClientID),
		RedirectURI:         values.Get(paramRedirectURI),
		Scope:               values.Get(paramScope),
		State:               values.Get(paramState),
		CodeChallenge:       values.Get(paramCodeChallenge),
		CodeChallengeMethod: values.Get(paramCodeChallengeMethod),
		Resource:            values.Get(paramResource),
	}, nil
}

// A Disclosure is the non-binding statement shown to the user before any credential
// is entered: who is asking, where the answer goes, and for what.
//
// It is data, not markup. internal/loginweb renders it; this package only guarantees
// that every field has been validated, and that none of them is attacker-chosen text
// beyond the client name an operator registered.
type Disclosure struct {
	// ClientID is the registered client identifier.
	ClientID string
	// ClientName is the operator-registered display name.
	ClientName string
	// RedirectURI is the exact registered redirect URI the code will be sent to.
	RedirectURI string
	// RedirectHost is that URI's host, which is the part a user can actually judge.
	RedirectHost string
	// Resource is the audience the token will be minted for.
	Resource string
	// Scopes are the scopes being requested.
	Scopes []Scope
}

// An Authorization is the result of a validated authorization request.
//
// Capability is the one-time secret that addresses the transaction. It belongs in a
// cookie on a clean fixed route, never in a URL, and this package never stores it:
// only its digest is persisted.
type Authorization struct {
	Capability  Secret
	Disclosure  Disclosure
	ExpiresAt   time.Time
	Transaction Transaction
}

// BeginAuthorization validates an authorization request and opens a transaction.
//
// The order of the checks is the security property. The client and the exact
// registered redirect URI are validated first, and any failure up to that point is
// returned as a local error, because the presented redirect URI is not yet known to
// belong to the client. Only afterwards do protocol failures become redirected OAuth
// errors carrying the client's state.
//
// Nothing here authenticates a user. The returned transaction is at [StagePending]:
// the Garmin login and the consent decision happen behind internal/loginweb and come
// back through [Server.AttachPrincipal] and [Server.GrantConsent].
func (s *Server) BeginAuthorization(
	ctx context.Context, req AuthorizeRequest,
) (Authorization, error) {
	client, redirect, err := s.resolveClientAndRedirect(ctx, req)
	if err != nil {
		return Authorization{}, err
	}
	state, err := checkedState(redirect, req.State)
	if err != nil {
		return Authorization{}, err
	}
	grant, err := s.validateGrantRequest(client, redirect, state, req)
	if err != nil {
		return Authorization{}, err
	}
	return s.openTransaction(ctx, client, redirect, state, grant)
}

// resolveClientAndRedirect performs the two checks that must precede any redirect.
func (s *Server) resolveClientAndRedirect(
	ctx context.Context, req AuthorizeRequest,
) (Client, RedirectURI, error) {
	if err := validateClientID(req.ClientID); err != nil {
		return Client{}, RedirectURI{}, localAuthorizeError(ErrorInvalidClient,
			"The client identifier is missing or malformed.", err)
	}
	client, err := s.store.Client(ctx, req.ClientID)
	if err != nil {
		return Client{}, RedirectURI{}, localAuthorizeError(ErrorInvalidClient,
			"The client is not registered with this server.",
			storageOrCause(err, ErrUnknownClient))
	}
	redirect, err := client.MatchRedirectURI(req.RedirectURI)
	if err != nil {
		return Client{}, RedirectURI{}, localAuthorizeError(ErrorInvalidRequest,
			"The redirect URI is missing or is not registered for this client.", err)
	}
	return client, redirect, nil
}

// checkedState validates the client's state. A state that cannot be echoed safely is
// refused, and the refusal carries no state, because there is none to trust.
func checkedState(redirect RedirectURI, raw string) (ClientState, error) {
	state, err := ParseClientState(raw)
	if err != nil {
		return ClientState{}, redirectAuthorizeError(redirect, ClientState{}, ErrorInvalidRequest,
			"The state parameter is unusable.", err)
	}
	return state, nil
}

// grantRequest is the validated core of an authorization request.
type grantRequest struct {
	scopes    ScopeSet
	resource  Resource
	challenge CodeChallenge
}

// validateGrantRequest checks the response type, PKCE, scope and resource. Every
// failure from here on is a redirected OAuth error.
func (s *Server) validateGrantRequest(
	client Client, redirect RedirectURI, state ClientState, req AuthorizeRequest,
) (grantRequest, error) {
	refuse := func(code, description string, cause error) error {
		return redirectAuthorizeError(redirect, state, code, description, cause)
	}
	if req.ResponseType != paramCode {
		return grantRequest{}, refuse(ErrorUnsupportedResponseType,
			"This server implements the authorization code flow only.",
			fmt.Errorf("response type %q: %w", req.ResponseType, ErrUnsupportedResponseType))
	}
	challenge, err := ParseCodeChallenge(req.CodeChallengeMethod, req.CodeChallenge)
	if err != nil {
		return grantRequest{}, refuse(ErrorInvalidRequest,
			"A PKCE S256 code challenge is required.", err)
	}
	scopes, err := s.validateRequestedScopes(client, req.Scope)
	if err != nil {
		return grantRequest{}, refuse(ErrorInvalidScope,
			"The requested scope is not available to this client.", err)
	}
	resource, err := s.validateRequestedResource(client, req.Resource)
	if err != nil {
		return grantRequest{}, refuse(ErrorInvalidTarget,
			"The resource indicator is missing or is not served here.", err)
	}
	return grantRequest{scopes: scopes, resource: resource, challenge: challenge}, nil
}

// validateRequestedScopes narrows the request against both bounds: what the
// deployment advertises and what the client registered. An empty request is refused
// rather than defaulted, so a grant is never wider than the client actually asked
// for.
func (s *Server) validateRequestedScopes(client Client, raw string) (ScopeSet, error) {
	scopes, err := ParseScopeSet(raw)
	if err != nil {
		return ScopeSet{}, err
	}
	if scopes.IsEmpty() {
		return ScopeSet{}, fmt.Errorf("the request names no scope: %w", ErrInvalidScope)
	}
	if !scopes.IsSubsetOf(s.scopesSupported) {
		return ScopeSet{}, fmt.Errorf("scope is not supported by this deployment: %w",
			ErrInvalidScope)
	}
	if !scopes.IsSubsetOf(client.MaxScopes()) {
		return ScopeSet{}, fmt.Errorf("scope exceeds the client registration: %w", ErrInvalidScope)
	}
	return scopes, nil
}

// validateRequestedResource applies RFC 8707 with exact audience validation. The
// indicator is required: an omitted resource would leave the audience implicit, and
// an implicit audience is what makes a token usable at the wrong server.
func (s *Server) validateRequestedResource(client Client, raw string) (Resource, error) {
	resource, err := ParseResource(raw)
	if err != nil {
		return Resource{}, err
	}
	if !resource.Equal(s.resource) {
		return Resource{}, fmt.Errorf("resource is not this deployment's: %w",
			ErrResourceNotAllowed)
	}
	if !client.AllowsResource(resource) {
		return Resource{}, fmt.Errorf("resource is not registered for the client: %w",
			ErrResourceNotAllowed)
	}
	return resource, nil
}

// openTransaction mints the capability and persists the transaction.
func (s *Server) openTransaction(
	ctx context.Context, client Client, redirect RedirectURI, state ClientState, grant grantRequest,
) (Authorization, error) {
	serverError := func(cause error) error {
		return redirectAuthorizeError(redirect, state, ErrorServerError,
			"The authorization request could not be started.", cause)
	}
	capability, err := NewSecret()
	if err != nil {
		return Authorization{}, serverError(err)
	}
	now := s.now()
	tx := Transaction{
		Lookup:      capability.Lookup(),
		ClientID:    client.ID(),
		RedirectURI: redirect,
		Scopes:      grant.scopes,
		Resource:    grant.resource,
		Challenge:   grant.challenge,
		State:       state,
		Stage:       StagePending,
		CreatedAt:   now,
		ExpiresAt:   now.Add(s.transactionTTL),
	}
	if err := s.store.CreateTransaction(ctx, tx); err != nil {
		return Authorization{}, serverError(storageOrCause(err))
	}
	return Authorization{
		Capability:  capability,
		Disclosure:  disclose(client, tx),
		ExpiresAt:   tx.ExpiresAt,
		Transaction: tx,
	}, nil
}

func disclose(client Client, tx Transaction) Disclosure {
	host := ""
	if parsed, err := url.Parse(tx.RedirectURI.String()); err == nil {
		host = parsed.Host
	}
	return Disclosure{
		ClientID:     client.ID(),
		ClientName:   client.Name(),
		RedirectURI:  tx.RedirectURI.String(),
		RedirectHost: host,
		Resource:     tx.Resource.String(),
		Scopes:       tx.Scopes.Slice(),
	}
}

// storageOrCause keeps a storage failure distinguishable from an expected outcome.
//
// An error that already wraps one of the named sentinels is passed through; anything
// else is wrapped in ErrStorage. That lets a caller tell "the client does not exist"
// from "the database is unreachable", while both still fail closed, and it keeps the
// database's own message out of everything a client can see.
func storageOrCause(err error, expected ...error) error {
	for _, sentinel := range expected {
		if errors.Is(err, sentinel) {
			return err
		}
	}
	return fmt.Errorf("%w: %v", ErrStorage, err)
}
