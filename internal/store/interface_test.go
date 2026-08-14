package store

import (
	"context"
	"errors"
	"testing"
)

// tokenStore is the consumer-side interface, copied from what internal/garmin/auth
// requires of a token store. It lives in a test file on purpose: an interface
// belongs to its consumer, and this assertion exists so a signature drift in
// FileStore fails here rather than in the auth package.
//
// Field mapping between this package's TokenSet and the auth package's
// equivalent:
//
//	store.TokenSet.Token()        <-> di_token
//	store.TokenSet.RefreshToken() <-> di_refresh_token
//	store.TokenSet.ClientID()     <-> di_client_id
//	store.TokenSet.ExpiresAt()    <-> the unverified exp claim, scheduling only
//
// The auth package holds those as plain struct fields. Because a TokenSet keeps
// its material behind accessors, the adapter is one call per field in each
// direction: NewTokenSet(t.Token, t.RefreshToken, t.ClientID, t.ExpiresAt) on the
// way in, and the four accessors on the way out.
type tokenStore interface {
	Load(ctx context.Context, principal string) (TokenSet, int64, error)
	Save(ctx context.Context, principal string, set TokenSet, expectedVersion int64) (int64, error)
	Delete(ctx context.Context, principal string) error
}

// The compile-time assertion is the real test: a signature drift fails the build
// of this package rather than the auth package's.
var _ tokenStore = (*FileStore)(nil)

func TestFileStoreSatisfiesTheConsumerInterface(t *testing.T) {
	concrete, _ := newTestStore(t)
	var iface tokenStore = concrete
	ctx := context.Background()

	// Exercise all three methods through the interface, so the assertion covers
	// behavior and not only the method set.
	if _, _, err := iface.Load(ctx, testPrincipal); !errors.Is(err, ErrNoTokens) {
		t.Fatalf("Load through the interface: err = %v, want ErrNoTokens", err)
	}
	version, err := iface.Save(ctx, testPrincipal, newTestTokens(), 0)
	if err != nil {
		t.Fatalf("Save through the interface: %v", err)
	}
	set, loaded, err := iface.Load(ctx, testPrincipal)
	if err != nil {
		t.Fatalf("Load through the interface: %v", err)
	}
	if loaded != version || set.Token() != testToken {
		t.Fatalf("Load returned version %d token presence %v", loaded, set.Token() != "")
	}
	if err := iface.Delete(ctx, testPrincipal); err != nil {
		t.Fatalf("Delete through the interface: %v", err)
	}
}
