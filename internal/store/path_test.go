package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveTokenFilePathAppendsTheLegacyFileNameForADirectory(t *testing.T) {
	dir := tempDir(t)

	got, err := ResolveTokenFilePath(dir)
	if err != nil {
		t.Fatalf("ResolveTokenFilePath: %v", err)
	}
	if want := filepath.Join(dir, legacyTokenFileName); got != want {
		t.Fatalf("ResolveTokenFilePath = %q, want %q", got, want)
	}
}

func TestResolveTokenFilePathAppendsTheLegacyFileNameForANonJSONPath(t *testing.T) {
	// Upstream treats any path without a .json suffix as a directory, even when it
	// does not exist yet.
	base := filepath.Join(tempDir(t), "garminconnect")

	got, err := ResolveTokenFilePath(base)
	if err != nil {
		t.Fatalf("ResolveTokenFilePath: %v", err)
	}
	if want := filepath.Join(base, legacyTokenFileName); got != want {
		t.Fatalf("ResolveTokenFilePath = %q, want %q", got, want)
	}
}

func TestResolveTokenFilePathKeepsAnExplicitJSONFile(t *testing.T) {
	path := filepath.Join(tempDir(t), "tokens.JSON")

	got, err := ResolveTokenFilePath(path)
	if err != nil {
		t.Fatalf("ResolveTokenFilePath: %v", err)
	}
	if got != path {
		t.Fatalf("ResolveTokenFilePath = %q, want %q (the suffix test is case-insensitive)", got, path)
	}
}

// TestResolveTokenFilePathRejectsAnotherUsersHome mirrors the 0.3.10 rule: bare ~
// and ~/... expand into the current user's home and are fine, while ~someone
// points into another account.
func TestResolveTokenFilePathRejectsAnotherUsersHome(t *testing.T) {
	for _, path := range []string{"~root/tokens.json", "~otheruser", `~admin\tokens.json`} {
		t.Run(path, func(t *testing.T) {
			_, err := ResolveTokenFilePath(path)
			if !errors.Is(err, ErrForeignHomePath) {
				t.Fatalf("ResolveTokenFilePath(%q) err = %v, want ErrForeignHomePath", path, err)
			}
		})
	}
}

func TestResolveTokenFilePathExpandsTheCurrentUsersHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory available: %v", err)
	}

	got, err := ResolveTokenFilePath(filepath.Join("~", ".garminconnect", "tokens.json"))
	if err != nil {
		t.Fatalf("ResolveTokenFilePath: %v", err)
	}
	if want := filepath.Join(home, ".garminconnect", "tokens.json"); got != want {
		t.Fatalf("ResolveTokenFilePath = %q, want %q", got, want)
	}
}

func TestResolveTokenFilePathRejectsAnEmptyPath(t *testing.T) {
	if _, err := ResolveTokenFilePath(""); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf(`ResolveTokenFilePath("") err = %v, want ErrInvalidConfig`, err)
	}
}

// TestResolveTokenFilePathRejectsASymlinkedFinalComponent covers the last
// component, which O_NOFOLLOW would also catch.
func TestResolveTokenFilePathRejectsASymlinkedFinalComponent(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "real.json")
	if err := os.WriteFile(target, []byte(`{"di_token":"x"}`), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if _, err := ResolveTokenFilePath(link); !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("ResolveTokenFilePath(symlink) err = %v, want ErrInsecurePath", err)
	}
}

// TestResolveTokenFilePathRejectsASymlinkedAncestor is the case O_NOFOLLOW misses
// and 0.3.10 added: an intermediate symlinked directory redirects the whole tree.
func TestResolveTokenFilePathRejectsASymlinkedAncestor(t *testing.T) {
	root := tempDir(t)
	attacker := filepath.Join(root, "attacker")
	if err := os.MkdirAll(attacker, 0o700); err != nil {
		t.Fatalf("mkdir attacker dir: %v", err)
	}
	link := filepath.Join(root, "garminconnect")
	if err := os.Symlink(attacker, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	_, err := ResolveTokenFilePath(filepath.Join(link, "tokens.json"))
	if !errors.Is(err, ErrInsecurePath) {
		t.Fatalf("ResolveTokenFilePath through a symlinked parent: err = %v, want ErrInsecurePath", err)
	}
}

func TestResolveTokenFilePathErrorsNeverQuoteFileContent(t *testing.T) {
	dir := tempDir(t)
	link := filepath.Join(dir, "tokens.json")
	if err := os.Symlink(filepath.Join(dir, "missing.json"), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	_, err := ResolveTokenFilePath(link)
	if err == nil {
		t.Fatal("a symlinked path must be refused")
	}
	if strings.Contains(err.Error(), "di_token") {
		t.Fatalf("the error must not describe file content: %v", err)
	}
}
