//go:build unix

package store

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
)

// Environment contract for the umask subprocess. umask is process-global, so a
// hostile-umask test cannot run in the same process as the rest of the suite: it
// would change the mask under every other test. The parent re-executes the test
// binary instead.
const (
	umaskChildEnv = "GARMIN_MCP_STORE_UMASK_CHILD"
	umaskDirEnv   = "GARMIN_MCP_STORE_UMASK_DIR"
)

func assertOwnerOnly(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != want {
		t.Fatalf("%q has mode %04o, want %04o", path, perm, want)
	}
}

func TestSaveCreatesOwnerOnlyFilesInOwnerOnlyDirectories(t *testing.T) {
	store, dir := newTestStore(t)

	if _, err := store.Save(context.Background(), testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}

	record := store.recordPath(testPrincipal)
	assertOwnerOnly(t, record, 0o600)
	assertOwnerOnly(t, filepath.Dir(record), 0o700)
	assertOwnerOnly(t, dir, 0o700)
}

// TestHostileUmaskIsIgnored runs the store in a subprocess whose umask is 0, the
// most permissive possible. Without an explicit chmod the record would land at
// 0666 and any local account could read the refresh token.
func TestHostileUmaskIsIgnored(t *testing.T) {
	if os.Getenv(umaskChildEnv) != "" {
		t.Skip("this is the child process")
	}
	dir := tempDir(t)

	cmd := exec.Command(os.Args[0], "-test.run=TestHostileUmaskChild", "-test.v")
	cmd.Env = append(os.Environ(), umaskChildEnv+"=1", umaskDirEnv+"="+dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("umask subprocess failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "--- PASS: TestHostileUmaskChild") {
		t.Fatalf("the umask child did not run:\n%s", output)
	}

	// The parent verifies the artefacts the child produced, so a child that
	// silently skipped cannot masquerade as a pass.
	key, err := cryptostore.LoadKey(filepath.Join(dir, "keys"), 1)
	if err != nil {
		t.Fatalf("LoadKey written under umask 0: %v", err)
	}
	store, err := NewFileStore(Config{Dir: dir, Key: key})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	record := store.recordPath(testPrincipal)
	assertOwnerOnly(t, record, 0o600)
	assertOwnerOnly(t, filepath.Dir(record), 0o700)
	assertOwnerOnly(t, filepath.Join(dir, "keys", "key-v1.json"), 0o600)
}

// TestHostileUmaskChild is the subprocess body. It runs only when the parent sets
// the marker, so an ordinary `go test` run skips it.
func TestHostileUmaskChild(t *testing.T) {
	if os.Getenv(umaskChildEnv) == "" {
		t.Skip("not the umask child process")
	}
	dir := os.Getenv(umaskDirEnv)
	if dir == "" {
		t.Fatalf("%s is not set", umaskDirEnv)
	}

	previous := syscall.Umask(0)
	defer syscall.Umask(previous)

	key, err := cryptostore.LoadOrCreateKey(filepath.Join(dir, "keys"), 1)
	if err != nil {
		t.Fatalf("LoadOrCreateKey: %v", err)
	}
	store, err := NewFileStore(Config{Dir: dir, Key: key})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if _, err := store.Save(context.Background(), testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	assertOwnerOnly(t, store.recordPath(testPrincipal), 0o600)
	assertOwnerOnly(t, filepath.Dir(store.recordPath(testPrincipal)), 0o700)
}

func TestLoadRefusesAGroupOrWorldReadableRecord(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Save(ctx, testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.Chmod(store.recordPath(testPrincipal), 0o644); err != nil {
		t.Fatalf("chmod record: %v", err)
	}

	if _, _, err := store.Load(ctx, testPrincipal); !errors.Is(err, ErrInsecurePermissions) {
		t.Fatalf("Load of a world-readable record: err = %v, want ErrInsecurePermissions", err)
	}
}

// TestSaveRefusesASymlinkedRecordPath is the write-side half of the 0.3.10
// hardening: a planted symlink must not redirect a token write.
func TestSaveRefusesASymlinkedRecordPath(t *testing.T) {
	store, dir := newTestStore(t)
	record := store.recordPath(testPrincipal)

	if err := os.MkdirAll(filepath.Dir(record), 0o700); err != nil {
		t.Fatalf("mkdir record dir: %v", err)
	}
	elsewhere := filepath.Join(dir, "attacker.json")
	if err := os.WriteFile(elsewhere, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write attacker file: %v", err)
	}
	if err := os.Symlink(elsewhere, record); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	_, err := store.Save(context.Background(), testPrincipal, newTestTokens(), 0)
	if !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("Save onto a symlink: err = %v, want ErrInsecurePath", err)
	}
	planted, readErr := os.ReadFile(elsewhere)
	if readErr != nil {
		t.Fatalf("read attacker file: %v", readErr)
	}
	if string(planted) != "{}" {
		t.Fatal("the write followed the symlink")
	}
}

func TestNewFileStoreRefusesASymlinkedDirectory(t *testing.T) {
	root := tempDir(t)
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	key, err := cryptostore.LoadOrCreateKey(filepath.Join(root, "keys"), 1)
	if err != nil {
		t.Fatalf("LoadOrCreateKey: %v", err)
	}

	if _, err := NewFileStore(Config{Dir: link, Key: key}); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("NewFileStore with a symlinked dir: err = %v, want ErrInsecurePath", err)
	}
}

func TestExportLegacyTokenFileIsOwnerOnly(t *testing.T) {
	dir := tempDir(t)

	path, err := ExportLegacyTokenFile(dir, newTestTokens())
	if err != nil {
		t.Fatalf("ExportLegacyTokenFile: %v", err)
	}
	assertOwnerOnly(t, path, 0o600)
	assertOwnerOnly(t, dir, 0o700)
}
