package cmd_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/cmd"
	"github.com/tamcore/garmin-mcp/internal/cryptostore"
)

const cmdRepairPermissions = "repair-permissions"

// databaseFileSuffixes are the database file and its write-ahead-log and
// shared-memory sidecars, matching internal/store's own (unexported)
// databaseFileSuffixes.
var databaseFileSuffixes = []string{"", "-wal", "-shm"}

// repairPermsStateDir returns a state directory inside a symlink-free
// ancestry, the same requirement every other filesystem-touching command
// test in this package already has (macOS resolves /tmp through /var, which
// is itself a symlink, and internal/securefile refuses a symlinked
// ancestor).
func repairPermsStateDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the temporary directory: %v", err)
	}
	return resolved
}

// widen forces path to mode regardless of the process umask, simulating a
// Kubernetes fsGroup mount recursion that widened a file this process
// otherwise owns.
func widen(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("widen %q to %v: %v", path, mode, err)
	}
}

func mustMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	return info.Mode().Perm()
}

// TestRepairPermissionsTightensAWidenedKeyFileItOwns is the core repair case:
// a key file this process created, then a platform widened, is tightened
// back to owner-only, and the report names it.
func TestRepairPermissionsTightensAWidenedKeyFileItOwns(t *testing.T) {
	clearGarminEnv(t)
	stateDir := repairPermsStateDir(t)
	keysDir := filepath.Join(stateDir, "keys")

	if _, err := cryptostore.LoadOrCreateKey(keysDir, 1); err != nil {
		t.Fatalf("seed key version 1: %v", err)
	}
	keyPath := filepath.Join(keysDir, "key-v1.json")
	widen(t, keyPath, 0o644)

	stdout, err := runCommand(t, cmdRepairPermissions, "--state-dir="+stateDir)
	if err != nil {
		t.Fatalf("repair-permissions on a fixable key file: err = %v, want nil", err)
	}
	if !strings.Contains(stdout, "tightened") || !strings.Contains(stdout, keyPath) {
		t.Fatalf("stdout = %q, want it to name %q as tightened", stdout, keyPath)
	}
	if got := mustMode(t, keyPath); got != 0o600 {
		t.Fatalf("key file mode after repair = %v, want 0600", got)
	}
}

// TestRepairPermissionsTightensAWidenedDatabaseAndItsSidecars covers the
// database file plus both WAL sidecars in one pass, without ever going
// through store.OpenDatabase: the files are planted directly, so no SQLite
// connection is ever made by this test or by the command under test.
func TestRepairPermissionsTightensAWidenedDatabaseAndItsSidecars(t *testing.T) {
	clearGarminEnv(t)
	stateDir := repairPermsStateDir(t)
	dbPath := filepath.Join(stateDir, "garmin.db")

	for _, suffix := range databaseFileSuffixes {
		path := dbPath + suffix
		if err := os.WriteFile(path, []byte("not a real sqlite file"), 0o600); err != nil {
			t.Fatalf("seed %q: %v", path, err)
		}
		widen(t, path, 0o666)
	}

	stdout, err := runCommand(t, cmdRepairPermissions, "--state-dir="+stateDir, "--database-path="+dbPath)
	if err != nil {
		t.Fatalf("repair-permissions on a fixable database: err = %v, want nil", err)
	}
	for _, suffix := range databaseFileSuffixes {
		path := dbPath + suffix
		if !strings.Contains(stdout, path) {
			t.Fatalf("stdout = %q, want it to name %q", stdout, path)
		}
		if got := mustMode(t, path); got != 0o600 {
			t.Fatalf("mode of %q after repair = %v, want 0600", path, got)
		}
	}
}

// TestRepairPermissionsOnHealthyStateIsACleanNoOp: a freshly created,
// never-touched state directory has nothing to repair, and the command says
// so and exits zero.
func TestRepairPermissionsOnHealthyStateIsACleanNoOp(t *testing.T) {
	clearGarminEnv(t)
	stateDir := repairPermsStateDir(t)

	stdout, err := runCommand(t, cmdRepairPermissions, "--state-dir="+stateDir)
	if err != nil {
		t.Fatalf("repair-permissions on healthy state: err = %v, want nil", err)
	}
	if !strings.Contains(stdout, "nothing to do") {
		t.Fatalf("stdout = %q, want it to report a clean no-op", stdout)
	}

	// Idempotent: running it again changes nothing about the report either.
	stdout2, err := runCommand(t, cmdRepairPermissions, "--state-dir="+stateDir)
	if err != nil {
		t.Fatalf("second repair-permissions run: err = %v, want nil", err)
	}
	if stdout != stdout2 {
		t.Fatalf("repeated run report changed: first = %q, second = %q", stdout, stdout2)
	}
}

// TestRepairPermissionsDryRunChangesNothing proves --dry-run reports a
// fixable problem without touching it, and exits non-zero so a script can
// tell "found a problem" apart from "clean".
func TestRepairPermissionsDryRunChangesNothing(t *testing.T) {
	clearGarminEnv(t)
	stateDir := repairPermsStateDir(t)
	keysDir := filepath.Join(stateDir, "keys")

	if _, err := cryptostore.LoadOrCreateKey(keysDir, 1); err != nil {
		t.Fatalf("seed key version 1: %v", err)
	}
	keyPath := filepath.Join(keysDir, "key-v1.json")
	widen(t, keyPath, 0o644)

	stdout, err := runCommand(t, cmdRepairPermissions, "--state-dir="+stateDir, "--dry-run")
	if !errors.Is(err, cmd.ErrPermissionsUnresolved) {
		t.Fatalf("repair-permissions --dry-run on a fixable key file: err = %v, want ErrPermissionsUnresolved", err)
	}
	if !strings.Contains(stdout, "would tighten") || !strings.Contains(stdout, keyPath) {
		t.Fatalf("stdout = %q, want it to name %q as something it would tighten", stdout, keyPath)
	}
	if got := mustMode(t, keyPath); got != 0o644 {
		t.Fatalf("key file mode after --dry-run = %v, want unchanged 0644", got)
	}
}

// TestRepairPermissionsNeverCreatesDatabaseSidecars is the observable proof
// that this command never opens the database as a database: given a bare
// database file with no "-wal"/"-shm" sidecars, running the command must not
// bring either into existence. A real SQLite connection in WAL mode is
// exactly the thing that would create them.
func TestRepairPermissionsNeverCreatesDatabaseSidecars(t *testing.T) {
	clearGarminEnv(t)
	stateDir := repairPermsStateDir(t)
	dbPath := filepath.Join(stateDir, "garmin.db")

	if err := os.WriteFile(dbPath, []byte("not a real sqlite file"), 0o600); err != nil {
		t.Fatalf("seed %q: %v", dbPath, err)
	}

	if _, err := runCommand(t, cmdRepairPermissions, "--state-dir="+stateDir, "--database-path="+dbPath); err != nil {
		t.Fatalf("repair-permissions on an already-tight database: err = %v, want nil", err)
	}

	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(dbPath + suffix); !os.IsNotExist(err) {
			t.Fatalf("sidecar %q exists after running repair-permissions, want it never created", dbPath+suffix)
		}
	}
}

// TestRepairPermissionsRefusesASymlinkedStateDir is the RED case for item 1:
// a symlinked state root pointing at clean, owner-only state must not be
// followed. Today the command never checks the ancestry of --state-dir, so
// ReadDir/Lstat happily follow the symlink, report everything clean, and
// exit 0 — while serve's own securefile checks refuse the same symlinked
// component and never start.
func TestRepairPermissionsRefusesASymlinkedStateDir(t *testing.T) {
	clearGarminEnv(t)
	real := repairPermsStateDir(t)
	keysDir := filepath.Join(real, "keys")
	if _, err := cryptostore.LoadOrCreateKey(keysDir, 1); err != nil {
		t.Fatalf("seed key version 1: %v", err)
	}

	parent := repairPermsStateDir(t)
	symlinkRoot := filepath.Join(parent, "state")
	if err := os.Symlink(real, symlinkRoot); err != nil {
		t.Fatalf("symlink state dir: %v", err)
	}

	_, err := runCommand(t, cmdRepairPermissions, "--state-dir="+symlinkRoot)
	if err == nil {
		t.Fatalf("repair-permissions on a symlinked state dir: err = nil, want a refusal")
	}
}

// TestRepairPermissionsRecoversAnUnlistableKeysDirectory is the RED case for
// item 2: a keys directory this process owns but cannot currently list
// (mode 0300) must not make the widened key file inside it invisible. Today
// every ReadDir error is treated as an empty directory, so the command
// tightens the directory and exits 0 without ever seeing the widened file.
func TestRepairPermissionsRecoversAnUnlistableKeysDirectory(t *testing.T) {
	clearGarminEnv(t)
	stateDir := repairPermsStateDir(t)
	keysDir := filepath.Join(stateDir, "keys")
	if _, err := cryptostore.LoadOrCreateKey(keysDir, 1); err != nil {
		t.Fatalf("seed key version 1: %v", err)
	}
	keyPath := filepath.Join(keysDir, "key-v1.json")
	widen(t, keyPath, 0o644)
	widen(t, keysDir, 0o300)

	stdout, err := runCommand(t, cmdRepairPermissions, "--state-dir="+stateDir)
	if err != nil {
		t.Fatalf("repair-permissions recovering an unlistable directory: err = %v, want nil", err)
	}
	if got := mustMode(t, keyPath); got != 0o600 {
		t.Fatalf("stdout = %q, key file mode after repair = %v, want 0600 (widened file must not stay hidden)",
			stdout, got)
	}
}

// TestRepairPermissionsRejectsADirectoryNamedLikeAKeyFile is the RED case for
// item 3: a directory happens to carry a key file's name. Today
// listMatchingFiles skips any entry.IsDir(), so this is silently excluded
// from the target set and the command exits 0, while serve's key load fails
// because the path is not a regular file.
func TestRepairPermissionsRejectsADirectoryNamedLikeAKeyFile(t *testing.T) {
	clearGarminEnv(t)
	stateDir := repairPermsStateDir(t)
	keysDir := filepath.Join(stateDir, "keys")
	if err := os.MkdirAll(filepath.Join(keysDir, "key-v1.json"), 0o700); err != nil {
		t.Fatalf("seed directory named like a key file: %v", err)
	}

	_, err := runCommand(t, cmdRepairPermissions, "--state-dir="+stateDir)
	if err == nil {
		t.Fatalf("repair-permissions with a directory named like a key file: err = nil, want a refusal")
	}
}

// TestRepairPermissionsIncludesTheOAuthClientSecretHashFile is the RED case
// for item 4: a confidential OAuth client's secret-hash-file is read through
// securefile.ReadFile at start-up, but repairTargets never lists it. A
// widened digest file, sitting anywhere the operator pointed it, is
// invisible to this command today.
func TestRepairPermissionsIncludesTheOAuthClientSecretHashFile(t *testing.T) {
	clearGarminEnv(t)
	stateDir := repairPermsStateDir(t)
	otherDir := repairPermsStateDir(t)
	hashPath := filepath.Join(otherDir, "client.hash")
	if err := os.WriteFile(hashPath, []byte(strings.Repeat("a", 64)), 0o644); err != nil {
		t.Fatalf("seed secret hash file: %v", err)
	}

	clientsJSON := `[{"id":"repair-perms-test-client","redirect-uris":["https://127.0.0.1/callback"],` +
		`"scopes":["garmin:read"],"resources":["https://127.0.0.1:8443"],` +
		`"secret-hash-file":"` + hashPath + `"}]`
	t.Setenv("GARMIN_MCP_OAUTH_CLIENTS", clientsJSON)

	stdout, err := runCommand(t, cmdRepairPermissions,
		"--state-dir="+stateDir,
		"--transport=streamable-http",
		"--bind-address=127.0.0.1:8443",
		"--public-url=https://127.0.0.1:8443",
		"--database-path="+filepath.Join(otherDir, "garmin.db"),
		"--master-key-file="+filepath.Join(stateDir, "keys", "key-v1.json"),
	)
	if err == nil {
		t.Fatalf("repair-permissions with a widened, configured secret-hash-file: err = nil, want it flagged unresolved")
	}
	if !strings.Contains(stdout, hashPath) {
		t.Fatalf("stdout = %q, want it to name %q", stdout, hashPath)
	}
}

// TestRepairPermissionsIgnoresANonCanonicalKeyFileName is the RED case for
// item 8: cryptostore's keyFilePath only ever produces "key-v" + a canonical
// positive integer (strconv.Itoa never emits a leading zero, and version 0
// is invalid), but the current pattern also matches "key-v0.json" and
// "key-v00.json" — names the server itself never reads or writes. Widening
// one of those must not be reported as something repair-permissions fixed.
func TestRepairPermissionsIgnoresANonCanonicalKeyFileName(t *testing.T) {
	clearGarminEnv(t)
	stateDir := repairPermsStateDir(t)
	keysDir := filepath.Join(stateDir, "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		t.Fatalf("create keys dir: %v", err)
	}
	nonCanonical := filepath.Join(keysDir, "key-v00.json")
	if err := os.WriteFile(nonCanonical, []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed non-canonical key file name: %v", err)
	}

	stdout, err := runCommand(t, cmdRepairPermissions, "--state-dir="+stateDir)
	if err != nil {
		t.Fatalf("repair-permissions on a non-canonical key file name: err = %v, want nil", err)
	}
	if strings.Contains(stdout, nonCanonical) {
		t.Fatalf("stdout = %q, want it to never mention %q, which cryptostore never produces or reads",
			stdout, nonCanonical)
	}
}

// TestRepairPermissionsReportNeverContainsFileContent proves the report
// carries paths and modes only: a marker string written into both the key
// file and the database file must never surface on stdout.
func TestRepairPermissionsReportNeverContainsFileContent(t *testing.T) {
	clearGarminEnv(t)
	const marker = "SECRET-MARKER-DO-NOT-LEAK"
	stateDir := repairPermsStateDir(t)
	keysDir := filepath.Join(stateDir, "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		t.Fatalf("create keys dir: %v", err)
	}
	keyPath := filepath.Join(keysDir, "key-v1.json")
	if err := os.WriteFile(keyPath, []byte(marker), 0o644); err != nil {
		t.Fatalf("seed %q: %v", keyPath, err)
	}
	dbPath := filepath.Join(stateDir, "garmin.db")
	if err := os.WriteFile(dbPath, []byte(marker), 0o644); err != nil {
		t.Fatalf("seed %q: %v", dbPath, err)
	}

	stdout, err := runCommand(t, cmdRepairPermissions, "--state-dir="+stateDir, "--database-path="+dbPath)
	if err != nil {
		t.Fatalf("repair-permissions on owned, fixable files: err = %v, want nil", err)
	}
	if strings.Contains(stdout, marker) {
		t.Fatalf("stdout contains file content: %q", stdout)
	}
}
