package store

import (
	"context"
	"time"
)

// Reads that return the record and leave the judgement to the caller.
//
// LookupAccessToken judges everything itself, which is what a resource server wants.
// The two reads here are for the other caller: an authorization server whose own
// contract says the record comes back and the decision is its own.
//
// They still refuse what a caller may not judge for itself. An unknown digest reports
// ErrTokenNotFound and a revoked token — or one whose family is revoked, or whose
// consent has been withdrawn — reports ErrTokenRevoked, because a revoked token is
// not a record with a decision left in it.
//
// What they do not refuse is a consumed refresh token. Reuse detection has to live in
// the rotation call, where the family dies in the same transaction that detects the
// replay; a read that refused early would report the replay and leave the family
// alive. Expiry is likewise reported rather than enforced: the record carries IssuedAt
// and ExpiresAt, so the caller has everything it needs to refuse.

// A TokenRecord is one stored token row as it is.
type TokenRecord struct {
	PrincipalID string
	ClientID    string
	FamilyID    string

	// Kind is "access" or "refresh".
	Kind string

	Scopes []string

	// Audience is the resource the token is bound to. It may be empty.
	Audience string

	// Resource is the resource recorded on the family. It equals Audience for every
	// token this store writes, and is read from the family row rather than from the
	// token row so a caller can see the key a revocation is addressed by.
	Resource string

	// Generation is how many rotations deep in its family the token is.
	Generation uint64

	IssuedAt  time.Time
	ExpiresAt time.Time

	// Consumed reports whether a refresh token has already been rotated away, and
	// ConsumedAt when. An access token is never consumed.
	Consumed   bool
	ConsumedAt time.Time
}

// IsExpired reports whether the record is past its expiry at now. It is the judgement
// the reads in this file deliberately do not make.
func (r TokenRecord) IsExpired(now time.Time) bool { return !now.Before(r.ExpiresAt) }

// ReadAccessToken returns the stored record for a presented access token.
//
// It reports ErrTokenNotFound for unknown material and ErrTokenRevoked for a revoked
// token, a revoked family or a withdrawn consent. It does not judge expiry: the record
// is returned and the caller decides. Use LookupAccessToken for the resource-server
// read that refuses an expired token itself.
func (s *SQLiteStore) ReadAccessToken(ctx context.Context, token Secret) (TokenRecord, error) {
	return s.readTokenRecord(ctx, purposeAccessToken, tokenKindAccess, token)
}

// ReadRefreshToken returns the stored record for a presented refresh token.
//
// It reports ErrTokenNotFound for unknown material and ErrTokenRevoked for a revoked
// token, a revoked family or a withdrawn consent. A token that has already been
// rotated away is returned rather than refused, with Consumed set: detecting the
// replay is RotateRefreshToken's business, because only there does the family die in
// the same transaction that detects it.
func (s *SQLiteStore) ReadRefreshToken(ctx context.Context, token Secret) (TokenRecord, error) {
	return s.readTokenRecord(ctx, purposeRefreshToken, tokenKindRefresh, token)
}

// readTokenRecord is the shared body of both reads.
func (s *SQLiteStore) readTokenRecord(ctx context.Context, purpose, kind string, token Secret,
) (TokenRecord, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return TokenRecord{}, err
	}
	hash, err := s.keys.requireLookup(purpose, token)
	if err != nil {
		return TokenRecord{}, err
	}
	stored, err := readToken(ctx, s.db, hash, kind)
	if err != nil {
		return TokenRecord{}, err
	}
	if err := checkNotRevoked(stored); err != nil {
		return TokenRecord{}, err
	}
	return stored.record(kind), nil
}

// record renders a stored token row as a TokenRecord.
func (t storedToken) record(kind string) TokenRecord {
	return TokenRecord{
		PrincipalID: t.principalID,
		ClientID:    t.clientID,
		FamilyID:    t.familyID,
		Kind:        kind,
		Scopes:      decodeScopes(t.scopes),
		Audience:    t.audience,
		Resource:    t.resource,
		Generation:  t.generation,
		IssuedAt:    t.issuedAt,
		ExpiresAt:   t.expiresAt,
		Consumed:    t.consumed,
		ConsumedAt:  t.consumedAt,
	}
}
