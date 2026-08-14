// Package tokenlink joins the Garmin DI token consumer to its persistence.
//
// internal/garmin/auth declares the TokenStore interface it needs, and
// internal/store implements the same method set over encrypted owner-only files.
// The two do not unify on their own: each package owns its own TokenSet type, so
// auth.TokenStore and the FileStore method set are structurally identical but not
// assignable. Neither package may import the other — auth is the consumer and
// must not depend on a storage implementation, and store must not depend on its
// consumer — so the adapter belongs here, in a wiring package both sides can stay
// ignorant of.
//
// The compile-time assertion below is the point of this package: it turns the
// claim "the file store satisfies the auth interface" into a build error when it
// stops being true, instead of a comment that drifts.
package tokenlink

import (
	"context"
	"errors"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// Store adapts a store.FileStore to the auth.TokenStore interface.
type Store struct {
	files *store.FileStore
}

// Assert the adapter satisfies the consumer's interface at build time.
var _ auth.TokenStore = (*Store)(nil)

// New returns a TokenStore backed by files. It reports an error rather than
// panicking on a nil store, because the caller is start-up wiring that must fail
// closed.
func New(files *store.FileStore) (*Store, error) {
	if files == nil {
		return nil, errors.New("tokenlink: nil file store")
	}

	return &Store{files: files}, nil
}

// Load reports the stored token set and its version for principal. It returns an
// error satisfying errors.Is(err, auth.ErrNoTokens) when nothing is stored, so
// the consumer never learns which backend answered.
func (s *Store) Load(ctx context.Context, principal string) (auth.TokenSet, int64, error) {
	set, version, err := s.files.Load(ctx, principal)
	if err != nil {
		return auth.TokenSet{}, 0, translate(err)
	}

	return toAuth(set), version, nil
}

// Save writes set for principal when the stored version still equals
// expectedVersion, and reports the new version. A zero expectedVersion means the
// record must not exist yet. A stale version yields an error satisfying
// errors.Is(err, auth.ErrVersionConflict).
func (s *Store) Save(ctx context.Context, principal string, set auth.TokenSet, expectedVersion int64) (int64, error) {
	version, err := s.files.Save(ctx, principal, toStore(set), expectedVersion)
	if err != nil {
		return 0, translate(err)
	}

	return version, nil
}

// Delete removes the stored token set. Deleting an absent set is not an error.
//
// Deleting local tokens does not revoke anything at Garmin. Unlinking an account
// remotely is a separate operation.
func (s *Store) Delete(ctx context.Context, principal string) error {
	if err := s.files.Delete(ctx, principal); err != nil {
		return translate(err)
	}

	return nil
}

// toAuth converts a storage token set into the consumer's type. Both types keep
// their secrets behind accessors, so the conversion reads through them instead of
// copying a struct.
func toAuth(set store.TokenSet) auth.TokenSet {
	if set.IsZero() {
		return auth.TokenSet{}
	}

	return auth.NewTokenSet(set.Token(), set.RefreshToken(), set.ClientID(), set.ExpiresAt())
}

// toStore converts the consumer's token set into the storage type.
func toStore(set auth.TokenSet) store.TokenSet {
	if set.IsZero() {
		return store.TokenSet{}
	}

	return store.NewTokenSet(set.Token(), set.RefreshToken(), set.ClientID(), set.ExpiresAt())
}

// translate maps storage sentinels onto the consumer's sentinels while keeping
// the original cause reachable, so errors.Is works against either package.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNoTokens):
		return fmt.Errorf("%w: %w", auth.ErrNoTokens, err)
	case errors.Is(err, store.ErrVersionConflict):
		return fmt.Errorf("%w: %w", auth.ErrVersionConflict, err)
	default:
		return err
	}
}
