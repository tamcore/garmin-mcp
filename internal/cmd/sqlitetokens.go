package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// sqliteTokens adapts the multi-user SQLite store to the token store the Garmin
// authenticator and refresher declare.
//
// It is the remote counterpart of internal/tokenlink, which joins the same
// consumer to the single-user file store. The adapter lives here rather than
// there because tokenlink is typed to the file store, and because this is the
// composition root: it is the one place that may know both sides.
//
// The adapter adds no locking. The compare-and-set guarantee is the database's,
// and a lock here would be a second, weaker serialization beside it.
type sqliteTokens struct {
	sqlite *store.SQLiteStore
}

// The assertion this type exists for. It stops compiling if either side moves.
var _ auth.TokenStore = (*sqliteTokens)(nil)

// newSQLiteTokens adapts sqlite. A nil store is a wiring defect and is reported
// rather than dereferenced later.
func newSQLiteTokens(sqlite *store.SQLiteStore) (*sqliteTokens, error) {
	if sqlite == nil {
		return nil, errors.New("cmd: no sqlite store to adapt as a token store")
	}
	return &sqliteTokens{sqlite: sqlite}, nil
}

// Load reports the stored set and its version for principal.
func (s *sqliteTokens) Load(ctx context.Context, principal string) (auth.TokenSet, int64, error) {
	set, version, err := s.sqlite.Load(ctx, principal)
	if err != nil {
		return auth.TokenSet{}, 0, translateTokenError(err)
	}
	return authTokenSet(set), version, nil
}

// Save writes set for principal when the stored version still equals
// expectedVersion, and reports the new version.
func (s *sqliteTokens) Save(
	ctx context.Context, principal string, set auth.TokenSet, expectedVersion int64,
) (int64, error) {
	version, err := s.sqlite.Save(ctx, principal, storeTokenSet(set), expectedVersion)
	if err != nil {
		return 0, translateTokenError(err)
	}
	return version, nil
}

// Delete removes the stored set. Deleting an absent set is not an error, and it
// revokes nothing at Garmin.
func (s *sqliteTokens) Delete(ctx context.Context, principal string) error {
	if err := s.sqlite.Delete(ctx, principal); err != nil {
		return translateTokenError(err)
	}
	return nil
}

// authTokenSet converts a storage token set into the consumer's type. Both keep
// their secrets behind accessors, so the conversion reads through them.
func authTokenSet(set store.TokenSet) auth.TokenSet {
	if set.IsZero() {
		return auth.TokenSet{}
	}
	return auth.NewTokenSet(set.Token(), set.RefreshToken(), set.ClientID(), set.ExpiresAt())
}

// storeTokenSet converts the consumer's token set into the storage type.
func storeTokenSet(set auth.TokenSet) store.TokenSet {
	if set.IsZero() {
		return store.TokenSet{}
	}
	return store.NewTokenSet(set.Token(), set.RefreshToken(), set.ClientID(), set.ExpiresAt())
}

// translateTokenError maps the storage sentinels onto the consumer's, keeping the
// original cause reachable so errors.Is matches either package.
func translateTokenError(err error) error {
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
