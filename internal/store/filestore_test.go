package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
)

// tempDir returns a temporary directory with every symlink resolved. t.TempDir
// alone is not enough: on macOS it sits under /var, which is a symlink to
// /private/var, and the full-ancestry symlink check correctly refuses that.
func tempDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return resolved
}

func newTestStore(t *testing.T) (*FileStore, string) {
	t.Helper()
	dir := tempDir(t)
	key, err := cryptostore.LoadOrCreateKey(filepath.Join(dir, "keys"), 1)
	if err != nil {
		t.Fatalf("LoadOrCreateKey: %v", err)
	}
	store, err := NewFileStore(Config{Dir: dir, Key: key})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return store, dir
}

func TestLoadReportsErrNoTokensWhenAbsent(t *testing.T) {
	store, _ := newTestStore(t)

	set, version, err := store.Load(context.Background(), testPrincipal)
	if !errors.Is(err, ErrNoTokens) {
		t.Fatalf("Load error = %v, want ErrNoTokens", err)
	}
	if !set.IsZero() {
		t.Fatal("Load must return the zero TokenSet when absent")
	}
	if version != 0 {
		t.Fatalf("Load version = %d, want 0", version)
	}
}

func TestSaveThenLoadRoundTripsEveryField(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	version, err := store.Save(ctx, testPrincipal, newTestTokens(), 0)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if version != 1 {
		t.Fatalf("first Save returned version %d, want 1", version)
	}

	loaded, loadedVersion, err := store.Load(ctx, testPrincipal)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loadedVersion != 1 {
		t.Fatalf("Load version = %d, want 1", loadedVersion)
	}
	if loaded.Token() != testToken || loaded.RefreshToken() != testRefreshToken {
		t.Fatal("Load did not return the saved credentials")
	}
	if loaded.ClientID() != testClientID {
		t.Fatalf("Load client id = %q, want %q", loaded.ClientID(), testClientID)
	}
	if !loaded.ExpiresAt().Equal(testExpiry()) {
		t.Fatalf("Load expiry = %v, want %v", loaded.ExpiresAt(), testExpiry())
	}
}

func TestSaveWithZeroExpectedVersionRefusesAnExistingRecord(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Save(ctx, testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if _, err := store.Save(ctx, testPrincipal, newTestTokens(), 0); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("second Save with expectedVersion 0: err = %v, want ErrVersionConflict", err)
	}
}

func TestSaveRejectsAStaleExpectedVersion(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	first, err := store.Save(ctx, testPrincipal, newTestTokens(), 0)
	if err != nil {
		t.Fatalf("first Save: %v", err)
	}
	second, err := store.Save(ctx, testPrincipal, newTestTokens().WithToken("rotated-1"), first)
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if second != first+1 {
		t.Fatalf("second Save returned version %d, want %d", second, first+1)
	}

	// A writer holding the stale version loses, and the stored record stays intact.
	_, err = store.Save(ctx, testPrincipal, newTestTokens().WithToken("rotated-2"), first)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale Save: err = %v, want ErrVersionConflict", err)
	}
	loaded, version, err := store.Load(ctx, testPrincipal)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if version != second || loaded.Token() != "rotated-1" {
		t.Fatalf("the losing write was applied: version %d token %q", version, loaded.Token())
	}
}

func TestSaveRejectsAnExpectedVersionForAnAbsentRecord(t *testing.T) {
	store, _ := newTestStore(t)

	_, err := store.Save(context.Background(), testPrincipal, newTestTokens(), 7)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("Save with expectedVersion 7 and no record: err = %v, want ErrVersionConflict", err)
	}
}

func TestDeleteIsIdempotentAndAbsenceIsNotAnError(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if err := store.Delete(ctx, testPrincipal); err != nil {
		t.Fatalf("Delete of an absent record: %v", err)
	}
	if _, err := store.Save(ctx, testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Delete(ctx, testPrincipal); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Delete(ctx, testPrincipal); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
	if _, _, err := store.Load(ctx, testPrincipal); !errors.Is(err, ErrNoTokens) {
		t.Fatalf("Load after Delete: err = %v, want ErrNoTokens", err)
	}
	// A fresh record starts at version 1 again, so a stale writer cannot
	// resurrect the deleted one.
	if _, err := store.Save(ctx, testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("Save after Delete: %v", err)
	}
}

func TestPrincipalsAreIsolated(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	other := testOther

	if _, err := store.Save(ctx, testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("Save first principal: %v", err)
	}
	if _, err := store.Save(ctx, other, newTestTokens().WithToken("other-token"), 0); err != nil {
		t.Fatalf("Save second principal: %v", err)
	}

	first, _, err := store.Load(ctx, testPrincipal)
	if err != nil {
		t.Fatalf("Load first: %v", err)
	}
	second, _, err := store.Load(ctx, other)
	if err != nil {
		t.Fatalf("Load second: %v", err)
	}
	if first.Token() == second.Token() {
		t.Fatal("two principals share token material")
	}
}

func TestRecordOnDiskHoldsNoPlaintextToken(t *testing.T) {
	store, dir := newTestStore(t)

	if _, err := store.Save(context.Background(), testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, err := os.ReadFile(store.recordPath(testPrincipal))
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	for _, secret := range []string{testToken, testRefreshToken, testClientID} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("record at rest contains %q: %s", secret, raw)
		}
	}
	// The principal is not written in the clear either: the file name is a digest
	// and the record carries no identifier.
	if strings.Contains(string(raw), testPrincipal) {
		t.Fatalf("record at rest contains the principal: %s", raw)
	}

	var record struct {
		Schema  int    `json:"schema"`
		Version int64  `json:"version"`
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("record is not JSON: %v", err)
	}
	if record.Schema != recordSchema || record.Version != 1 || record.Payload == "" {
		t.Fatalf("unexpected record shape: %+v", record)
	}
	if !strings.HasPrefix(store.recordPath(testPrincipal), dir) {
		t.Fatal("the record must live under the configured directory")
	}
}

func TestRecordSealedForAnotherPrincipalCannotBeReplayed(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	other := testOther

	if _, err := store.Save(ctx, testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Move the record onto the other principal's file name, as a host-level
	// attacker or a botched restore could.
	raw, err := os.ReadFile(store.recordPath(testPrincipal))
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	if err := os.WriteFile(store.recordPath(other), raw, 0o600); err != nil {
		t.Fatalf("plant record: %v", err)
	}

	if _, _, err := store.Load(ctx, other); err == nil {
		t.Fatal("a record sealed for another principal must not load")
	}
}

func TestLoadRejectsATamperedRecord(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Save(ctx, testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path := store.recordPath(testPrincipal)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	tampered := strings.Replace(string(raw), `"payload":"`, `"payload":"A`, 1)
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatalf("write tampered record: %v", err)
	}

	if _, _, err := store.Load(ctx, testPrincipal); err == nil {
		t.Fatal("a tampered record must not load")
	}
}

func TestNewFileStoreRejectsAnUnusableConfiguration(t *testing.T) {
	key, err := cryptostore.GenerateKey(1)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if _, err := NewFileStore(Config{Dir: "", Key: key}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty directory: err = %v, want ErrInvalidConfig", err)
	}
	if _, err := NewFileStore(Config{Dir: tempDir(t)}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("zero key: err = %v, want ErrInvalidConfig", err)
	}
}

func TestAllowsInlineTokensReflectsTheConfiguration(t *testing.T) {
	dir := tempDir(t)
	key, err := cryptostore.LoadOrCreateKey(filepath.Join(dir, "keys"), 1)
	if err != nil {
		t.Fatalf("LoadOrCreateKey: %v", err)
	}

	secure, err := NewFileStore(Config{Dir: dir, Key: key})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if secure.AllowsInlineTokens() {
		t.Fatal("inline token JSON must be off by default")
	}

	override, err := NewFileStore(Config{Dir: dir, Key: key, AllowInsecureInlineTokens: true})
	if err != nil {
		t.Fatalf("NewFileStore with the override: %v", err)
	}
	if !override.AllowsInlineTokens() {
		t.Fatal("the explicit insecure override was not reported")
	}
}

func TestOperationsRejectAnEmptyPrincipal(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, _, err := store.Load(ctx, ""); !errors.Is(err, ErrInvalidPrincipal) {
		t.Fatalf("Load with empty principal: err = %v, want ErrInvalidPrincipal", err)
	}
	if _, err := store.Save(ctx, "", newTestTokens(), 0); !errors.Is(err, ErrInvalidPrincipal) {
		t.Fatalf("Save with empty principal: err = %v, want ErrInvalidPrincipal", err)
	}
	if err := store.Delete(ctx, ""); !errors.Is(err, ErrInvalidPrincipal) {
		t.Fatalf("Delete with empty principal: err = %v, want ErrInvalidPrincipal", err)
	}
}

func TestOperationsHonorContextCancellation(t *testing.T) {
	store, _ := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := store.Load(ctx, testPrincipal); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load with a cancelled context: err = %v, want context.Canceled", err)
	}
	if _, err := store.Save(ctx, testPrincipal, newTestTokens(), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("Save with a cancelled context: err = %v, want context.Canceled", err)
	}
	if err := store.Delete(ctx, testPrincipal); !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete with a cancelled context: err = %v, want context.Canceled", err)
	}
}

func TestSaveLeavesNoTemporaryFileBehind(t *testing.T) {
	store, _ := newTestStore(t)

	if _, err := store.Save(context.Background(), testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(store.recordPath(testPrincipal)))
	if err != nil {
		t.Fatalf("read record dir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("temporary file survived the write: %s", entry.Name())
		}
	}
}
