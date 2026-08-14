//go:build windows

package store

import (
	"context"
	"os/exec"
	"os/user"
	"strings"
	"testing"
)

// aclEntries returns the access-control entries icacls reports for path. icacls is
// the platform's own tool, so the assertion checks what Windows actually
// enforces: os.Stat on Windows reports a synthesized 0666 or 0444 that says
// nothing about the ACL.
func aclEntries(t *testing.T, path string) []string {
	t.Helper()
	output, err := exec.Command("icacls", path).CombinedOutput()
	if err != nil {
		t.Fatalf("icacls %q: %v\n%s", path, err, output)
	}

	var entries []string
	for _, line := range strings.Split(string(output), "\n") {
		trimmed := strings.TrimSpace(line)
		// icacls prints "<path> <principal>:(perm)", then further
		// "<principal>:(perm)" lines, and finishes with a summary line.
		if trimmed == "" || strings.HasPrefix(trimmed, "Successfully processed") {
			continue
		}
		trimmed = strings.TrimPrefix(trimmed, path)
		if entry := strings.TrimSpace(trimmed); entry != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

func assertUserOnlyACL(t *testing.T, path string) {
	t.Helper()
	current, err := user.Current()
	if err != nil {
		t.Fatalf("resolve current user: %v", err)
	}
	entries := aclEntries(t, path)
	if len(entries) == 0 {
		t.Fatalf("icacls reported no ACL entries for %q", path)
	}
	for _, entry := range entries {
		if !strings.Contains(entry, current.Username) {
			t.Fatalf("%q grants access to %q, want the current user only", path, entry)
		}
	}
}

func TestSaveWritesAUserOnlyACL(t *testing.T) {
	store, _ := newTestStore(t)

	if _, err := store.Save(context.Background(), testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	assertUserOnlyACL(t, store.recordPath(testPrincipal))
}

func TestExportLegacyTokenFileWritesAUserOnlyACL(t *testing.T) {
	dir := tempDir(t)

	path, err := ExportLegacyTokenFile(dir, newTestTokens())
	if err != nil {
		t.Fatalf("ExportLegacyTokenFile: %v", err)
	}
	assertUserOnlyACL(t, path)
}
