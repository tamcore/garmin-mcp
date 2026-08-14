package oauthserver

import (
	"context"
	"fmt"
	"net/http"
)

// codeGrant redeems an authorization code.
//
// The code is consumed first, atomically, so a replay loses before any binding is even
// examined, and an expired code cannot be retried. Every binding captured when the code
// was issued is then verified against what the client presented: the client id, the
// exact redirect URI, the resource and the PKCE verifier. Only then are tokens minted,
// bound to the principal the login resolved to.
//
// The scopes come from the code, never from the request. A scope parameter is honoured
// only if it narrows what the code already carries, so this exchange cannot widen the
// grant the user consented to.
func (s *Server) codeGrant(
	ctx context.Context, client Client, req TokenRequest,
) (TokenResponse, error) {
	code, err := s.consumeCode(ctx, req.Code)
	if err != nil {
		return TokenResponse{}, err
	}
	if code.IsExpired(s.now()) {
		return TokenResponse{}, invalidGrant("The authorization code has expired.",
			fmt.Errorf("code outlived its %v lifetime: %w", s.codeTTL, ErrCodeExpired))
	}
	if err := verifyCodeBindings(client, code, req); err != nil {
		return TokenResponse{}, err
	}
	scopes, err := narrowScopes(code.Scopes, req.Scope)
	if err != nil {
		return TokenResponse{}, tokenError(ErrorInvalidScope,
			"The requested scope exceeds the authorized scope.", http.StatusBadRequest, err)
	}
	family, err := NewFamilyID()
	if err != nil {
		return TokenResponse{}, serverFailure(err)
	}
	return s.issueTokens(ctx, grantedTokens{
		client:    client,
		principal: code.Principal,
		scopes:    scopes,
		resource:  code.Resource,
		family:    family,
	})
}

// consumeCode turns a presented code into its record, exactly once.
func (s *Server) consumeCode(ctx context.Context, presented string) (AuthorizationCode, error) {
	code := SecretFromString(presented)
	if code.IsZero() {
		return AuthorizationCode{}, invalidGrant("The authorization code is missing.",
			fmt.Errorf("no code presented: %w", ErrCodeNotFound))
	}
	record, err := s.store.ConsumeCode(ctx, code.Lookup())
	if err != nil {
		return AuthorizationCode{}, invalidGrant("The authorization code is not usable.",
			storageOrCause(err, ErrCodeNotFound, ErrCodeAlreadyUsed))
	}
	return record, nil
}

// verifyCodeBindings checks every binding the code carries.
//
// The redirect URI is compared byte-exactly against the one the code was issued
// against, which is what stops a code stolen from one client's callback being redeemed
// against another target. The resource is optional in the request under RFC 8707, but
// when present it must be the one the code was minted for: silently minting for a
// different audience is the token-confusion failure resource indicators exist to
// prevent.
func verifyCodeBindings(client Client, code AuthorizationCode, req TokenRequest) error {
	if code.ClientID != client.ID() {
		return invalidGrant("The authorization code was not issued to this client.",
			fmt.Errorf("code belongs to another client: %w", ErrCodeBinding))
	}
	presentedRedirect, err := ParseRedirectURI(req.RedirectURI)
	if err != nil || !presentedRedirect.Equal(code.RedirectURI) {
		return invalidGrant("The redirect URI does not match the authorization request.",
			fmt.Errorf("redirect URI does not match the code: %w", ErrCodeBinding))
	}
	if req.Resource != "" {
		presentedResource, err := ParseResource(req.Resource)
		if err != nil || !presentedResource.Equal(code.Resource) {
			return tokenError(ErrorInvalidTarget,
				"The resource does not match the authorization request.", http.StatusBadRequest,
				fmt.Errorf("resource does not match the code: %w", ErrCodeBinding))
		}
	}
	if err := code.Challenge.Verify(req.CodeVerifier); err != nil {
		return invalidGrant("The PKCE code verifier is missing or does not match.", err)
	}
	return nil
}

// narrowScopes resolves the effective scope of an exchange.
//
// An absent scope parameter means "what was authorized". A present one must be a subset
// of it: that is the only safe direction, because the wider set is the one the user
// actually saw and agreed to.
func narrowScopes(authorized ScopeSet, requested string) (ScopeSet, error) {
	if requested == "" {
		return authorized, nil
	}
	scopes, err := ParseScopeSet(requested)
	if err != nil {
		return ScopeSet{}, err
	}
	if scopes.IsEmpty() {
		return authorized, nil
	}
	if !scopes.IsSubsetOf(authorized) {
		return ScopeSet{}, fmt.Errorf("the request widens the authorized scope: %w", ErrInvalidScope)
	}
	return scopes, nil
}
