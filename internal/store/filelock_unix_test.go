//go:build unix

package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/securefile"
)

// TestLockRecordRefusesASymlinkedLockFile is the hardening property item 2
// requires: lockRecord must never follow a symlink planted at the lock path.
// Following it would let a planted symlink aimed at the record file itself
// split the lock domain across two inodes once the record is atomically
// replaced, so two processes could each believe they hold the lock and write
// concurrently — exactly the failure the lock exists to prevent.
func TestLockRecordRefusesASymlinkedLockFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "record.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed target file: %v", err)
	}

	lockPath := filepath.Join(dir, "record.json.lock")
	if err := os.Symlink(target, lockPath); err != nil {
		t.Fatalf("plant symlinked lock file: %v", err)
	}

	if _, err := lockRecord(lockPath); err == nil {
		t.Fatal("lockRecord followed a symlinked lock path, want it refused")
	} else if !errors.Is(err, securefile.ErrInsecurePath) {
		t.Fatalf("lockRecord on a symlinked lock path: err = %v, want it to wrap securefile.ErrInsecurePath", err)
	}
}
