package testkit

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestGoldenWritesUnderUpdateAndThenMatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "testdata", "x.golden.json")
	got := []byte("{\"a\": 1}\n")

	if err := flag.Set(updateFlag, "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = flag.Set(updateFlag, "false") })
	Golden(t, path, got)

	written, err := os.ReadFile(path)
	if err != nil || string(written) != string(got) {
		t.Fatalf("golden file = %q, %v; want %q", written, err, got)
	}

	if err := flag.Set(updateFlag, "false"); err != nil {
		t.Fatal(err)
	}
	Golden(t, path, got)
}
