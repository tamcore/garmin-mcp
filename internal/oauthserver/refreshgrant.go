package oauthserver

import (
	"context"
	"fmt"
	"net/http"
)

// refreshGrant rotates a refresh token.
//
// Rotation happens on every use, without exception: the presented token is consumed and
// a new pair is stored inside one storage transaction. That single-use property is what
// makes reuse detectable at all — a second presentation of a consumed token can only be
// a replay, so the storage layer revokes the whole family in the transaction that
// notices it, and this grant reports invalid_grant.
//
// Nothing about the grant can grow. The principal, the client, the resource and the
// family all come from the stored record; only the scope may move, and only narrower.
func (s *Server) refreshGrant(
	ctx context.Context, client Client, req TokenRequest,
) (TokenResponse, error) {
	presented := SecretFromString(req.RefreshToken)
	if presented.IsZero() {
		return TokenResponse{}, invalidGrant("The refresh token is missing.",
			fmt.Errorf("no refresh token presented: %w", ErrTokenNotFound))
	}
	stored, err := s.store.RefreshToken(ctx, presented.Lookup())
	if err != nil {
		// An unknown token and a revoked family are one answer to the client. Telling
		// them apart would tell an attacker whether a guessed value ever existed.
		return TokenResponse{}, invalidGrant("The refresh token is not usable.",
			storageOrCause(err, ErrTokenNotFound, ErrTokenRevoked))
	}
	if stored.IsExpired(s.now()) {
		return TokenResponse{}, invalidGrant("The refresh token has expired.",
			fmt.Errorf("refresh token outlived its %v lifetime: %w",
				s.refreshTokenTTL, ErrTokenExpired))
	}
	scopes, err := verifyRefreshBindings(client, stored, req)
	if err != nil {
		return TokenResponse{}, err
	}
	return s.issueTokens(ctx, grantedTokens{
		client:     client,
		principal:  stored.Principal,
		scopes:     scopes,
		resource:   stored.Resource,
		family:     stored.Family,
		generation: stored.Generation + 1,
		rotate:     presented.Lookup(),
	})
}

// verifyRefreshBindings checks the bindings and resolves the effective scope.
//
// A client presenting another client's refresh token is invalid_grant rather than
// invalid_client: the client authenticated correctly, it simply has no claim to this
// grant. A changed resource is invalid_target, the RFC 8707 code for a resource the
// request may not have, because a refresh must never move a token to a new audience.
func verifyRefreshBindings(
	client Client, stored RefreshToken, req TokenRequest,
) (ScopeSet, error) {
	if stored.ClientID != client.ID() {
		return ScopeSet{}, invalidGrant("The refresh token was not issued to this client.",
			fmt.Errorf("refresh token belongs to another client: %w", ErrRefreshBinding))
	}
	if req.Resource != "" {
		presented, err := ParseResource(req.Resource)
		if err != nil || !presented.Equal(stored.Resource) {
			return ScopeSet{}, tokenError(ErrorInvalidTarget,
				"A refresh must keep the resource it was issued for.", http.StatusBadRequest,
				fmt.Errorf("resource does not match the refresh token: %w", ErrRefreshBinding))
		}
	}
	scopes, err := narrowScopes(stored.Scopes, req.Scope)
	if err != nil {
		return ScopeSet{}, tokenError(ErrorInvalidScope,
			"A refresh cannot widen scope.", http.StatusBadRequest,
			fmt.Errorf("%w: %v", ErrRefreshBinding, err))
	}
	return scopes, nil
}
