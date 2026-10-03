package testkit

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// PrivateManifestDir is where maintainers keep the pinned upstream manifests,
// relative to the module root. It is gitignored, so CI never has it.
const PrivateManifestDir = ".private/compat"

// PrivateManifest returns the pinned upstream manifest name (for example
// "tools.json"), and skips the test when the maintainer-only copy is absent.
func PrivateManifest(t testing.TB, name string) []byte {
	t.Helper()

	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("finding the module root: %v", err)
	}
	path := filepath.Join(root, PrivateManifestDir, name)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("%s is absent: the pinned upstream manifest is maintainer-only", path)
	}
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return raw
}

// moduleRoot walks up from the working directory to the directory holding go.mod.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fs.ErrNotExist
		}
		dir = parent
	}
}
