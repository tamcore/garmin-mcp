package oauthstore

import (
	"context"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/oauthserver"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// Tokens, families and revocation.
//
// The family identifier is minted by the authorization server and written as given,
// so the records it returned and the rows in the database agree on the key every
// later revocation is addressed by. Nothing here mints one.
//
// Reuse detection is not implemented here and must not be: the store revokes the
// family in the same transaction in which it finds a consumed refresh token, and a
// check in this package would be a second, racing copy of that decision.

// familyRevocationReason is the audit code written when the authorization server
// revokes a family itself, as opposed to the store's own consent and reuse codes.
// The column is part of the audit trail, so it is a constant and never text from a
// request.
const familyRevocationReason = "authorization_revoked"

// reasonFor maps the caller's oauthserver.RevokeReason onto this store's own
// closed reason-code vocabulary. RevokeReasonReplay uses store.ReasonRefreshReuse,
// the exact code the transactional RotateRefreshToken reuse path already writes,
// because refreshGrant's pre-check and that in-transaction path detect the same
// underlying event — a refresh token was replayed — on two different code paths,
// and the audit trail must not distinguish them by reason code.
//
// An unrecognised reason is refused rather than defaulted to
// familyRevocationReason: a future RevokeReason this switch was never updated for,
// or an accidentally zero value, must not silently file a security event under the
// wrong reason. The caller must never let a revocation both proceed AND use an
// invalid reason, so this is checked before RevokeFamily touches the store.
func reasonFor(reason oauthserver.RevokeReason) (string, error) {
	switch reason {
	case oauthserver.RevokeReasonReplay:
		return store.ReasonRefreshReuse, nil
	case oauthserver.RevokeReasonClient:
		return familyRevocationReason, nil
	default:
		return "", fmt.Errorf("oauthstore: revoke token family: unrecognised revoke reason %d: %w",
			reason, oauthserver.ErrStorage)
	}
}

// SaveTokenPair stores the first access and refresh token of a new family.
//
// The store requires an active consent for the principal and client, so the consent
// has to be saved first. It cannot check the exact consent key here, because an
// access token record carries no redirect URI; that check belongs to the
// authorization decision.
func (a *Adapter) SaveTokenPair(ctx context.Context, access oauthserver.AccessToken,
	refresh oauthserver.RefreshToken,
) error {
	const op = "save token pair"
	if err := checkPairAgrees(op, access, refresh); err != nil {
		return err
	}
	accessMaterial, refreshMaterial, err := pairMaterial(op, access.Lookup, refresh.Lookup)
	if err != nil {
		return err
	}
	_, err = a.sqlite.IssueTokenFamilyRecord(ctx, store.TokenFamilyGrant{
		FamilyID:         string(access.Family),
		PrincipalID:      principalIDOf(access.Principal),
		ClientID:         access.ClientID,
		Scopes:           access.Scopes.Strings(),
		Resource:         access.Resource.String(),
		Generation:       refresh.Generation,
		AccessToken:      accessMaterial,
		RefreshToken:     refreshMaterial,
		IssuedAt:         access.IssuedAt,
		AccessExpiresAt:  access.ExpiresAt,
		RefreshExpiresAt: refresh.ExpiresAt,
	})
	return translate(op, err)
}

// AccessToken returns the record for a presented access token digest. It reports
// ErrTokenNotFound for unknown material and ErrTokenRevoked for a revoked token, a
// revoked family or a withdrawn consent. Expiry is the caller's judgement, so an
// expired token still comes back as a record.
func (a *Adapter) AccessToken(ctx context.Context, lookup oauthserver.Lookup,
) (oauthserver.AccessToken, error) {
	const op = "read access token"
	handle, err := material(op, lookup)
	if err != nil {
		return oauthserver.AccessToken{}, err
	}
	stored, err := a.sqlite.ReadAccessToken(ctx, handle)
	if err != nil {
		return oauthserver.AccessToken{}, translate(op, err)
	}
	parts, err := tokenPartsOf(op, stored)
	if err != nil {
		return oauthserver.AccessToken{}, err
	}
	return oauthserver.AccessToken{
		Lookup:    lookup,
		ClientID:  stored.ClientID,
		Principal: parts.principal,
		Scopes:    parts.scopes,
		Resource:  parts.resource,
		Family:    oauthserver.FamilyID(stored.FamilyID),
		IssuedAt:  stored.IssuedAt,
		ExpiresAt: stored.ExpiresAt,
	}, nil
}

// RefreshToken returns the record for a presented refresh token digest.
//
// It deliberately does not judge whether the token has already been rotated away: a
// consumed record comes back, so a replay is detected by RotateRefreshToken, where
// the family dies in the same transaction that detects it.
func (a *Adapter) RefreshToken(ctx context.Context, lookup oauthserver.Lookup,
) (oauthserver.RefreshToken, error) {
	const op = "read refresh token"
	handle, err := material(op, lookup)
	if err != nil {
		return oauthserver.RefreshToken{}, err
	}
	stored, err := a.sqlite.ReadRefreshToken(ctx, handle)
	if err != nil {
		return oauthserver.RefreshToken{}, translate(op, err)
	}
	parts, err := tokenPartsOf(op, stored)
	if err != nil {
		return oauthserver.RefreshToken{}, err
	}
	return oauthserver.RefreshToken{
		Lookup:     lookup,
		ClientID:   stored.ClientID,
		Principal:  parts.principal,
		Scopes:     parts.scopes,
		Resource:   parts.resource,
		Family:     oauthserver.FamilyID(stored.FamilyID),
		Generation: stored.Generation,
		IssuedAt:   stored.IssuedAt,
		ExpiresAt:  stored.ExpiresAt,
		Consumed:   stored.Consumed,
	}, nil
}

// RotateRefreshToken consumes the presented refresh token and stores its
// replacement pair, atomically.
//
// A presented token that is already consumed, or whose family is revoked, takes the
// whole family down inside the same transaction and reports ErrRefreshTokenReused,
// having stored nothing.
func (a *Adapter) RotateRefreshToken(ctx context.Context, presented oauthserver.Lookup,
	access oauthserver.AccessToken, refresh oauthserver.RefreshToken,
) error {
	const op = "rotate refresh token"
	if err := checkPairAgrees(op, access, refresh); err != nil {
		return err
	}
	presentedMaterial, err := material(op, presented)
	if err != nil {
		return err
	}
	accessMaterial, refreshMaterial, err := pairMaterial(op, access.Lookup, refresh.Lookup)
	if err != nil {
		return err
	}
	_, err = a.sqlite.RotateRefreshToken(ctx, store.RefreshRotation{
		Presented:        presentedMaterial,
		NextAccessToken:  accessMaterial,
		NextRefreshToken: refreshMaterial,
		NextGeneration:   refresh.Generation,
		// The narrowed set the server computed and reported to the client, not the
		// consumed token's. Verification reads the persisted row, so inheriting the
		// old scopes here would hand back a token wider than the client was told.
		Scopes:           access.Scopes.Strings(),
		IssuedAt:         access.IssuedAt,
		AccessExpiresAt:  access.ExpiresAt,
		RefreshExpiresAt: refresh.ExpiresAt,
	})
	return translate(op, err)
}

// RevokeFamily revokes every token in the family. Revoking an already revoked
// family is not an error; a family the database has never seen reports
// ErrTokenNotFound rather than succeeding silently on an id nobody recognizes.
func (a *Adapter) RevokeFamily(
	ctx context.Context, family oauthserver.FamilyID, reason oauthserver.RevokeReason,
) error {
	const op = "revoke token family"
	code, err := reasonFor(reason)
	if err != nil {
		return err
	}
	_, err = a.sqlite.RevokeTokenFamily(ctx, string(family), code)
	return translate(op, err)
}

// RevokePrincipal revokes every token family belonging to the principal and revokes
// its consents, in one transaction that re-counts what must be gone before it
// commits. It is idempotent, and it leaves the principal's Garmin linkage alone:
// this is the token half of unlinking, not the unlink.
func (a *Adapter) RevokePrincipal(ctx context.Context, principal identity.Principal) error {
	const op = "revoke principal"
	_, err := a.sqlite.RevokePrincipalTokens(ctx, principalIDOf(principal))
	return translate(op, err)
}

// tokenParts holds the values re-validated on the way out of a token row.
type tokenParts struct {
	principal identity.Principal
	scopes    oauthserver.ScopeSet
	resource  oauthserver.Resource
}

// tokenPartsOf re-validates the columns both token records share.
func tokenPartsOf(op string, stored store.TokenRecord) (tokenParts, error) {
	holder, err := principalOf(op, stored.PrincipalID)
	if err != nil {
		return tokenParts{}, err
	}
	scopes, err := scopesOf(op, stored.Scopes)
	if err != nil {
		return tokenParts{}, err
	}
	resource, err := resourceOf(op, stored.Audience)
	if err != nil {
		return tokenParts{}, err
	}
	return tokenParts{principal: holder, scopes: scopes, resource: resource}, nil
}

// pairMaterial renders both digests as store material.
func pairMaterial(op string, access, refresh oauthserver.Lookup,
) (store.Secret, store.Secret, error) {
	accessMaterial, err := material(op, access)
	if err != nil {
		return store.Secret{}, store.Secret{}, err
	}
	refreshMaterial, err := material(op, refresh)
	if err != nil {
		return store.Secret{}, store.Secret{}, err
	}
	return accessMaterial, refreshMaterial, nil
}

// checkPairAgrees refuses two records that disagree about a value the store writes
// once for both. Without it the access token's answer would silently win and the
// refresh record the caller returned to the client would describe a row that does
// not exist.
func checkPairAgrees(op string, access oauthserver.AccessToken, refresh oauthserver.RefreshToken,
) error {
	switch {
	case access.Family != refresh.Family:
		return inconsistent(op, "token family")
	case access.ClientID != refresh.ClientID:
		return inconsistent(op, "client")
	case principalIDOf(access.Principal) != principalIDOf(refresh.Principal):
		return inconsistent(op, "principal")
	case access.Resource.String() != refresh.Resource.String():
		return inconsistent(op, "resource")
	case !access.Scopes.Equal(refresh.Scopes):
		return inconsistent(op, "scope set")
	}
	return nil
}
