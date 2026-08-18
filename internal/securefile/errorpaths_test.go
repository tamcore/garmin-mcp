package securefile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file closes real, previously-untested error branches in RestrictExistingDir
// and the package's shared internals: an owner-unreadable file (OpenFile refused
// after Lstat already accepted it), an over-long name (a genuine Lstat failure that
// is neither ErrNotExist nor ErrExist, so pathError's final fallback branch is
// reached), and a read-only parent directory that refuses to create anything in it.
// None of these need root or a race; they are ordinary permission and pathname
// failures an unprivileged process can always cause. Under uid 0 several of these
// checks do not hold — opening a 000 file and creating inside a 0500 directory both
// succeed for root — so those tests skip rather than fail.

// overlongName is longer than any real filesystem's NAME_MAX (255 bytes on
// every filesystem this package targets), so Lstat refuses it with
// ENAMETOOLONG: a genuine error that is neither ErrNotExist nor ErrExist,
// exercising pathError's final, unwrapped-cause branch.
func overlongName() string {
	return strings.Repeat("a", 1024)
}

func TestRestrictExistingDirTightensAWidenedDirectory(t *testing.T) {
	dir := filepath.Join(tempDir(t), "keys")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := RestrictExistingDir(dir, 0o700); err != nil {
		t.Fatalf("RestrictExistingDir: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("mode = %04o, want 0700", perm)
	}
}

func TestRestrictExistingDirReportsAnAbsentDirectoryWithoutCreatingIt(t *testing.T) {
	dir := filepath.Join(tempDir(t), "never-created")

	if err := RestrictExistingDir(dir, 0o700); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RestrictExistingDir on an absent directory: err = %v, want ErrNotFound", err)
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("RestrictExistingDir created the directory it was only asked to restrict: %v", statErr)
	}
}

func TestOpenParentRefusesTheVolumeRoot(t *testing.T) {
	if _, _, err := openParent(string(filepath.Separator)); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("openParent(%q): err = %v, want ErrInsecurePath", string(filepath.Separator), err)
	}
}

func TestReadFileRefusesAnOwnerUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can open a 000 file, so this refusal does not hold under uid 0")
	}
	path := filepath.Join(tempDir(t), "record.json")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if _, err := ReadFile(path, 1024); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("ReadFile of an owner-unreadable file: err = %v, want ErrInsecurePath", err)
	}
}

func TestRestrictExistingRefusesAnOwnerUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can open a 000 file, so this refusal does not hold under uid 0")
	}
	path := filepath.Join(tempDir(t), "record.json")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if err := RestrictExisting(path, 0o600); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("RestrictExisting of an owner-unreadable file: err = %v, want ErrInsecurePath", err)
	}
}

// TestInstallNewFileReportsAGenuinePathErrorForAnOverLongName drives the
// over-long-name failure through checkReplaceable's Lstat, covering
// installNewFile's non-ErrNotExist, non-regular-file Lstat branch and
// pathError's final fallback branch together.
func TestInstallNewFileReportsAGenuinePathErrorForAnOverLongName(t *testing.T) {
	path := filepath.Join(tempDir(t), overlongName())

	err := InstallNewFile(path, []byte(testMaterial), 0o600)
	if err == nil {
		t.Fatal("InstallNewFile with an over-long name unexpectedly succeeded")
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrExists) {
		t.Fatalf("InstallNewFile with an over-long name: err = %v, want neither ErrNotFound nor ErrExists", err)
	}
}

// TestOpenForLockingRefusesInAReadOnlyDirectory drives openForLocking's
// default branch (an OpenFile failure that is not ErrExist) by removing
// write access from the containing directory after it was opened: creating
// a fresh lock file then needs a write permission the directory no longer
// grants.
func TestOpenForLockingRefusesInAReadOnlyDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can create files in a 0500 directory, so this refusal does not hold under uid 0")
	}
	dir := tempDir(t)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	path := filepath.Join(dir, "lock")
	if _, err := OpenForLocking(path, 0o600); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("OpenForLocking in a read-only directory: err = %v, want ErrInsecurePath", err)
	}
}

// TestCheckOwnerOnlyRefusesAClosedFile and
// TestCheckOwnerRefusesAClosedFile drive checkOwnerOnly's and checkOwner's
// own file.Stat() failure branch directly: an already-closed *os.File cannot
// be stat'd, which is a real, if artificial, way to make Stat itself fail
// without needing root.
func TestCheckOwnerOnlyRefusesAClosedFile(t *testing.T) {
	path := filepath.Join(tempDir(t), "record.json")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := checkOwnerOnly(file, path); !errors.Is(err, ErrInsecurePermissions) {
		t.Fatalf("checkOwnerOnly on a closed file: err = %v, want ErrInsecurePermissions", err)
	}
}

func TestCheckOwnerRefusesAClosedFile(t *testing.T) {
	path := filepath.Join(tempDir(t), "record.json")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := checkOwner(file, path); !errors.Is(err, ErrInsecurePermissions) {
		t.Fatalf("checkOwner on a closed file: err = %v, want ErrInsecurePermissions", err)
	}
}

// TestRestrictFileRefusesAClosedFile and TestRestrictDirRefusesAClosedRoot
// drive restrictFile's and restrictDir's own Chmod failure branch: chmod on
// an already-closed descriptor fails, without needing a privileged setup.
func TestRestrictFileRefusesAClosedFile(t *testing.T) {
	path := filepath.Join(tempDir(t), "record.json")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := restrictFile(file, path, 0o600); err == nil {
		t.Fatal("restrictFile on a closed file unexpectedly succeeded")
	}
}

func TestRestrictDirRefusesAClosedRoot(t *testing.T) {
	dir := tempDir(t)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := restrictDir(root, dir, 0o700); err == nil {
		t.Fatal("restrictDir on a closed root unexpectedly succeeded")
	}
}

// fakeDirInfo is a minimal fs.FileInfo whose Sys() does not return a
// *syscall.Stat_t, so checkDirOwner's "cannot determine owner" branch can be
// driven directly: a real directory's FileInfo always carries one on unix, so
// there is no way to reach this branch through an actual file.
type fakeDirInfo struct{ fs.FileInfo }

func (fakeDirInfo) Sys() any { return nil }

func TestCheckDirOwnerRefusesWhenTheOwnerCannotBeDetermined(t *testing.T) {
	dir := tempDir(t)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if err := checkDirOwner(fakeDirInfo{info}, dir); !errors.Is(err, ErrInsecurePermissions) {
		t.Fatalf("checkDirOwner with an unrecognized Sys(): err = %v, want ErrInsecurePermissions", err)
	}
}
