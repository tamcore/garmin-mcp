package auth

import (
	"context"
	"errors"
)

// Sentinels the store contract is expressed in. They are declared here because
// this package is the consumer of the interface below.
var (
	// ErrNoTokens reports that nothing is stored for a principal.
	ErrNoTokens = errors.New("garmin auth: no stored tokens for principal")
	// ErrVersionConflict reports that a Save lost a compare-and-set race: the
	// stored version moved on, so the caller's set is stale and must not be
	// written. Overwriting anyway would clobber a newer rotated refresh token.
	ErrVersionConflict = errors.New("garmin auth: token version conflict")
)

// TokenStore persists one principal's Garmin DI token set.
//
// The interface lives with its consumer on purpose: this package defines what it
// needs, and the storage layer implements it. Nothing here performs persistence.
type TokenStore interface {
	// Load returns the stored set and its version. It returns an error
	// satisfying errors.Is(err, ErrNoTokens) when nothing is stored.
	Load(ctx context.Context, principal string) (TokenSet, int64, error)
	// Save writes set for principal only if the stored version still equals
	// expectedVersion, and returns the new version. It returns an error
	// satisfying errors.Is(err, ErrVersionConflict) on a mismatch. A zero
	// expectedVersion means "must not exist yet".
	Save(ctx context.Context, principal string, set TokenSet, expectedVersion int64) (int64, error)
	// Delete removes the stored set. Deleting an absent set is not an error.
	Delete(ctx context.Context, principal string) error
}
