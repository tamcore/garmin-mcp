package cmd_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/cmd"
	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// writeActiveKeyVersionMarker writes the active-key-version marker directly,
// in the exact on-disk shape internal/cmd's own writeActiveKeyVersion
// produces, to simulate a killed run that already activated the target
// version before dying. This package cannot call that unexported helper
// directly (rotatekey_test.go is package cmd_test, driving the command only
// through its public entry points), so the marker is written by hand instead.
func writeActiveKeyVersionMarker(t *testing.T, stateDir string, version int) {
	t.Helper()
	keysDir := filepath.Join(stateDir, "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		t.Fatalf("create keys dir: %v", err)
	}
	content, err := json.Marshal(struct {
		Version json.Number `json:"version"`
	}{Version: json.Number(strconv.Itoa(version))})
	if err != nil {
		t.Fatalf("encode marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(keysDir, "active-key-version.json"), content, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
}

const cmdRotateKey = "rotate-key"

// testRefreshTokenValue is the refresh token every seeded record in this file
// uses, so the string literal is not repeated across every test.
const testRefreshTokenValue = "refresh-token"

// rotateStateDir returns a state directory inside a symlink-free ancestry, the
// same requirement every other filesystem-touching command test in this package
// already has.
func rotateStateDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the temporary directory: %v", err)
	}
	return resolved
}

// TestRotateKeyRequiresTargetVersionFlag documents that the target is never
// inferred: Cobra refuses to run without it.
func TestRotateKeyRequiresTargetVersionFlag(t *testing.T) {
	clearGarminEnv(t)
	stateDir := rotateStateDir(t)

	_, err := runCommand(t, cmdRotateKey, "--state-dir="+stateDir)
	if err == nil {
		t.Fatal("rotate-key without --target-version succeeded, want a refusal")
	}
}

// TestRotateKeyRefusesATargetThatSkipsAVersion is the one-version-at-a-time rule.
func TestRotateKeyRefusesATargetThatSkipsAVersion(t *testing.T) {
	clearGarminEnv(t)
	stateDir := rotateStateDir(t)
	seedFileStoreKeyRing(t, stateDir)

	_, err := runCommand(t, cmdRotateKey, "--state-dir="+stateDir, "--target-version=3")
	if !errors.Is(err, cmd.ErrRotationTargetInvalid) {
		t.Fatalf("rotate-key to a skipped version: err = %v, want ErrRotationTargetInvalid", err)
	}
}

// TestRotateKeyRefusesWhenNoActiveKeyExistsYet: rotating requires something to
// rotate from. A deployment that has never been served has no key-v1.json, and
// rotate-key must refuse rather than silently creating one and calling that a
// rotation.
func TestRotateKeyRefusesWhenNoActiveKeyExistsYet(t *testing.T) {
	clearGarminEnv(t)
	stateDir := rotateStateDir(t)

	_, err := runCommand(t, cmdRotateKey, "--state-dir="+stateDir, "--target-version=2")
	if err == nil {
		t.Fatal("rotate-key with no active key succeeded, want a refusal")
	}
	if !errors.Is(err, cryptostore.ErrKeyNotFound) {
		t.Fatalf("rotate-key with no active key: err = %v, want it to wrap cryptostore.ErrKeyNotFound", err)
	}
}

// TestRotateKeyRefusesTargetVersionOneOnAMarkerLessDeployment is item 8(a) of
// the fix list: a marker-less deployment resolves active=1 with present=false
// (see resolveActiveKeyVersion). --target-version=1 there used to be
// misclassified as a resume (target == active), which then failed deep inside
// loadRotationKeys with the opaque "invalid key version 0" (retiringVersion =
// target-1 = 0), instead of naming the real problem: there is nothing to
// rotate from at version 1 on a deployment that has never rotated.
func TestRotateKeyRefusesTargetVersionOneOnAMarkerLessDeployment(t *testing.T) {
	clearGarminEnv(t)
	stateDir := rotateStateDir(t)
	seedFileStoreKeyRing(t, stateDir)

	_, err := runCommand(t, cmdRotateKey, "--state-dir="+stateDir, "--target-version=1")
	if !errors.Is(err, cmd.ErrRotationTargetInvalid) {
		t.Fatalf("rotate-key --target-version=1 with no marker: err = %v, want ErrRotationTargetInvalid", err)
	}
	if strings.Contains(err.Error(), "invalid key version 0") {
		t.Fatalf("err = %q, want it to name the real problem, not the opaque downstream failure", err.Error())
	}
	// Requiring the phrase, not just the sentinel: ErrRotationTargetInvalid is
	// returned for every rejected target, so asserting only the sentinel passes
	// even if the diagnostic regresses to the opaque downstream one this test
	// exists to prevent.
	if !strings.Contains(err.Error(), "nothing to rotate from") {
		t.Fatalf("err = %q, want it to say there is nothing to rotate from", err.Error())
	}
}

// seedFileStoreKeyRing creates the version-1 key material a local deployment
// would have created on its first serve or auth run.
func seedFileStoreKeyRing(t *testing.T, stateDir string) cryptostore.Key {
	t.Helper()
	key, err := cryptostore.LoadOrCreateKey(filepath.Join(stateDir, "keys"), 1)
	if err != nil {
		t.Fatalf("seed key version 1: %v", err)
	}
	return key
}

// TestRotateKeyResealsTheLocalFileStoreRecord drives the command end to end for
// the stdio deployment shape: no --database-path, so rotate-key must reseal the
// single bound principal's FileStore record.
func TestRotateKeyResealsTheLocalFileStoreRecord(t *testing.T) {
	clearGarminEnv(t)
	stateDir := rotateStateDir(t)
	oldKey := seedFileStoreKeyRing(t, stateDir)

	files, err := store.NewFileStore(store.Config{Dir: filepath.Join(stateDir, "tokens"), Key: oldKey})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	set := store.NewTokenSet("token", testRefreshTokenValue, "client-id", time.Time{})
	if _, err := files.Save(context.Background(), config.DefaultPrincipalID, set, 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	stdout, err := runCommand(t, cmdRotateKey, "--state-dir="+stateDir, "--target-version=2")
	if err != nil {
		t.Fatalf("rotate-key = %v, want success", err)
	}
	if !strings.Contains(stdout, "resealed") {
		t.Errorf("stdout = %q, want it to report the reseal", stdout)
	}

	targetKey, err := cryptostore.LoadKey(filepath.Join(stateDir, "keys"), 2)
	if err != nil {
		t.Fatalf("LoadKey version 2 after rotation: %v", err)
	}
	targetOnly, err := store.NewFileStore(store.Config{Dir: filepath.Join(stateDir, "tokens"), Key: targetKey})
	if err != nil {
		t.Fatalf("NewFileStore with only the target key: %v", err)
	}
	loaded, _, err := targetOnly.Load(context.Background(), config.DefaultPrincipalID)
	if err != nil {
		t.Fatalf("Load with only the target key after rotation: %v", err)
	}
	if loaded.RefreshToken() != testRefreshTokenValue {
		t.Errorf("loaded refresh token = %q, want %q", loaded.RefreshToken(), testRefreshTokenValue)
	}
}

// TestRotateKeyResumesAfterBeingKilledMidRotationFileStore is the
// kill-and-resume property driven through the command entry point, for the
// local FileStore backend: a first run that reached "the marker now names the
// target version" but never got to actually reseal the record (modeling a
// process kill in between) must be resumable by invoking rotate-key again with
// the SAME --target-version, not refused as skipping a version.
func TestRotateKeyResumesAfterBeingKilledMidRotationFileStore(t *testing.T) {
	clearGarminEnv(t)
	stateDir := rotateStateDir(t)
	oldKey := seedFileStoreKeyRing(t, stateDir)

	files, err := store.NewFileStore(store.Config{Dir: filepath.Join(stateDir, "tokens"), Key: oldKey})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	set := store.NewTokenSet("token", testRefreshTokenValue, "client-id", time.Time{})
	if _, err := files.Save(context.Background(), config.DefaultPrincipalID, set, 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	// Model a kill: a first attempt already created the target key and
	// activated the marker, but died before resealing the one stored record,
	// which is still sealed under the retiring key.
	if _, err := cryptostore.LoadOrCreateKey(filepath.Join(stateDir, "keys"), 2); err != nil {
		t.Fatalf("simulate target key already created by the killed run: %v", err)
	}
	writeActiveKeyVersionMarker(t, stateDir, 2)

	// Re-invoking with the SAME target the killed run was given must resume,
	// not be refused as skipping a version.
	stdout, err := runCommand(t, cmdRotateKey, "--state-dir="+stateDir, "--target-version=2")
	if err != nil {
		t.Fatalf("resuming rotate-key with target-version=2 = %v, want success", err)
	}
	if !strings.Contains(stdout, "resealed") {
		t.Errorf("stdout = %q, want it to report the reseal", stdout)
	}

	targetKey, err := cryptostore.LoadKey(filepath.Join(stateDir, "keys"), 2)
	if err != nil {
		t.Fatalf("LoadKey version 2 after resuming: %v", err)
	}
	targetOnly, err := store.NewFileStore(store.Config{Dir: filepath.Join(stateDir, "tokens"), Key: targetKey})
	if err != nil {
		t.Fatalf("NewFileStore with only the target key: %v", err)
	}
	loaded, _, err := targetOnly.Load(context.Background(), config.DefaultPrincipalID)
	if err != nil {
		t.Fatalf("Load with only the target key after resuming: %v", err)
	}
	if loaded.RefreshToken() != testRefreshTokenValue {
		t.Errorf("loaded refresh token = %q, want %q", loaded.RefreshToken(), testRefreshTokenValue)
	}
}

// TestRotateKeyResumesAfterBeingKilledMidRotationSQLite is the same
// kill-and-resume property for the SQLite backend.
func TestRotateKeyResumesAfterBeingKilledMidRotationSQLite(t *testing.T) {
	clearGarminEnv(t)
	stateDir := rotateStateDir(t)
	oldKey := seedFileStoreKeyRing(t, stateDir)
	dbPath := filepath.Join(stateDir, "state.db")

	seed, err := store.OpenSQLite(context.Background(), store.SQLiteConfig{Path: dbPath, Key: oldKey})
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	principal, err := seed.CreatePrincipal(context.Background(), "rotate-key-resume-test@example.com")
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	if _, err := seed.Save(context.Background(), principal.ID,
		store.NewTokenSet("token", testRefreshTokenValue, "client-id", time.Time{}), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	// Model a kill: the target key and the marker are already in place, but
	// the database was never re-sealed.
	if _, err := cryptostore.LoadOrCreateKey(filepath.Join(stateDir, "keys"), 2); err != nil {
		t.Fatalf("simulate target key already created by the killed run: %v", err)
	}
	writeActiveKeyVersionMarker(t, stateDir, 2)

	stdout, err := runCommand(t, cmdRotateKey,
		"--state-dir="+stateDir, "--database-path="+dbPath, "--target-version=2")
	if err != nil {
		t.Fatalf("resuming rotate-key with target-version=2 = %v, want success", err)
	}
	if !strings.Contains(stdout, "garmin token sets resealed: 1") {
		t.Errorf("stdout = %q, want it to report one resealed garmin token set", stdout)
	}

	targetKey, err := cryptostore.LoadKey(filepath.Join(stateDir, "keys"), 2)
	if err != nil {
		t.Fatalf("LoadKey version 2 after resuming: %v", err)
	}
	targetOnly, err := store.OpenSQLite(context.Background(), store.SQLiteConfig{Path: dbPath, Key: targetKey})
	if err != nil {
		t.Fatalf("OpenSQLite with only the target key after resuming: %v", err)
	}
	defer func() { _ = targetOnly.Close() }()
	loaded, _, err := targetOnly.Load(context.Background(), principal.ID)
	if err != nil {
		t.Fatalf("Load with only the target key after resuming: %v", err)
	}
	if loaded.RefreshToken() != testRefreshTokenValue {
		t.Errorf("loaded refresh token = %q, want %q", loaded.RefreshToken(), testRefreshTokenValue)
	}
}

// TestRotateKeyResealsTheSQLiteStore drives the command for the remote deployment
// shape: --database-path set, so rotate-key must reseal every table.
func TestRotateKeyResealsTheSQLiteStore(t *testing.T) {
	clearGarminEnv(t)
	stateDir := rotateStateDir(t)
	oldKey := seedFileStoreKeyRing(t, stateDir)
	dbPath := filepath.Join(stateDir, "state.db")

	seed, err := store.OpenSQLite(context.Background(), store.SQLiteConfig{Path: dbPath, Key: oldKey})
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	principal, err := seed.CreatePrincipal(context.Background(), "rotate-key-test@example.com")
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	if _, err := seed.Save(context.Background(), principal.ID,
		store.NewTokenSet("token", testRefreshTokenValue, "client-id", time.Time{}), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	stdout, err := runCommand(t, cmdRotateKey,
		"--state-dir="+stateDir, "--database-path="+dbPath, "--target-version=2")
	if err != nil {
		t.Fatalf("rotate-key = %v, want success", err)
	}
	if !strings.Contains(stdout, "garmin token sets resealed: 1") {
		t.Errorf("stdout = %q, want it to report one resealed garmin token set", stdout)
	}
	if !strings.Contains(stdout, "no record needed the retiring key as of this scan") {
		t.Errorf("stdout = %q, want it to tell the operator the scan found nothing outstanding", stdout)
	}

	targetKey, err := cryptostore.LoadKey(filepath.Join(stateDir, "keys"), 2)
	if err != nil {
		t.Fatalf("LoadKey version 2 after rotation: %v", err)
	}
	targetOnly, err := store.OpenSQLite(context.Background(), store.SQLiteConfig{Path: dbPath, Key: targetKey})
	if err != nil {
		t.Fatalf("OpenSQLite with only the target key after rotation: %v", err)
	}
	defer func() { _ = targetOnly.Close() }()
	loaded, _, err := targetOnly.Load(context.Background(), principal.ID)
	if err != nil {
		t.Fatalf("Load with only the target key after rotation: %v", err)
	}
	if loaded.RefreshToken() != testRefreshTokenValue {
		t.Errorf("loaded refresh token = %q, want %q", loaded.RefreshToken(), testRefreshTokenValue)
	}
}

// TestRotateKeyResumingRefusesWhenTheTargetKeyIsMissing is the fail-closed
// property that separates a resume from a fresh rotation.
//
// A resumed run's target key must already exist: the interrupted run created it
// and sealed records under it. An absent file therefore means that key was LOST,
// not that one should be minted. Creating a replacement would leave every record
// the first run already re-sealed readable by nothing, permanently, and the
// operator would see a successful-looking rotation while doing it. So a resume
// loads the target key and refuses when it is gone, where a fresh rotation
// creates it.
func TestRotateKeyResumingRefusesWhenTheTargetKeyIsMissing(t *testing.T) {
	clearGarminEnv(t)
	stateDir := rotateStateDir(t)
	oldKey := seedFileStoreKeyRing(t, stateDir)

	files, err := store.NewFileStore(store.Config{Dir: filepath.Join(stateDir, "tokens"), Key: oldKey})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	set := store.NewTokenSet("token", testRefreshTokenValue, "client-id", time.Time{})
	if _, err := files.Save(context.Background(), config.DefaultPrincipalID, set, 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	// A killed run activated the marker, and the target key it created has since
	// been lost — an operator retiring the wrong file is the realistic way there.
	writeActiveKeyVersionMarker(t, stateDir, 2)

	_, err = runCommand(t, cmdRotateKey, "--state-dir="+stateDir, "--target-version=2")
	if err == nil {
		t.Fatal("resuming with the target key absent succeeded, so a replacement key " +
			"was minted at that version: every record the killed run already resealed " +
			"is now unreadable")
	}
	if !errors.Is(err, cryptostore.ErrKeyNotFound) {
		t.Errorf("err = %v, want it to report the missing key (%v)", err, cryptostore.ErrKeyNotFound)
	}

	// And the record must still be readable under the retiring key, so refusing
	// left the deployment recoverable rather than half-rotated.
	stillOld, err := store.NewFileStore(store.Config{Dir: filepath.Join(stateDir, "tokens"), Key: oldKey})
	if err != nil {
		t.Fatalf("NewFileStore with the retiring key: %v", err)
	}
	loaded, _, err := stillOld.Load(context.Background(), config.DefaultPrincipalID)
	if err != nil {
		t.Fatalf("Load with the retiring key after the refusal: %v", err)
	}
	if loaded.RefreshToken() != testRefreshTokenValue {
		t.Errorf("loaded refresh token = %q, want %q", loaded.RefreshToken(), testRefreshTokenValue)
	}
}

// TestRotateKeyResumingReportsCompletionWhenTheRetiringKeyIsAlreadyGoneAndNothingRemains
// is item 7 of the fix list. docs/operations.md documents this exact sequence:
// rotate, confirm every record resealed, retire the OLD key, then re-run
// rotate-key with the SAME --target-version to double-check. Before this fix
// that re-run failed with "opening the active key to rotate from: ... key not
// found", which reads like data loss on the documented happy path. It must
// instead report completion, because nothing is left that needs the retiring
// key at all.
func TestRotateKeyResumingReportsCompletionWhenTheRetiringKeyIsAlreadyGoneAndNothingRemains(t *testing.T) {
	clearGarminEnv(t)
	stateDir := rotateStateDir(t)
	seedFileStoreKeyRing(t, stateDir)

	// A completed rotation, driven through the command exactly as an operator
	// would: seed a record under v1, rotate to v2, confirm it resealed.
	oldKey, err := cryptostore.LoadKey(filepath.Join(stateDir, "keys"), 1)
	if err != nil {
		t.Fatalf("LoadKey version 1: %v", err)
	}
	files, err := store.NewFileStore(store.Config{Dir: filepath.Join(stateDir, "tokens"), Key: oldKey})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	set := store.NewTokenSet("token", testRefreshTokenValue, "client-id", time.Time{})
	if _, err := files.Save(context.Background(), config.DefaultPrincipalID, set, 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	stdout, err := runCommand(t, cmdRotateKey, "--state-dir="+stateDir, "--target-version=2")
	if err != nil {
		t.Fatalf("rotate-key to version 2 = %v, want success", err)
	}
	if !strings.Contains(stdout, "resealed") {
		t.Fatalf("stdout = %q, want it to report the reseal", stdout)
	}

	// The operator's next documented step: retire the old key by deleting it.
	if err := os.Remove(filepath.Join(stateDir, "keys", "key-v1.json")); err != nil {
		t.Fatalf("retire (remove) key version 1: %v", err)
	}

	// Re-running with the SAME target to double-check, per docs/operations.md,
	// must succeed and report completion rather than a missing-key refusal.
	stdout, err = runCommand(t, cmdRotateKey, "--state-dir="+stateDir, "--target-version=2")
	if err != nil {
		t.Fatalf("re-running rotate-key after the retiring key was already removed = %v, want success", err)
	}
	if !strings.Contains(stdout, "is complete for that record") {
		t.Errorf("stdout = %q, want it to report the rotation complete for the bound record", stdout)
	}
	// The claim must stay scoped to what a FileStore scan can actually check.
	// Record file names are one-way digests of the principal id, so a record for
	// a principal this configuration does not bind cannot be enumerated — and an
	// unqualified "rotation is already complete" here is what leads an operator
	// to delete a key such a record still needs.
	if !strings.Contains(stdout, "does not bind cannot be checked") {
		t.Errorf("stdout = %q, want it to state that an unbound principal's record "+
			"is not covered by the completion claim", stdout)
	}
}

// TestRotateKeySQLiteReportsPartialProgressWhenARowCannotBeRead is the
// command-level half of the partial-progress property.
//
// The store-level test proves ResealToActiveKey RETURNS the counts it managed
// alongside the error. This proves the command actually PRINTS them: moving the
// report below the command's error check would satisfy the store test and still
// leave the operator with nothing — neither what succeeded nor what remains. With
// one unreadable row the completion scan can never reach done, so those counts are
// the only thing the operator has to reason from before deciding about the key.
//
// garmin_token_sets is re-sealed before principals, so corrupting the principal's
// sealed identity fails the second table after the first has fully succeeded.
func TestRotateKeySQLiteReportsPartialProgressWhenARowCannotBeRead(t *testing.T) {
	clearGarminEnv(t)
	stateDir := rotateStateDir(t)
	oldKey := seedFileStoreKeyRing(t, stateDir)
	dbPath := filepath.Join(stateDir, "state.db")
	ctx := context.Background()

	seed, err := store.OpenSQLite(ctx, store.SQLiteConfig{Path: dbPath, Key: oldKey})
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	principal, err := seed.CreatePrincipal(ctx, "rotate-key-partial@example.com")
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	if _, err := seed.Save(ctx, principal.ID,
		store.NewTokenSet("token", testRefreshTokenValue, "client-id", time.Time{}), 0); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	if err := seed.LinkGarminAccount(ctx, principal.ID, store.GarminIdentity{
		AccountID: store.NewSecret("garmin-account-under-test"), DisplayName: "display-name",
	}); err != nil {
		t.Fatalf("LinkGarminAccount: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	// Raw, so the store's own encoding rules cannot repair it: no key opens this.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open the database directly: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE principals SET garmin_identity_sealed = ? WHERE id = ?`,
		[]byte("not an envelope"), principal.ID); err != nil {
		t.Fatalf("corrupt the sealed identity: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close the direct connection: %v", err)
	}

	stdout, err := runCommand(t, cmdRotateKey,
		"--state-dir="+stateDir, "--database-path="+dbPath, "--target-version=2")
	if err == nil {
		t.Fatal("rotate-key with an unreadable row succeeded, want it to report the failure")
	}
	if !strings.Contains(stdout, "garmin token sets resealed: 1") {
		t.Errorf("stdout = %q, want the partial progress the run did make before failing; "+
			"without it the operator has nothing to reason from and the retiring key can "+
			"never be retired", stdout)
	}
}
