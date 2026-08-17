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
	// Captured once: the consumed-and-expired pre-check below and the plain
	// expiry check after it must judge the same instant. Two separate s.now()
	// calls here used to let a token that was live at the first read and expired
	// by the second slip onto the plain-expiry path, which never revokes
	// anything, instead of being caught as reuse.
	now := s.now()
	// Reuse must be caught regardless of the presented token's own expiry. The
	// ordinary path relies on RotateRefreshToken noticing a consumed row inside its
	// own transaction, but that path is never reached for a token this server would
	// otherwise refuse for being expired — and a replay of an old, already-consumed
	// token is exactly the signal that a token store leaked, whether or not the
	// clock has since caught up with it. This check runs BEFORE the expiry check
	// below and only for that reason: it must never fire for a live consumed token,
	// which stays on the transactional path so concurrent refreshers are still
	// serialized atomically against each other.
	if stored.Consumed && stored.IsExpired(now) {
		return TokenResponse{}, s.revokeConsumedExpiredFamily(ctx, stored.Family)
	}
	if stored.IsExpired(now) {
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

// revokeConsumedExpiredFamily revokes the family of a refresh token that was
// replayed after both being consumed and outliving its own expiry.
//
// It calls the same store.RevokeFamily the rest of this package uses for every
// other revocation — RevokeConsent's cascade, the destructive-tool revocation path,
// principal unlink — rather than a second implementation of "take a family down",
// tagged with RevokeReasonReplay so the audit trail records the same reason code
// the transactional RotateRefreshToken reuse path does: this is the same event,
// caught on a different path. RevokeFamily is documented idempotent, so a race
// against the in-transaction path noticing the same reuse first is harmless
// either way.
//
// The client never learns anything a plain reuse would not already tell it: the
// description is the same descRefreshNoLongerValid string RotateRefreshToken's
// reuse path reports, and the code is always invalid_grant, regardless of whether
// the revocation itself succeeded. A failure to revoke is wrapped into the cause
// with %w (not just folded into its text) so that errors.Is can still recover it:
// the client's answer must not change — confirming to a caller that a replay's
// revocation failed would be confirming the replay worked — but the failure must
// not become unrecoverable to anything upstream that inspects the error chain.
func (s *Server) revokeConsumedExpiredFamily(ctx context.Context, family FamilyID) error {
	cause := fmt.Errorf(
		"consumed refresh token replayed after outliving its own expiry, family %s revoked: %w",
		family, ErrRefreshTokenReused)
	if revokeErr := s.store.RevokeFamily(ctx, family, RevokeReasonReplay); revokeErr != nil {
		cause = fmt.Errorf("%w (revoking the family also reported: %w)", cause, revokeErr)
	}
	return invalidGrant(descRefreshNoLongerValid, cause)
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
