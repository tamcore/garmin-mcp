package securefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenForLockingCreatesAndReturnsAnOpenFile(t *testing.T) {
	path := filepath.Join(tempDir(t), "record.json.lock")

	file, err := OpenForLocking(path, 0o600)
	if err != nil {
		t.Fatalf("OpenForLocking: %v", err)
	}
	defer func() { _ = file.Close() }()

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat the created lock file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("lock file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestOpenForLockingReopensAnExistingLockFile(t *testing.T) {
	path := filepath.Join(tempDir(t), "record.json.lock")

	first, err := OpenForLocking(path, 0o600)
	if err != nil {
		t.Fatalf("first OpenForLocking: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first handle: %v", err)
	}

	second, err := OpenForLocking(path, 0o600)
	if err != nil {
		t.Fatalf("second OpenForLocking: %v", err)
	}
	defer func() { _ = second.Close() }()
}

func TestOpenForLockingRefusesASymlinkedTarget(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "record.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed target file: %v", err)
	}
	lockPath := filepath.Join(dir, "record.json.lock")
	if err := os.Symlink(target, lockPath); err != nil {
		t.Fatalf("plant symlink: %v", err)
	}

	if _, err := OpenForLocking(lockPath, 0o600); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("OpenForLocking on a symlinked path: err = %v, want ErrInsecurePath", err)
	}
}

func TestOpenForLockingRefusesADirectory(t *testing.T) {
	dir := tempDir(t)
	lockPath := filepath.Join(dir, "record.json.lock")
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatalf("create directory at the lock path: %v", err)
	}

	if _, err := OpenForLocking(lockPath, 0o600); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("OpenForLocking on a directory: err = %v, want ErrInsecurePath", err)
	}
}

func TestOpenForLockingRefusesAnInsecurePermissionOnAnExistingFile(t *testing.T) {
	dir := tempDir(t)
	lockPath := filepath.Join(dir, "record.json.lock")
	if err := os.WriteFile(lockPath, []byte(""), 0o644); err != nil {
		t.Fatalf("seed a world-readable lock file: %v", err)
	}

	if _, err := OpenForLocking(lockPath, 0o600); !errors.Is(err, ErrInsecurePermissions) {
		t.Fatalf("OpenForLocking on a world-readable existing file: err = %v, want ErrInsecurePermissions", err)
	}
}
