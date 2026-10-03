package testkit

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// updateFlag rewrites golden files instead of comparing against them.
const updateFlag = "update"

// The flag is registered here rather than held in a package-level variable.
func init() {
	flag.Bool(updateFlag, false, "rewrite golden files under testdata/")
}

// Golden compares got with the golden file at path, or rewrites the file when the
// test binary runs with -update.
func Golden(t testing.TB, path string, got []byte) {
	t.Helper()

	if shouldUpdate() {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v (run `go test -run %s -update` to create it)", path, err, t.Name())
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s does not match the registered surface. If the change is intended, run "+
			"`go test -run %s -update` in this package and review the diff", path, t.Name())
	}
}

func shouldUpdate() bool {
	f := flag.Lookup(updateFlag)
	if f == nil {
		return false
	}
	getter, ok := f.Value.(flag.Getter)
	if !ok {
		return false
	}
	update, _ := getter.Get().(bool)
	return update
}
