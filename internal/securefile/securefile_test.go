package securefile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testMaterial is the placeholder file content this package's tests write and
// read back; shared so golangci-lint's goconst does not flag the repeated
// literal across this file and perm_unix_test.go.
const testMaterial = "material"

// tempDir returns a temporary directory with every symlink resolved. t.TempDir
// alone is not enough: on macOS it sits under /var, which is a symlink to
// /private/var, and the full-ancestry check correctly refuses that.
func tempDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return resolved
}

func TestWriteFileThenReadFileRoundTrips(t *testing.T) {
	path := filepath.Join(tempDir(t), "record.json")

	if err := WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	raw, err := ReadFile(path, 1024)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(raw) != "payload" {
		t.Fatalf("ReadFile = %q, want %q", raw, "payload")
	}
}

func TestWriteFileReplacesExistingContent(t *testing.T) {
	path := filepath.Join(tempDir(t), "record.json")

	for _, want := range []string{"first", "second-longer"} {
		if err := WriteFile(path, []byte(want), 0o600); err != nil {
			t.Fatalf("WriteFile %q: %v", want, err)
		}
		raw, err := ReadFile(path, 1024)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(raw) != want {
			t.Fatalf("ReadFile = %q, want %q", raw, want)
		}
	}
}

func TestWriteFileLeavesNoTemporarySibling(t *testing.T) {
	dir := tempDir(t)
	if err := WriteFile(filepath.Join(dir, "record.json"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("temporary file %q survived the write", entry.Name())
		}
	}
}

func TestReadFileReportsAnAbsentFile(t *testing.T) {
	_, err := ReadFile(filepath.Join(tempDir(t), "absent.json"), 1024)
	if !errors.Is(err, ErrNotFound) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadFile of an absent file: err = %v, want ErrNotFound and fs.ErrNotExist", err)
	}
}

func TestReadFileReportsAnAbsentDirectory(t *testing.T) {
	_, err := ReadFile(filepath.Join(tempDir(t), "absent", "absent.json"), 1024)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadFile below an absent directory: err = %v, want ErrNotFound", err)
	}
}

func TestReadFileRefusesContentOverTheLimit(t *testing.T) {
	path := filepath.Join(tempDir(t), "big.json")
	if err := WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := ReadFile(path, 4); err == nil {
		t.Fatal("ReadFile with a 4 byte limit accepted 10 bytes")
	}
	if _, err := ReadFile(path, 10); err != nil {
		t.Fatalf("ReadFile with an exact limit: %v", err)
	}
}

func TestReadFileRefusesADirectory(t *testing.T) {
	dir := tempDir(t)
	if err := EnsureDir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	if _, err := ReadFile(filepath.Join(dir, "sub"), 1024); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("ReadFile of a directory: err = %v, want ErrInsecurePath", err)
	}
}

func TestReadFileRefusesASymlinkedFile(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "target.json")
	if err := WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if _, err := ReadFile(link, 1024); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("ReadFile through a symlink: err = %v, want ErrInsecurePath", err)
	}
}

func TestReadFileRefusesASymlinkedAncestor(t *testing.T) {
	root := tempDir(t)
	real := filepath.Join(root, "real")
	if err := EnsureDir(real, 0o700); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	if err := WriteFile(filepath.Join(real, "f.json"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if _, err := ReadFile(filepath.Join(link, "f.json"), 1024); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("ReadFile through a symlinked ancestor: err = %v, want ErrInsecurePath", err)
	}
}

func TestWriteFileRefusesASymlinkedTarget(t *testing.T) {
	dir := tempDir(t)
	elsewhere := filepath.Join(dir, "attacker.json")
	if err := WriteFile(elsewhere, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	link := filepath.Join(dir, "record.json")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if err := WriteFile(link, []byte("new"), 0o600); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("WriteFile onto a symlink: err = %v, want ErrInsecurePath", err)
	}
	raw, err := os.ReadFile(elsewhere)
	if err != nil {
		t.Fatalf("read attacker file: %v", err)
	}
	if string(raw) != "{}" {
		t.Fatal("the write followed the symlink")
	}
}

func TestWriteFileRefusesASymlinkedAncestor(t *testing.T) {
	root := tempDir(t)
	real := filepath.Join(root, "real")
	if err := EnsureDir(real, 0o700); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	err := WriteFile(filepath.Join(link, "f.json"), []byte("x"), 0o600)
	if !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("WriteFile through a symlinked ancestor: err = %v, want ErrInsecurePath", err)
	}
	if _, statErr := os.Lstat(filepath.Join(real, "f.json")); statErr == nil {
		t.Fatal("the write followed the symlinked ancestor")
	}
}

func TestInstallNewFileCreatesTheFile(t *testing.T) {
	path := filepath.Join(tempDir(t), "key.json")

	if err := InstallNewFile(path, []byte(testMaterial), 0o600); err != nil {
		t.Fatalf("InstallNewFile: %v", err)
	}
	raw, err := ReadFile(path, 1024)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(raw) != testMaterial {
		t.Fatalf("ReadFile = %q, want %q", raw, testMaterial)
	}
}

// TestInstallNewFileRefusesToReplaceAWinner is the no-replace requirement: two
// concurrent creators must not be able to overwrite each other's material.
func TestInstallNewFileRefusesToReplaceAWinner(t *testing.T) {
	path := filepath.Join(tempDir(t), "key.json")
	if err := InstallNewFile(path, []byte("winner"), 0o600); err != nil {
		t.Fatalf("InstallNewFile: %v", err)
	}

	err := InstallNewFile(path, []byte("loser"), 0o600)
	if !errors.Is(err, ErrExists) || !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second InstallNewFile: err = %v, want ErrExists and fs.ErrExist", err)
	}
	raw, readErr := ReadFile(path, 1024)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(raw) != "winner" {
		t.Fatalf("the loser replaced the winner: %q", raw)
	}
}

func TestInstallNewFileLeavesNoTemporarySiblingOnCollision(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "key.json")
	if err := InstallNewFile(path, []byte("winner"), 0o600); err != nil {
		t.Fatalf("InstallNewFile: %v", err)
	}
	if err := InstallNewFile(path, []byte("loser"), 0o600); !errors.Is(err, ErrExists) {
		t.Fatalf("second InstallNewFile: err = %v, want ErrExists", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("temporary file %q survived the collision", entry.Name())
		}
	}
}

func TestEnsureDirCreatesEveryLevel(t *testing.T) {
	dir := filepath.Join(tempDir(t), "config", "garmin-mcp", "keys")

	if err := EnsureDir(dir, 0o700); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", dir)
	}
}

func TestEnsureDirIsIdempotent(t *testing.T) {
	dir := filepath.Join(tempDir(t), "keys")
	for range 3 {
		if err := EnsureDir(dir, 0o700); err != nil {
			t.Fatalf("EnsureDir: %v", err)
		}
	}
}

func TestEnsureDirRefusesASymlinkedDirectory(t *testing.T) {
	root := tempDir(t)
	real := filepath.Join(root, "real")
	if err := EnsureDir(real, 0o700); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if err := EnsureDir(link, 0o700); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("EnsureDir on a symlink: err = %v, want ErrInsecurePath", err)
	}
}

func TestEnsureDirRefusesAFileInThePath(t *testing.T) {
	root := tempDir(t)
	file := filepath.Join(root, "file")
	if err := WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := EnsureDir(filepath.Join(file, "sub"), 0o700); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("EnsureDir below a file: err = %v, want ErrInsecurePath", err)
	}
}

func TestRemoveDeletesAFileAndToleratesAnAbsentOne(t *testing.T) {
	path := filepath.Join(tempDir(t), "record.json")
	if err := WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the file survived Remove: %v", err)
	}
	if err := Remove(path); err != nil {
		t.Fatalf("Remove of an absent file: %v", err)
	}
}

func TestCheckAncestryAcceptsARealPathAndRefusesASymlinkedOne(t *testing.T) {
	root := tempDir(t)
	if err := CheckAncestry(filepath.Join(root, "absent", "file.json")); err != nil {
		t.Fatalf("CheckAncestry of a real path: %v", err)
	}

	real := filepath.Join(root, "real")
	if err := EnsureDir(real, 0o700); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := CheckAncestry(filepath.Join(link, "file.json")); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("CheckAncestry of a symlinked ancestor: err = %v, want ErrInsecurePath", err)
	}
}

func TestEmptyPathsAreRejected(t *testing.T) {
	for name, call := range map[string]func() error{
		"ReadFile":       func() error { _, err := ReadFile("", 16); return err },
		"WriteFile":      func() error { return WriteFile("", nil, 0o600) },
		"InstallNewFile": func() error { return InstallNewFile("", nil, 0o600) },
		"Remove":         func() error { return Remove("") },
		"EnsureDir":      func() error { return EnsureDir("", 0o700) },
		"CheckAncestry":  func() error { return CheckAncestry("") },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatalf("%s(\"\") accepted an empty path", name)
			}
		})
	}
}
