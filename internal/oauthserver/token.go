package oauthserver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tamcore/garmin-mcp/internal/identity"
)

// The grants this server implements, and the only token type it issues.
//
// There is no "password" grant, no "client_credentials" grant and no token exchange.
// A grant that is not one of these two is refused by name rather than ignored.
const (
	// GrantAuthorizationCode is RFC 6749 §4.1 with mandatory PKCE S256.
	GrantAuthorizationCode = "authorization_code"
	// GrantRefreshToken is RFC 6749 §6 with rotation on every use.
	GrantRefreshToken = "refresh_token"
	// TokenTypeBearer is the RFC 6750 token type.
	TokenTypeBearer = "Bearer"
)

// tokenParams are the form parameters the token endpoint reads. As at the
// authorization endpoint, a repeated parameter is refused rather than resolved.
var tokenParams = []string{
	paramGrantType, paramClientID, paramClientSecret, paramCode, paramRedirectURI,
	paramCodeVerifier, paramRefreshToken, paramScope, paramResource,
}

// A TokenRequest is the unvalidated token request.
//
// ClientSecret is a [Secret] rather than a string, so a presented client secret is
// redacted from the moment it is parsed, whichever transport it arrived on.
type TokenRequest struct {
	GrantType    string
	ClientID     string
	ClientSecret Secret
	Code         string
	RedirectURI  string
	CodeVerifier string
	RefreshToken string
	Scope        string
	Resource     string
}

// A TokenResponse is a successful token response.
//
// Both tokens are [Secret] values, so nothing logs them by accident. The JSON encoding
// that reveals them lives in tokenhttp.go and is the only place permitted to.
type TokenResponse struct {
	AccessToken  Secret
	RefreshToken Secret
	TokenType    string
	ExpiresIn    int
	Scopes       ScopeSet
	Resource     Resource
}

// A TokenError is a failure at the token endpoint.
//
// Unlike an [AuthorizeError] it never redirects: the token endpoint answers the client
// directly, so the only decision left is the status code. RFC 6749 §5.2 maps a
// client-authentication failure to 401 and every other client error to 400; a failure
// of this server is 500.
type TokenError struct {
	code        string
	description string
	status      int
	cause       error
}

func tokenError(code, description string, status int, cause error) *TokenError {
	return &TokenError{code: code, description: description, status: status, cause: cause}
}

// descRefreshNoLongerValid is the one description a refresh-token replay is ever
// told, whether the reuse is caught by the in-transaction RotateRefreshToken path
// or by refreshGrant's own pre-check for a presented token that outlived its own
// expiry before it could be replayed. The two paths must never diverge in what the
// client is told: that would let a client distinguish "consumed and expired" from
// "consumed and still live", which is more than invalid_grant ever promises.
const descRefreshNoLongerValid = "The refresh token is no longer valid."

func invalidGrant(description string, cause error) *TokenError {
	return tokenError(ErrorInvalidGrant, description, http.StatusBadRequest, cause)
}

func invalidClient(description string, cause error) *TokenError {
	return tokenError(ErrorInvalidClient, description, http.StatusUnauthorized, cause)
}

func serverFailure(cause error) *TokenError {
	return tokenError(ErrorServerError, "The request could not be completed.",
		http.StatusInternalServerError, cause)
}

// Code returns the OAuth error code.
func (e *TokenError) Code() string { return e.code }

// Description returns the sanitized description, safe to put in a response body.
func (e *TokenError) Description() string { return e.description }

// Status returns the HTTP status for the response.
func (e *TokenError) Status() int { return e.status }

// Error implements error, naming the code and the cause but never a credential.
func (e *TokenError) Error() string {
	if e.cause == nil {
		return fmt.Sprintf("oauthserver: token endpoint: %s", e.code)
	}
	return fmt.Sprintf("oauthserver: token endpoint: %s: %v", e.code, e.cause)
}

// Unwrap exposes the specific cause for errors.Is.
func (e *TokenError) Unwrap() error { return e.cause }

// ParseTokenForm reads a token request from a form body and the Authorization header.
//
// It refuses a repeated parameter, and it refuses conflicting client credentials:
// credentials in both the header and the body, or a header client id that disagrees
// with the body's. RFC 6749 §2.3.1 allows exactly one authentication method per
// request, and accepting two means choosing which one to believe.
func ParseTokenForm(form url.Values, header http.Header) (TokenRequest, error) {
	for _, name := range tokenParams {
		if len(form[name]) > 1 {
			return TokenRequest{}, fmt.Errorf("the %q parameter appears %d times: %w",
				name, len(form[name]), ErrDuplicateParameter)
		}
	}
	req := TokenRequest{
		GrantType:    form.Get(paramGrantType),
		ClientID:     form.Get(paramClientID),
		ClientSecret: SecretFromString(form.Get(paramClientSecret)),
		Code:         form.Get(paramCode),
		RedirectURI:  form.Get(paramRedirectURI),
		CodeVerifier: form.Get(paramCodeVerifier),
		RefreshToken: form.Get(paramRefreshToken),
		Scope:        form.Get(paramScope),
		Resource:     form.Get(paramResource),
	}
	return applyBasicAuth(req, header.Get("Authorization"))
}

// applyBasicAuth folds RFC 6749 §2.3.1 basic client authentication into the request.
func applyBasicAuth(req TokenRequest, authorization string) (TokenRequest, error) {
	if authorization == "" {
		return req, nil
	}
	id, secret, err := parseBasicCredentials(authorization)
	if err != nil {
		return TokenRequest{}, err
	}
	if !req.ClientSecret.IsZero() {
		return TokenRequest{}, fmt.Errorf(
			"client credentials appear in both the header and the body: %w", ErrDuplicateParameter)
	}
	if req.ClientID != "" && req.ClientID != id {
		return TokenRequest{}, fmt.Errorf(
			"the header and the body name different clients: %w", ErrDuplicateParameter)
	}
	req.ClientID = id
	req.ClientSecret = secret
	return req, nil
}

// parseBasicCredentials decodes the header. Both halves are form-urlencoded under RFC
// 6749 §2.3.1, so they are unescaped; a value that does not unescape is refused rather
// than used raw.
func parseBasicCredentials(authorization string) (string, Secret, error) {
	encoded, ok := strings.CutPrefix(authorization, "Basic ")
	if !ok || strings.TrimSpace(encoded) == "" {
		return "", Secret{}, fmt.Errorf(
			"the Authorization header is not basic client authentication: %w", ErrClientAuthFailed)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", Secret{}, fmt.Errorf(
			"the basic credentials are not base64: %w", ErrClientAuthFailed)
	}
	rawID, rawSecret, found := strings.Cut(string(decoded), ":")
	if !found {
		return "", Secret{}, fmt.Errorf(
			"the basic credentials carry no separator: %w", ErrClientAuthFailed)
	}
	id, err := url.QueryUnescape(rawID)
	if err != nil {
		return "", Secret{}, fmt.Errorf(
			"the basic client id is not form-urlencoded: %w", ErrClientAuthFailed)
	}
	secret, err := url.QueryUnescape(rawSecret)
	if err != nil {
		return "", Secret{}, fmt.Errorf(
			"the basic client secret is not form-urlencoded: %w", ErrClientAuthFailed)
	}
	return id, SecretFromString(secret), nil
}

// Token is the token endpoint.
//
// It refuses an unimplemented grant before touching storage, authenticates the client,
// then dispatches. Both grants end in [Server.issueTokens], so there is exactly one
// place in this package where a token comes into existence.
func (s *Server) Token(ctx context.Context, req TokenRequest) (TokenResponse, error) {
	if req.GrantType != GrantAuthorizationCode && req.GrantType != GrantRefreshToken {
		return TokenResponse{}, tokenError(ErrorUnsupportedGrantType,
			"This server implements the authorization code and refresh token grants only.",
			http.StatusBadRequest,
			fmt.Errorf("grant type %q: %w", req.GrantType, ErrUnsupportedGrantType))
	}
	client, err := s.authenticateClient(ctx, req)
	if err != nil {
		return TokenResponse{}, err
	}
	if req.GrantType == GrantAuthorizationCode {
		return s.codeGrant(ctx, client, req)
	}
	return s.refreshGrant(ctx, client, req)
}

// authenticateClient resolves the client and checks its credential. A public client
// authenticates by presenting nothing, which is only safe because PKCE S256 is
// mandatory on the authorization request that produced the code.
func (s *Server) authenticateClient(ctx context.Context, req TokenRequest) (Client, error) {
	if err := validateClientID(req.ClientID); err != nil {
		return Client{}, invalidClient("The client identifier is missing or malformed.", err)
	}
	client, err := s.store.Client(ctx, req.ClientID)
	if err != nil {
		return Client{}, invalidClient("The client is not registered with this server.",
			storageOrCause(err, ErrUnknownClient))
	}
	if err := client.Authenticate(req.ClientSecret); err != nil {
		return Client{}, invalidClient("Client authentication failed.", err)
	}
	return client, nil
}

// grantedTokens is the outcome of a validated grant: everything the new tokens must be
// bound to. Both grants produce one, and only [Server.issueTokens] consumes it.
type grantedTokens struct {
	client     Client
	principal  identity.Principal
	scopes     ScopeSet
	resource   Resource
	family     FamilyID
	generation uint64
	// rotate is the digest of the refresh token being replaced, or the zero Lookup for
	// a brand-new family.
	rotate Lookup
}

// issueTokens mints and persists an access and refresh token pair.
//
// Both values come from crypto/rand with 256 bits of entropy, and only their digests
// are stored. A new family is written with SaveTokenPair; a rotation goes through
// RotateRefreshToken, which consumes the presented token and stores the replacement in
// one transaction, so a reuse can never be observed as a success.
func (s *Server) issueTokens(ctx context.Context, granted grantedTokens) (TokenResponse, error) {
	access, err := NewSecret()
	if err != nil {
		return TokenResponse{}, serverFailure(err)
	}
	refresh, err := NewSecret()
	if err != nil {
		return TokenResponse{}, serverFailure(err)
	}
	now := s.now()
	accessRecord := AccessToken{
		Lookup:    access.Lookup(),
		ClientID:  granted.client.ID(),
		Principal: granted.principal,
		Scopes:    granted.scopes,
		Resource:  granted.resource,
		Family:    granted.family,
		IssuedAt:  now,
		ExpiresAt: now.Add(s.accessTokenTTL),
	}
	refreshRecord := RefreshToken{
		Lookup:     refresh.Lookup(),
		ClientID:   granted.client.ID(),
		Principal:  granted.principal,
		Scopes:     granted.scopes,
		Resource:   granted.resource,
		Family:     granted.family,
		Generation: granted.generation,
		IssuedAt:   now,
		ExpiresAt:  now.Add(s.refreshTokenTTL),
	}
	if err := s.persistTokens(ctx, granted.rotate, accessRecord, refreshRecord); err != nil {
		return TokenResponse{}, err
	}
	return TokenResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		TokenType:    TokenTypeBearer,
		ExpiresIn:    int(s.accessTokenTTL / time.Second),
		Scopes:       granted.scopes,
		Resource:     granted.resource,
	}, nil
}

// persistTokens writes the pair, either as a new family or as a rotation.
func (s *Server) persistTokens(
	ctx context.Context, rotate Lookup, access AccessToken, refresh RefreshToken,
) error {
	if rotate.IsZero() {
		if err := s.store.SaveTokenPair(ctx, access, refresh); err != nil {
			return serverFailure(storageOrCause(err))
		}
		return nil
	}
	err := s.store.RotateRefreshToken(ctx, rotate, access, refresh)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrRefreshTokenReused), errors.Is(err, ErrTokenRevoked),
		errors.Is(err, ErrTokenNotFound):
		return invalidGrant(descRefreshNoLongerValid, err)
	default:
		return serverFailure(storageOrCause(err))
	}
}
