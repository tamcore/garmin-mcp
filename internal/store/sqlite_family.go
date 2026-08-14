package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Issuing a family from records rather than from lifetimes.
//
// A caller that mints its own token records already knows the family id, the
// generation and the absolute instants before it calls the store. Handing the store
// lifetimes instead would make it recompute the expiry from its own clock, so the
// stored expiry would drift from the record the caller returned to the client by
// however long the call took, and handing the store no family id would make it mint a
// second one — leaving every later revocation addressing an id the database never saw.
// This path takes all three.

// TokenFamilyGrant is the record-shaped input to IssueTokenFamilyRecord.
//
// Unlike TokenGrant, an empty scope list and an empty resource are accepted: a grant
// of no scope and a request that named no resource are both real states, and a store
// that refused them would force a caller to invent a value.
type TokenFamilyGrant struct {
	// FamilyID is the identifier the caller minted. An empty value means the store
	// mints one, which is what a caller with no id of its own wants; any other value
	// is used as given, so the caller's records and the database agree on the key
	// every later revocation is addressed by.
	FamilyID string

	PrincipalID string
	ClientID    string

	// Scopes may be empty.
	Scopes []string

	// Resource is the RFC 8707 resource both tokens are bound to. It may be empty.
	Resource string

	// Generation is how deep in the family this pair sits. The first pair is 0.
	Generation uint64

	AccessToken  Secret
	RefreshToken Secret

	// IssuedAt is the instant the caller stamped on the records. Zero selects the
	// store's clock.
	IssuedAt time.Time

	// AccessExpiresAt and RefreshExpiresAt are absolute and required. Each must be
	// after IssuedAt and within the bounded token lifetime.
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}

// IssueTokenFamilyRecord creates a family and its first access and refresh token from
// caller-minted records, and returns the family id that was used.
//
// It requires an active consent for the principal and client, and the whole thing is
// one transaction, so a family never exists without its tokens. A family id that is
// already in use is refused rather than merged into: two grants sharing a family
// would make one revocation kill both.
func (s *SQLiteStore) IssueTokenFamilyRecord(ctx context.Context, grant TokenFamilyGrant,
) (string, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return "", err
	}
	prepared, err := s.prepareFamilyGrant(grant)
	if err != nil {
		return "", err
	}
	familyID, err := resolveFamilyID(grant.FamilyID)
	if err != nil {
		return "", err
	}

	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveConsent(ctx, tx, grant.PrincipalID, grant.ClientID); err != nil {
			return err
		}
		if err := s.insertFamily(ctx, tx, familyID,
			grant.PrincipalID, grant.ClientID, grant.Resource); err != nil {
			return err
		}
		return s.insertTokenPair(ctx, tx, familyID, prepared)
	})
	if err != nil {
		return "", err
	}
	return familyID, nil
}

// resolveFamilyID keeps a caller-supplied id and mints one only when there is none.
func resolveFamilyID(supplied string) (string, error) {
	if supplied == "" {
		return newPrincipalID()
	}
	if err := checkIdentifier("family id", supplied); err != nil {
		return "", err
	}
	return supplied, nil
}

// prepareFamilyGrant validates a record-shaped grant and hashes its material.
func (s *SQLiteStore) prepareFamilyGrant(grant TokenFamilyGrant) (preparedGrant, error) {
	if err := checkIdentifier("principal id", grant.PrincipalID); err != nil {
		return preparedGrant{}, err
	}
	if err := checkIdentifier("client id", grant.ClientID); err != nil {
		return preparedGrant{}, err
	}
	if err := checkLocator("resource", grant.Resource); err != nil {
		return preparedGrant{}, err
	}
	scopes, err := encodeOptionalScopes(grant.Scopes)
	if err != nil {
		return preparedGrant{}, err
	}

	issuedAt := grant.IssuedAt
	if issuedAt.IsZero() {
		issuedAt = s.now()
	}
	issuedAt = issuedAt.UTC()
	if err := checkTokenWindow(issuedAt, grant.AccessExpiresAt, grant.RefreshExpiresAt); err != nil {
		return preparedGrant{}, err
	}

	hashes, err := s.hashTokenPair(grant.AccessToken, grant.RefreshToken)
	if err != nil {
		return preparedGrant{}, err
	}
	return preparedGrant{
		accessHash:       hashes[0],
		refreshHash:      hashes[1],
		scopes:           scopes,
		audience:         grant.Resource,
		generation:       grant.Generation,
		issuedAt:         issuedAt,
		accessExpiresAt:  grant.AccessExpiresAt.UTC(),
		refreshExpiresAt: grant.RefreshExpiresAt.UTC(),
	}, nil
}

// hashTokenPair computes the two per-kind lookup values, in access-then-refresh order.
func (s *SQLiteStore) hashTokenPair(access, refresh Secret) ([2]string, error) {
	accessHash, err := s.keys.requireLookup(purposeAccessToken, access)
	if err != nil {
		return [2]string{}, err
	}
	refreshHash, err := s.keys.requireLookup(purposeRefreshToken, refresh)
	if err != nil {
		return [2]string{}, err
	}
	return [2]string{accessHash, refreshHash}, nil
}

// checkTokenWindow refuses an expiry that is not after the issue instant, and one so
// far out that the token would be a permanent credential.
func checkTokenWindow(issuedAt, accessExpiresAt, refreshExpiresAt time.Time) error {
	for name, expiresAt := range map[string]time.Time{
		"access": accessExpiresAt, "refresh": refreshExpiresAt,
	} {
		lifetime := expiresAt.Sub(issuedAt)
		if lifetime <= 0 || lifetime > maxTokenLifetime {
			return fmt.Errorf("store: %s token lives %s from %s, outside (0, %s]: %w",
				name, lifetime, issuedAt.Format(timeLayout), maxTokenLifetime, ErrInvalidArgument)
		}
	}
	return nil
}
