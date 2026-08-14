//go:build unix

package securefile

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestWriteFileEnforcesTheRequestedMode(t *testing.T) {
	path := filepath.Join(tempDir(t), "record.json")
	if err := WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %04o, want 0600", perm)
	}
}

func TestEnsureDirEnforcesTheRequestedMode(t *testing.T) {
	dir := filepath.Join(tempDir(t), "keys")
	if err := EnsureDir(dir, 0o700); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("mode = %04o, want 0700", perm)
	}
}

func TestEnsureDirTightensAnExistingPermissiveDirectory(t *testing.T) {
	dir := filepath.Join(tempDir(t), "keys")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := EnsureDir(dir, 0o700); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("mode = %04o, want 0700", perm)
	}
}

// TestEnsureDirNeverChmodsASymlinkTarget is the regression test for a chmod that ran
// before the symlink check: the mode of whatever a planted symlink pointed at was
// changed, and only then was the path refused. Verifying the object's type and
// identity through the descriptor first is what stops that.
func TestEnsureDirNeverChmodsASymlinkTarget(t *testing.T) {
	root := tempDir(t)
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if err := EnsureDir(link, 0o700); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("EnsureDir on a symlink: err = %v, want ErrInsecurePath", err)
	}
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("lstat target: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Fatalf("the refused EnsureDir changed the symlink target to %04o", perm)
	}
}

func TestReadFileRefusesGroupOrWorldAccess(t *testing.T) {
	for _, perm := range []fs.FileMode{0o640, 0o604, 0o660, 0o666, 0o644, 0o601} {
		t.Run(perm.String(), func(t *testing.T) {
			path := filepath.Join(tempDir(t), "record.json")
			if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			if err := os.Chmod(path, perm); err != nil {
				t.Fatalf("chmod: %v", err)
			}

			if _, err := ReadFile(path, 1024); !errors.Is(err, ErrInsecurePermissions) {
				t.Fatalf("ReadFile with mode %04o: err = %v, want ErrInsecurePermissions", perm, err)
			}
		})
	}
}

func TestReadFileAcceptsStricterThanOwnerReadWrite(t *testing.T) {
	path := filepath.Join(tempDir(t), "record.json")
	if err := os.WriteFile(path, []byte("x"), 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := ReadFile(path, 1024); err != nil {
		t.Fatalf("ReadFile with mode 0400: %v", err)
	}
}

// TestReadFileRefusesAFifoWithoutBlocking is the availability half of the
// regular-file rule: a mode 0600 FIFO with no writer would hang a blocking open
// forever, so the load must be refused instead of waiting.
func TestReadFileRefusesAFifoWithoutBlocking(t *testing.T) {
	path := filepath.Join(tempDir(t), "record.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := ReadFile(path, 1024)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrInsecurePath) {
			t.Fatalf("ReadFile of a FIFO: err = %v, want ErrInsecurePath", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadFile blocked on a FIFO")
	}
}

func TestReadFileRefusesADeviceFile(t *testing.T) {
	if _, err := os.Lstat("/dev/null"); err != nil {
		t.Skipf("/dev/null unavailable: %v", err)
	}

	if _, err := ReadFile("/dev/null", 1024); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("ReadFile of /dev/null: err = %v, want ErrInsecurePath", err)
	}
}

func TestReadFileRefusesASocket(t *testing.T) {
	path := filepath.Join(tempDir(t), "record.json")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Skipf("unix socket unsupported: %v", err)
	}
	defer func() { _ = listener.Close() }()

	if _, err := ReadFile(path, 1024); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("ReadFile of a socket: err = %v, want ErrInsecurePath", err)
	}
}
