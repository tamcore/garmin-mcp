package testkit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateManifestReadsThePresentFileAndSkipsTheAbsentOne(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, PrivateManifestDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools.json"), []byte(`{"tools":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "internal", "pkg")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	if got := string(PrivateManifest(t, "tools.json")); got != `{"tools":[]}` {
		t.Errorf("PrivateManifest = %q", got)
	}

	var skipped bool
	t.Run("absent", func(t *testing.T) {
		defer func() { skipped = t.Skipped() }()
		PrivateManifest(t, "resources.json")
		t.Error("PrivateManifest returned for an absent file")
	})
	if !skipped {
		t.Error("an absent manifest did not skip the test")
	}
}
