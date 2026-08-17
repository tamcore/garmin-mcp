package securefile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// RestrictExisting exists for files this package does not create itself: the SQLite
// driver opens the database, the write-ahead log and the shared-memory file with a
// mode masked by the process umask, so they can land group- or world-readable. The
// process must not change its umask — that is global mutable state — so the files
// are tightened right after they appear.

func TestRestrictExistingMakesAForeignFileOwnerOnly(t *testing.T) {
	t.Parallel()
	path := filepath.Join(tempDir(t), "database.db")
	if err := os.WriteFile(path, []byte("db-bytes"), 0o644); err != nil { //nolint:gosec // deliberately permissive
		t.Fatalf("plant a group-readable file: %v", err)
	}

	if err := RestrictExisting(path, 0o600); err != nil {
		t.Fatalf("RestrictExisting: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %#o, want 0600", mode)
	}

	// The content must survive: this is a chmod, not a rewrite.
	content, err := os.ReadFile(path) //nolint:gosec // test-owned temp path
	if err != nil || string(content) != "db-bytes" {
		t.Errorf("content = %q err = %v, want the original bytes", content, err)
	}
}

func TestRestrictExistingReportsAnAbsentFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(tempDir(t), "never-created.db-wal")
	err := RestrictExisting(path, 0o600)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound so a caller can ignore a file SQLite has not created yet", err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want it to wrap fs.ErrNotExist as well", err)
	}
}

func TestRestrictExistingRefusesASymlink(t *testing.T) {
	t.Parallel()
	dir := tempDir(t)
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := RestrictExisting(link, 0o600); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("err = %v, want ErrInsecurePath: a planted symlink must not absorb the chmod", err)
	}
}

func TestRestrictExistingRefusesAnEmptyPath(t *testing.T) {
	t.Parallel()

	if err := RestrictExisting("", 0o600); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("err = %v, want ErrInsecurePath", err)
	}
}
