//go:build unix

package securefile

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
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

// TestEnsureDirSkipsChmodOnAnAlreadyPrivateDirectory proves EnsureDir skips
// chmod when the mode already matches: ctime changes on chmod(2) even when
// the mode is unchanged, so an unchanged ctime is evidence the syscall never
// ran. Needed for a Kubernetes volume mount root, where chmod is EPERM even
// when the mode already holds.
func TestEnsureDirSkipsChmodOnAnAlreadyPrivateDirectory(t *testing.T) {
	dir := filepath.Join(tempDir(t), "keys")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Positive control: a real chmod must change ctime here, or the skip
	// assertion below would pass vacuously on a coarse-timestamp filesystem.
	controlBeforeSec, controlBeforeNsec, err := statCtime(dir)
	if err != nil {
		t.Fatalf("statCtime control before: %v", err)
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatalf("chmod control: %v", err)
	}
	controlAfterSec, controlAfterNsec, err := statCtime(dir)
	if err != nil {
		t.Fatalf("statCtime control after: %v", err)
	}
	if controlBeforeSec == controlAfterSec && controlBeforeNsec == controlAfterNsec {
		t.Skipf("ctime did not change after a real chmod on this filesystem; cannot observe a skip")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod restore: %v", err)
	}

	beforeSec, beforeNsec, err := statCtime(dir)
	if err != nil {
		t.Fatalf("statCtime before: %v", err)
	}

	if err := EnsureDir(dir, 0o700); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("mode = %04o, want unchanged 0700", perm)
	}

	afterSec, afterNsec, err := statCtime(dir)
	if err != nil {
		t.Fatalf("statCtime after: %v", err)
	}
	if beforeSec != afterSec || beforeNsec != afterNsec {
		t.Fatalf("ctime changed from (%d,%d) to (%d,%d): a chmod ran on an already owner-only directory",
			beforeSec, beforeNsec, afterSec, afterNsec)
	}
}

// TestEnsureDirStillChmodsAPermissiveDirectory is the companion to the test
// above: a group- or world-accessible directory must still be tightened, never
// accepted merely because an owner-only one is.
func TestEnsureDirStillChmodsAPermissiveDirectory(t *testing.T) {
	for _, perm := range []fs.FileMode{0o750, 0o755} {
		t.Run(perm.String(), func(t *testing.T) {
			dir := filepath.Join(tempDir(t), "keys")
			if err := os.Mkdir(dir, perm); err != nil {
				t.Fatalf("mkdir: %v", err)
			}

			if err := EnsureDir(dir, 0o700); err != nil {
				t.Fatalf("EnsureDir: %v", err)
			}

			info, err := os.Stat(dir)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if got := info.Mode().Perm(); got != 0o700 {
				t.Fatalf("mode = %04o, want 0700 (chmod must still run for a permissive directory)", got)
			}
		})
	}
}

// TestEnsureDirTightensASupersetMode proves the skip is an exact-mode match, not
// "current already has every requested bit": an existing 0700 must still be
// chmodded down to a requested 0500, or the exact-mode contract silently
// tolerates leftover owner bits the caller asked removed.
func TestEnsureDirTightensASupersetMode(t *testing.T) {
	dir := filepath.Join(tempDir(t), "keys")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := EnsureDir(dir, 0o500); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o500 {
		t.Fatalf("mode = %04o, want 0500 (a superset current mode must still be chmodded down)", got)
	}
}

// restrict's own "not a usable directory" branch (dir.go) is defense in depth
// against a *os.Root descriptor whose Stat(".") stops reporting a directory. No
// real construction was found to trigger it without root privileges: removing
// the directory out from under an open os.Root leaves the descriptor's Stat(".")
// still reporting a directory (the fd keeps the deleted inode alive), which was
// tried and observed to leave restrict succeeding, not failing. That branch is
// therefore left untouched and unexercised by a new test here, rather than
// backed by a test that cannot fail the way it claims to.

func TestWrapChmodDirErrorAddsTheRemedyForPermissionDenied(t *testing.T) {
	cause := &fs.PathError{Op: "chmodat", Path: ".", Err: fs.ErrPermission}

	err := wrapChmodDirError("/data", cause)

	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("wrapChmodDirError: err = %v, want it to wrap fs.ErrPermission", err)
	}
	if !strings.Contains(err.Error(), "subdirectory") {
		t.Fatalf("wrapChmodDirError: err = %q, want it to name the remedy (a subdirectory)", err)
	}
	if !strings.Contains(err.Error(), "most commonly") {
		t.Fatalf("wrapChmodDirError: err = %q, want the cause phrased as typical guidance, not asserted fact", err)
	}
}

func TestWrapChmodDirErrorLeavesOtherErrorsWithoutTheRemedy(t *testing.T) {
	cause := errors.New("disk exploded")

	err := wrapChmodDirError("/data", cause)

	if errors.Is(err, fs.ErrPermission) {
		t.Fatalf("wrapChmodDirError: err = %v, unexpectedly wraps fs.ErrPermission", err)
	}
	if strings.Contains(err.Error(), "subdirectory") {
		t.Fatalf("wrapChmodDirError: err = %q, a non-permission error must not carry the remedy text", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("wrapChmodDirError: err = %v, want it to still wrap the original cause", err)
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
