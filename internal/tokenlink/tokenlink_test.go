package tokenlink_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/store"
	"github.com/tamcore/garmin-mcp/internal/tokenlink"
)

const (
	testPrincipal    = "principal-0001"
	testToken        = "di-token-synthetic-0001"
	testRefreshToken = "di-refresh-synthetic-0001"
	testClientID     = "GARMIN_CONNECT_MOBILE_ANDROID_DI_TEST"
)

// newStore builds a real encrypted file store, because the point of this package
// is that the two halves join through the actual storage path, not through a fake.
func newStore(t *testing.T) *tokenlink.Store {
	t.Helper()

	// The store refuses any path with a symlinked component. On macOS t.TempDir()
	// lives under /var, which is a symlink to /private/var, so a caller must pass
	// a resolved path — the same requirement real start-up wiring has.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	key, err := cryptostore.LoadOrCreateKey(filepath.Join(dir, "keys"), 1)
	if err != nil {
		t.Fatalf("LoadOrCreateKey: %v", err)
	}

	files, err := store.NewFileStore(store.Config{Dir: dir, Key: key})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	linked, err := tokenlink.New(files)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return linked
}

func TestSaveThenLoadRoundTripsThroughEncryptedStorage(t *testing.T) {
	linked := newStore(t)
	ctx := context.Background()
	expiry := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	set := auth.NewTokenSet(testToken, testRefreshToken, testClientID, expiry)

	version, err := linked.Save(ctx, testPrincipal, set, 0)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if version == 0 {
		t.Fatal("Save returned version 0, want a positive version")
	}

	got, gotVersion, err := linked.Load(ctx, testPrincipal)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if gotVersion != version {
		t.Errorf("version = %d, want %d", gotVersion, version)
	}
	if got.Token() != testToken {
		t.Error("Token did not survive the round trip")
	}
	if got.RefreshToken() != testRefreshToken {
		t.Error("RefreshToken did not survive the round trip")
	}
	if got.ClientID() != testClientID {
		t.Errorf("ClientID = %q, want %q", got.ClientID(), testClientID)
	}
	if !got.ExpiresAt().Equal(expiry) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt(), expiry)
	}
}

// TestLoadReportsTheConsumerSentinel is the reason translate exists: the consumer
// must recognise "nothing stored" without importing the storage package.
func TestLoadReportsTheConsumerSentinel(t *testing.T) {
	linked := newStore(t)

	_, _, err := linked.Load(context.Background(), "absent-principal")
	if !errors.Is(err, auth.ErrNoTokens) {
		t.Fatalf("error = %v, want errors.Is(err, auth.ErrNoTokens)", err)
	}
	if !errors.Is(err, store.ErrNoTokens) {
		t.Error("the storage cause must stay reachable for diagnostics")
	}
}

func TestSaveWithStaleVersionReportsTheConsumerConflict(t *testing.T) {
	linked := newStore(t)
	ctx := context.Background()
	set := auth.NewTokenSet(testToken, testRefreshToken, testClientID, time.Time{})

	version, err := linked.Save(ctx, testPrincipal, set, 0)
	if err != nil {
		t.Fatalf("first Save: %v", err)
	}

	// Saving with a version that has already been superseded is the rotated-token
	// race the CAS contract exists to lose safely.
	if _, err := linked.Save(ctx, testPrincipal, set, version-1); !errors.Is(err, auth.ErrVersionConflict) {
		t.Fatalf("error = %v, want errors.Is(err, auth.ErrVersionConflict)", err)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	linked := newStore(t)
	ctx := context.Background()

	if err := linked.Delete(ctx, "never-stored"); err != nil {
		t.Fatalf("Delete of an absent set: %v", err)
	}

	set := auth.NewTokenSet(testToken, testRefreshToken, testClientID, time.Time{})
	if _, err := linked.Save(ctx, testPrincipal, set, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := linked.Delete(ctx, testPrincipal); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := linked.Load(ctx, testPrincipal); !errors.Is(err, auth.ErrNoTokens) {
		t.Fatalf("after Delete, Load error = %v, want auth.ErrNoTokens", err)
	}
}

func TestNewRejectsANilStore(t *testing.T) {
	if _, err := tokenlink.New(nil); err == nil {
		t.Fatal("New(nil) returned no error; start-up wiring must fail closed")
	}
}
