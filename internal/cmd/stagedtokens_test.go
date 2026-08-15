package cmd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
)

// recordingStore is the delegate behind the staging wrapper. It records what
// reached it, which is how a test proves that a login in flight reached nothing.
type recordingStore struct {
	saved   map[string]auth.TokenSet
	deleted []string
	loads   int
}

func newRecordingStore() *recordingStore {
	return &recordingStore{saved: make(map[string]auth.TokenSet)}
}

func (r *recordingStore) Load(_ context.Context, principal string) (auth.TokenSet, int64, error) {
	r.loads++
	set, ok := r.saved[principal]
	if !ok {
		return auth.TokenSet{}, 0, auth.ErrNoTokens
	}
	return set, 1, nil
}

func (r *recordingStore) Save(
	_ context.Context, principal string, set auth.TokenSet, _ int64,
) (int64, error) {
	r.saved[principal] = set
	return 1, nil
}

func (r *recordingStore) Delete(_ context.Context, principal string) error {
	r.deleted = append(r.deleted, principal)
	return nil
}

func stagedTestSet() auth.TokenSet {
	return auth.NewTokenSet("di-token-staged", "di-refresh-staged", "DI_CLIENT_TEST",
		time.Date(2026, 8, 15, 13, 0, 0, 0, time.UTC))
}

// A login in flight must not reach the database at all: that is the whole reason
// the staging area exists, because the row it would need does not exist yet.
func TestStagedTokensKeepAnInFlightLoginOutOfTheStore(t *testing.T) {
	delegate := newRecordingStore()
	staging, err := newStagedTokens(delegate, 4, time.Minute, nil)
	if err != nil {
		t.Fatalf("newStagedTokens: %v", err)
	}

	key, err := staging.begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, _, err := staging.Load(t.Context(), key); !errors.Is(err, auth.ErrNoTokens) {
		t.Fatalf("Load of a fresh staging area = %v, want auth.ErrNoTokens", err)
	}
	version, err := staging.Save(t.Context(), key, stagedTestSet(), 0)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if version != 1 {
		t.Errorf("the staged version is %d, want 1", version)
	}
	if len(delegate.saved) != 0 {
		t.Errorf("the delegate received %v for a login that has no principal yet", delegate.saved)
	}

	set, err := staging.take(key)
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if set.Token() != "di-token-staged" {
		t.Error("the staged set is not the one the login wrote")
	}
	if staging.inFlight() != 0 {
		t.Errorf("%d logins are still staged after a commit, want none", staging.inFlight())
	}
	if _, err := staging.take(key); !errors.Is(err, errNoStagedTokens) {
		t.Errorf("a second take = %v, want errNoStagedTokens", err)
	}
}

// The compare-and-set contract holds for a staged entry too, so no code path has to
// know which store it is writing to.
func TestStagedTokensApplyCompareAndSet(t *testing.T) {
	staging, err := newStagedTokens(newRecordingStore(), 4, time.Minute, nil)
	if err != nil {
		t.Fatalf("newStagedTokens: %v", err)
	}
	key, err := staging.begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if _, err := staging.Save(t.Context(), key, stagedTestSet(), 0); err != nil {
		t.Fatalf("the first Save: %v", err)
	}
	if _, err := staging.Save(t.Context(), key, stagedTestSet(), 0); !errors.Is(
		err, auth.ErrVersionConflict) {
		t.Errorf("a stale staged write = %v, want auth.ErrVersionConflict", err)
	}

	if err := staging.Delete(t.Context(), key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := staging.Load(t.Context(), key); !errors.Is(err, auth.ErrNoTokens) {
		t.Errorf("Load after Delete = %v, want auth.ErrNoTokens", err)
	}
	staging.drop(key)
	if staging.inFlight() != 0 {
		t.Errorf("%d logins are still staged after a drop, want none", staging.inFlight())
	}
}

// Every key that is not staged is the delegate's, which is what makes this the one
// store the authenticator and the refresher both use.
func TestStagedTokensDelegateEveryOtherPrincipal(t *testing.T) {
	delegate := newRecordingStore()
	staging, err := newStagedTokens(delegate, 4, time.Minute, nil)
	if err != nil {
		t.Fatalf("newStagedTokens: %v", err)
	}

	if _, err := staging.Save(t.Context(), testLoginPrincipal, stagedTestSet(), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, ok := delegate.saved[testLoginPrincipal]; !ok {
		t.Error("a write for a real principal did not reach the delegate")
	}
	if _, _, err := staging.Load(t.Context(), testLoginPrincipal); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if delegate.loads != 1 {
		t.Errorf("the delegate served %d loads, want 1", delegate.loads)
	}
	if err := staging.Delete(t.Context(), testLoginPrincipal); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(delegate.deleted) != 1 {
		t.Errorf("the delegate saw %d deletes, want 1", len(delegate.deleted))
	}
}

// The staging area is bounded and it expires, so abandoned logins cannot grow the
// process and a full one recovers on its own rather than staying full.
func TestStagedTokensAreBoundedAndExpire(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	staging, err := newStagedTokens(newRecordingStore(), 2, time.Minute,
		func() time.Time { return now })
	if err != nil {
		t.Fatalf("newStagedTokens: %v", err)
	}

	first, err := staging.begin()
	if err != nil {
		t.Fatalf("the first begin: %v", err)
	}
	if _, err := staging.begin(); err != nil {
		t.Fatalf("the second begin: %v", err)
	}
	if _, err := staging.begin(); !errors.Is(err, errStagingFull) {
		t.Errorf("a full staging area returned %v, want errStagingFull", err)
	}

	now = now.Add(2 * time.Minute)
	if _, err := staging.take(first); !errors.Is(err, errNoStagedTokens) {
		t.Errorf("an expired staging area = %v, want errNoStagedTokens", err)
	}
	if _, err := staging.begin(); err != nil {
		t.Errorf("the staging area did not recover after its entries expired: %v", err)
	}
}

// A staging wrapper with no delegate is a wiring defect and must be reported at
// construction rather than dereferenced on the first real principal.
func TestStagedTokensRefuseNoDelegate(t *testing.T) {
	if _, err := newStagedTokens(nil, 4, time.Minute, nil); err == nil {
		t.Fatal("newStagedTokens accepted a nil delegate")
	}
}
