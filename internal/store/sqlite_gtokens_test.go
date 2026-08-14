package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/store"
)

func TestGarminTokenRoundTripAndCompareAndSet(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)

	if _, _, err := opened.Load(ctx, principal.ID); !errors.Is(err, store.ErrNoTokens) {
		t.Fatalf("Load before any Save: err = %v, want ErrNoTokens", err)
	}

	version, err := opened.Save(ctx, principal.ID, newSQLTestTokens(), 0)
	if err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if version != 1 {
		t.Errorf("first Save returned version %d, want 1", version)
	}

	loaded, loadedVersion, err := opened.Load(ctx, principal.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loadedVersion != version {
		t.Errorf("Load version = %d, want %d", loadedVersion, version)
	}
	if loaded.Token() != sqlTestToken || loaded.RefreshToken() != sqlTestRefreshToken {
		t.Error("the round-tripped token set does not match what was saved")
	}
	if loaded.ClientID() != sqlTestClientID {
		t.Errorf("ClientID() = %q, want %q", loaded.ClientID(), sqlTestClientID)
	}

	// A rotated refresh token, written against the version that was read.
	rotated := newSQLTestTokens().WithRefreshToken("rotated-refresh-token")
	next, err := opened.Save(ctx, principal.ID, rotated, loadedVersion)
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if next != loadedVersion+1 {
		t.Errorf("second Save returned version %d, want %d", next, loadedVersion+1)
	}
	reloaded, _, err := opened.Load(ctx, principal.ID)
	if err != nil || reloaded.RefreshToken() != "rotated-refresh-token" {
		t.Fatalf("the rotated refresh token was not persisted: err = %v", err)
	}
}

func TestGarminTokenSaveRefusesAStaleVersion(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)

	first, err := opened.Save(ctx, principal.ID, newSQLTestTokens(), 0)
	if err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	// Writing against version 0 again means "the record must not exist yet", which is
	// now false.
	_, err = opened.Save(ctx, principal.ID, newSQLTestTokens(), 0)
	if !errors.Is(err, store.ErrVersionConflict) {
		t.Errorf("Save with expectedVersion 0 over an existing record: err = %v, want ErrVersionConflict", err)
	}

	_, err = opened.Save(ctx, principal.ID, newSQLTestTokens(), first+5)
	if !errors.Is(err, store.ErrVersionConflict) {
		t.Errorf("Save with a version from the future: err = %v, want ErrVersionConflict", err)
	}

	// The stored record must be untouched by either refusal.
	_, version, err := opened.Load(ctx, principal.ID)
	if err != nil || version != first {
		t.Fatalf("the record moved despite two refused writes: version %d err %v", version, err)
	}
}

func TestGarminTokenSaveRefusesBadInput(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)

	_, err := opened.Save(ctx, "", newSQLTestTokens(), 0)
	if !errors.Is(err, store.ErrInvalidPrincipal) {
		t.Errorf("empty principal: err = %v, want ErrInvalidPrincipal", err)
	}
	_, err = opened.Save(ctx, principal.ID, store.TokenSet{}, 0)
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Errorf("zero token set: err = %v, want ErrInvalidArgument", err)
	}
	_, err = opened.Save(ctx, principal.ID, newSQLTestTokens(), -1)
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Errorf("negative expected version: err = %v, want ErrInvalidArgument", err)
	}
}

func TestGarminTokenDeleteIsIdempotentAndIsolatedPerPrincipal(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	first, err := opened.CreatePrincipal(ctx, "first@example.com")
	if err != nil {
		t.Fatalf("CreatePrincipal first: %v", err)
	}
	second, err := opened.CreatePrincipal(ctx, "second@example.com")
	if err != nil {
		t.Fatalf("CreatePrincipal second: %v", err)
	}

	for _, principal := range []store.Principal{first, second} {
		if _, err := opened.Save(ctx, principal.ID, newSQLTestTokens(), 0); err != nil {
			t.Fatalf("Save for %s: %v", principal.ID, err)
		}
	}

	if err := opened.Delete(ctx, first.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// A second delete of an absent record is not an error.
	if err := opened.Delete(ctx, first.ID); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
	if _, _, err := opened.Load(ctx, first.ID); !errors.Is(err, store.ErrNoTokens) {
		t.Errorf("Load after Delete: err = %v, want ErrNoTokens", err)
	}

	// The other principal is untouched: deleting one principal's tokens must never
	// reach another's.
	if _, version, err := opened.Load(ctx, second.ID); err != nil || version != 1 {
		t.Fatalf("the second principal's record was affected: version %d err %v", version, err)
	}
}

// TestGarminTokensStayIsolatedBetweenPrincipals exercises the per-principal AEAD
// binding from the outside: each principal's record decrypts to its own value, and the
// two are never confused. The binding itself is what makes the swap impossible, and it
// has no public surface to attack, so this is the observable half of the property.
func TestGarminTokensStayIsolatedBetweenPrincipals(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	first, err := opened.CreatePrincipal(ctx, "first@example.com")
	if err != nil {
		t.Fatalf("CreatePrincipal first: %v", err)
	}
	second, err := opened.CreatePrincipal(ctx, "second@example.com")
	if err != nil {
		t.Fatalf("CreatePrincipal second: %v", err)
	}

	_, err = opened.Save(ctx, first.ID, newSQLTestTokens().WithToken("first-token"), 0)
	if err != nil {
		t.Fatalf("Save first: %v", err)
	}
	_, err = opened.Save(ctx, second.ID, newSQLTestTokens().WithToken("second-token"), 0)
	if err != nil {
		t.Fatalf("Save second: %v", err)
	}

	firstSet, _, err := opened.Load(ctx, first.ID)
	if err != nil {
		t.Fatalf("Load first: %v", err)
	}
	secondSet, _, err := opened.Load(ctx, second.ID)
	if err != nil {
		t.Fatalf("Load second: %v", err)
	}
	if firstSet.Token() != "first-token" || secondSet.Token() != "second-token" {
		t.Error("two principals' token sets were confused with each other")
	}
}

// TestSQLiteStoreSatisfiesTheSameConsumerInterfaceAsFileStore is the point of keeping
// the signatures identical: a caller wired to the file store can be moved to the
// database without touching its error handling.
func TestSQLiteStoreSatisfiesTheSameConsumerInterfaceAsFileStore(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)

	// The interface is declared here rather than imported: an interface belongs to its
	// consumer, and internal/garmin/auth requires exactly this shape.
	type tokenStore interface {
		Load(ctx context.Context, principal string) (store.TokenSet, int64, error)
		Save(ctx context.Context, principal string, set store.TokenSet, expected int64) (int64, error)
		Delete(ctx context.Context, principal string) error
	}

	var iface tokenStore = opened
	if _, _, err := iface.Load(ctx, principal.ID); !errors.Is(err, store.ErrNoTokens) {
		t.Fatalf("Load through the interface: err = %v, want ErrNoTokens", err)
	}
	version, err := iface.Save(ctx, principal.ID, newSQLTestTokens(), 0)
	if err != nil {
		t.Fatalf("Save through the interface: %v", err)
	}
	set, loaded, err := iface.Load(ctx, principal.ID)
	if err != nil || loaded != version || set.Token() != sqlTestToken {
		t.Fatalf("Load through the interface: version %d err %v", loaded, err)
	}
	if err := iface.Delete(ctx, principal.ID); err != nil {
		t.Fatalf("Delete through the interface: %v", err)
	}
}
