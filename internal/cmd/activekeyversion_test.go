package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/securefile"
)

// activeVersionTestDir resolves a symlink-free temporary directory, the same
// requirement every filesystem-touching test in this package already has.
func activeVersionTestDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return resolved
}

// TestResolveActiveKeyVersionDefaultsWhenTheMarkerIsAbsent is the pre-rotation
// shape: a deployment that has never rotated, or a fresh one, has no marker at
// all, and must resolve to version 1, which is what every deployment before this
// feature already used.
func TestResolveActiveKeyVersionDefaultsWhenTheMarkerIsAbsent(t *testing.T) {
	paths := statePaths{root: activeVersionTestDir(t)}
	paths.keys = filepath.Join(paths.root, "keys")

	version, present, err := resolveActiveKeyVersion(paths)
	if err != nil {
		t.Fatalf("resolveActiveKeyVersion with no marker: %v", err)
	}
	if version != defaultActiveKeyVersion {
		t.Errorf("version = %d, want %d", version, defaultActiveKeyVersion)
	}
	if present {
		t.Error("present = true with no marker on disk, want false")
	}
}

// TestWriteThenResolveActiveKeyVersionRoundTrips is the marker's basic contract.
func TestWriteThenResolveActiveKeyVersionRoundTrips(t *testing.T) {
	paths := statePaths{root: activeVersionTestDir(t)}
	paths.keys = filepath.Join(paths.root, "keys")

	if err := writeActiveKeyVersion(paths, 4); err != nil {
		t.Fatalf("writeActiveKeyVersion: %v", err)
	}
	version, present, err := resolveActiveKeyVersion(paths)
	if err != nil {
		t.Fatalf("resolveActiveKeyVersion: %v", err)
	}
	if version != 4 {
		t.Errorf("version = %d, want 4", version)
	}
	if !present {
		t.Error("present = false with a marker on disk, want true")
	}

	// Writing again must replace, not refuse: unlike a key file, the marker is
	// selection metadata that legitimately changes on every rotation.
	if err := writeActiveKeyVersion(paths, 5); err != nil {
		t.Fatalf("writeActiveKeyVersion (second write): %v", err)
	}
	version, _, err = resolveActiveKeyVersion(paths)
	if err != nil {
		t.Fatalf("resolveActiveKeyVersion (after second write): %v", err)
	}
	if version != 5 {
		t.Errorf("version = %d, want 5", version)
	}
}

// TestWriteActiveKeyVersionRefusesANonPositiveVersion documents that the marker
// can never record a version that resolveActiveKeyVersion itself would refuse.
func TestWriteActiveKeyVersionRefusesANonPositiveVersion(t *testing.T) {
	paths := statePaths{root: activeVersionTestDir(t)}
	paths.keys = filepath.Join(paths.root, "keys")

	if err := writeActiveKeyVersion(paths, 0); !errors.Is(err, ErrUnresolvedState) {
		t.Fatalf("writeActiveKeyVersion(0): err = %v, want ErrUnresolvedState", err)
	}
}

// TestResolveActiveKeyVersionRefusesAMalformedMarker fails closed: a marker that
// exists but does not hold a positive integer is a state an operator must fix,
// never silently treated as version 1.
func TestResolveActiveKeyVersionRefusesAMalformedMarker(t *testing.T) {
	paths := statePaths{root: activeVersionTestDir(t)}
	paths.keys = filepath.Join(paths.root, "keys")

	if err := securefile.EnsureDir(paths.keys, keysDirMode); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	if err := os.WriteFile(activeKeyVersionPath(paths), []byte(`not json`), 0o600); err != nil {
		t.Fatalf("write malformed marker: %v", err)
	}

	if _, _, err := resolveActiveKeyVersion(paths); !errors.Is(err, ErrUnresolvedState) {
		t.Fatalf("resolveActiveKeyVersion of a malformed marker: err = %v, want ErrUnresolvedState", err)
	}

	if err := os.WriteFile(activeKeyVersionPath(paths), []byte(`{"version":"0"}`), 0o600); err != nil {
		t.Fatalf("write zero-version marker: %v", err)
	}
	if _, _, err := resolveActiveKeyVersion(paths); !errors.Is(err, ErrUnresolvedState) {
		t.Fatalf("resolveActiveKeyVersion of a zero-version marker: err = %v, want ErrUnresolvedState", err)
	}
}

// TestResolveActiveKeyVersionRefusesAVersionAboveTheEnvelopeMaximum is the
// unbounded-marker defect: a version above what the envelope's uint32 header
// can encode must be refused, not accepted and then used to probe billions of
// retired-key versions.
func TestResolveActiveKeyVersionRefusesAVersionAboveTheEnvelopeMaximum(t *testing.T) {
	paths := statePaths{root: activeVersionTestDir(t)}
	paths.keys = filepath.Join(paths.root, "keys")

	if err := securefile.EnsureDir(paths.keys, keysDirMode); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	tooLarge := fmt.Sprintf(`{"version":"%d"}`, int64(cryptostore.MaxKeyVersion)+1)
	if err := os.WriteFile(activeKeyVersionPath(paths), []byte(tooLarge), 0o600); err != nil {
		t.Fatalf("write oversized marker: %v", err)
	}

	if _, _, err := resolveActiveKeyVersion(paths); !errors.Is(err, ErrUnresolvedState) {
		t.Fatalf("resolveActiveKeyVersion of a marker above the maximum: err = %v, want ErrUnresolvedState", err)
	}
}

// TestResolveActiveKeyVersionRefusesAnImplausiblyLargeVersion is the
// unbounded-enumeration defect: a marker value that is well under the
// envelope's uint32 maximum but far beyond anything a manual, one-version-
// at-a-time rotation could plausibly reach must still be refused, because
// loadKeyRing feeds this value straight to loadRetiredKeys as the upper bound
// of a linear probe — one filesystem lookup per version below it.
func TestResolveActiveKeyVersionRefusesAnImplausiblyLargeVersion(t *testing.T) {
	paths := statePaths{root: activeVersionTestDir(t)}
	paths.keys = filepath.Join(paths.root, "keys")

	if err := securefile.EnsureDir(paths.keys, keysDirMode); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	tooLarge := fmt.Sprintf(`{"version":"%d"}`, maxPlausibleKeyVersion+1)
	if err := os.WriteFile(activeKeyVersionPath(paths), []byte(tooLarge), 0o600); err != nil {
		t.Fatalf("write implausibly large marker: %v", err)
	}

	if _, _, err := resolveActiveKeyVersion(paths); !errors.Is(err, ErrUnresolvedState) {
		t.Fatalf("resolveActiveKeyVersion of an implausibly large marker: err = %v, want ErrUnresolvedState", err)
	}
}

// TestLoadKeyRingBootstrapsVersion1WhenNoMarkerExists covers the sole
// legitimate creation path: a deployment that has never served and never
// rotated has no marker at all, and loadKeyRing must create version 1 for it,
// exactly like every deployment before this marker existed.
func TestLoadKeyRingBootstrapsVersion1WhenNoMarkerExists(t *testing.T) {
	paths := statePaths{root: activeVersionTestDir(t)}
	paths.keys = filepath.Join(paths.root, "keys")

	active, retired, err := loadKeyRing(paths)
	if err != nil {
		t.Fatalf("loadKeyRing with no marker: %v", err)
	}
	if len(retired) != 0 {
		t.Fatalf("retired keys = %d, want 0 on first bootstrap", len(retired))
	}
	if _, err := cryptostore.LoadKey(paths.keys, defaultActiveKeyVersion); err != nil {
		t.Fatalf("the bootstrapped key was not actually persisted: %v", err)
	}
	if _, err := cryptostore.Encrypt(active, "probe", "probe", []byte("x")); err != nil {
		t.Fatalf("probe encrypt with the bootstrapped active key: %v", err)
	}
}

// TestLoadKeyRingLoadsAnExistingMarkedKeyAndItsRetiredKeys covers the ordinary
// post-rotation path: the marker names a version whose key already exists —
// because a rotation actually activated it — and every lower version still
// present on disk is loaded as retired, in order, skipping a gap.
func TestLoadKeyRingLoadsAnExistingMarkedKeyAndItsRetiredKeys(t *testing.T) {
	paths := statePaths{root: activeVersionTestDir(t)}
	paths.keys = filepath.Join(paths.root, "keys")

	// Seed v1, v3 and v4, deliberately leaving v2 absent, then activate v4.
	v1, err := cryptostore.LoadOrCreateKey(paths.keys, 1)
	if err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	if _, err := cryptostore.LoadOrCreateKey(paths.keys, 3); err != nil {
		t.Fatalf("seed v3: %v", err)
	}
	if _, err := cryptostore.LoadOrCreateKey(paths.keys, 4); err != nil {
		t.Fatalf("seed v4: %v", err)
	}
	if err := writeActiveKeyVersion(paths, 4); err != nil {
		t.Fatalf("writeActiveKeyVersion: %v", err)
	}

	active, retired, err := loadKeyRing(paths)
	if err != nil {
		t.Fatalf("loadKeyRing: %v", err)
	}
	if len(retired) != 2 {
		t.Fatalf("retired keys = %d, want 2 (v1 and v3, v2 absent)", len(retired))
	}

	// The active key must actually be usable at version 4, and distinct from
	// every retired key.
	probe, err := cryptostore.Encrypt(active, "probe", "probe", []byte("x"))
	if err != nil {
		t.Fatalf("probe encrypt with the active key: %v", err)
	}
	if _, err := cryptostore.Decrypt(v1, "probe", "probe", probe); err == nil {
		t.Fatal("the active key at v4 decrypted with the v1 key, want them to differ")
	}
}

// TestLoadKeyRingFailsClosedWhenTheMarkedActiveKeyIsMissing is the lost-key
// defect: once a marker exists, the version it names is reachable only
// because a rotation actually activated it, so an absent key file at that
// version means the key was LOST — for example an operator retiring the wrong
// file — never that a fresh one should be minted in its place. Fabricating a
// replacement there would silently strand every record still sealed under the
// real key, and the real key could no longer be reinstalled once the
// fabricated file exists on disk.
func TestLoadKeyRingFailsClosedWhenTheMarkedActiveKeyIsMissing(t *testing.T) {
	paths := statePaths{root: activeVersionTestDir(t)}
	paths.keys = filepath.Join(paths.root, "keys")

	// The marker names version 2, as a completed rotation would leave it, but
	// key-v2.json was never seeded — modeling an operator who deleted the
	// wrong file.
	if err := writeActiveKeyVersion(paths, 2); err != nil {
		t.Fatalf("writeActiveKeyVersion: %v", err)
	}

	if _, _, err := loadKeyRing(paths); !errors.Is(err, cryptostore.ErrKeyNotFound) {
		t.Fatalf("loadKeyRing with the marked key missing: err = %v, want it to wrap cryptostore.ErrKeyNotFound", err)
	}

	// The refusal must not have fabricated a replacement key file.
	if _, err := os.Stat(filepath.Join(paths.keys, "key-v2.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key-v2.json exists after the refusal, want it to remain absent: stat err = %v", err)
	}
}

// TestLoadKeyRingReportsAnUnreadableRetiredKey documents that a retired key file
// which exists but is unusable is a failure to report, not a version to silently
// skip: skipping is reserved for a version that was never created at all.
func TestLoadKeyRingReportsAnUnreadableRetiredKey(t *testing.T) {
	paths := statePaths{root: activeVersionTestDir(t)}
	paths.keys = filepath.Join(paths.root, "keys")

	if err := securefile.EnsureDir(paths.keys, keysDirMode); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.keys, "key-v1.json"), []byte(`not a key document`), 0o600); err != nil {
		t.Fatalf("write malformed key file: %v", err)
	}
	if err := writeActiveKeyVersion(paths, 2); err != nil {
		t.Fatalf("writeActiveKeyVersion: %v", err)
	}

	if _, _, err := loadKeyRing(paths); err == nil {
		t.Fatal("loadKeyRing with an unreadable retired key succeeded, want an error")
	}
}
