//go:build unix

package cmd

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// repairPermsInternalStateDir mirrors the black-box helper of the same
// family (see repairperms_test.go), but lives in this file's own internal
// test package.
func repairPermsInternalStateDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the temporary directory: %v", err)
	}
	return resolved
}

// TestOwnershipMismatch is the pure comparison inspectTarget relies on to
// classify a target as foreign-owned, exercised directly with a synthesized
// uid: no test in this package can genuinely chown a file to another local
// account without privilege this test does not have, so — the same as
// internal/securefile's own checkStatOwner test — the comparison is driven
// with a constructed syscall.Stat_t instead of a real foreign file.
func TestOwnershipMismatch(t *testing.T) {
	if owner, mismatched := ownershipMismatch(&syscall.Stat_t{Uid: 65534}, 1000); !mismatched || owner != 65534 {
		t.Fatalf("ownershipMismatch(uid=65534, want=1000) = (%d, %v), want (65534, true)", owner, mismatched)
	}
	if _, mismatched := ownershipMismatch(&syscall.Stat_t{Uid: 1000}, 1000); mismatched {
		t.Fatal("ownershipMismatch(uid=1000, want=1000) reported a mismatch for a matching owner")
	}
}

// TestInspectTargetRefusesAForeignOwnedFileWithoutTouchingIt is the
// "unfixable" case end to end through inspectTarget and repairOne: the file
// really exists, is really owned by the invoking process, and really has a
// widened mode, but is inspected against an injected euid that does not
// match. repairOne must classify it foreign-owned and MUST NOT call
// applyFix — proven not by a mock but by re-reading the file's mode
// afterwards and requiring it to be exactly what was planted.
//
// The injected euid is the seam: inspectTarget and repairOne both take euid
// as an explicit parameter rather than reading os.Geteuid() themselves,
// specifically so this test can exercise the foreign-owner path without the
// privilege a real chown to another account would need.
func TestInspectTargetRefusesAForeignOwnedFileWithoutTouchingIt(t *testing.T) {
	dir := repairPermsInternalStateDir(t)
	path := filepath.Join(dir, "key-v1.json")
	if err := os.WriteFile(path, []byte(`{"version":"1","key":"x"}`), 0o644); err != nil {
		t.Fatalf("seed %q: %v", path, err)
	}

	target := repairTarget{path: path, mode: 0o600}
	notMyEUID := currentEUID() + 1

	f := inspectTarget(target, notMyEUID)
	if f.problem != problemForeignOwner {
		t.Fatalf("inspectTarget with a mismatched euid: problem = %v, want problemForeignOwner", f.problem)
	}
	if f.fixable() {
		t.Fatal("a problemForeignOwner finding reported fixable() = true, want false")
	}
	if !f.unresolved() {
		t.Fatal("a problemForeignOwner finding reported unresolved() = false, want true")
	}

	result := repairOne(f, false, false)
	if result.fixed || result.fixErr != nil {
		t.Fatalf("repairOne on a foreign-owned target: fixed=%v fixErr=%v, want neither", result.fixed, result.fixErr)
	}
	if got := mustModeInternal(t, path); got != 0o644 {
		t.Fatalf("mode after repairOne on a foreign-owned target = %v, want unchanged 0644", got)
	}
	if !repairLeftSomethingUnresolved(false, []repairResult{result}) {
		t.Fatal("repairLeftSomethingUnresolved did not flag a foreign-owned finding")
	}
}

// TestInspectTargetAcceptsAMatchingEUID is the companion: the same file,
// inspected with the correct euid, is fixable and gets fixed.
func TestInspectTargetAcceptsAMatchingEUID(t *testing.T) {
	dir := repairPermsInternalStateDir(t)
	path := filepath.Join(dir, "key-v1.json")
	if err := os.WriteFile(path, []byte(`{"version":"1","key":"x"}`), 0o644); err != nil {
		t.Fatalf("seed %q: %v", path, err)
	}

	target := repairTarget{path: path, mode: 0o600}
	f := inspectTarget(target, currentEUID())
	result := repairOne(f, false, false)
	if !result.fixed || result.fixErr != nil {
		t.Fatalf("repairOne on an owned, fixable target: fixed=%v fixErr=%v, want fixed=true, fixErr=nil",
			result.fixed, result.fixErr)
	}
	if got := mustModeInternal(t, path); got != 0o600 {
		t.Fatalf("mode after repairOne on an owned target = %v, want 0600", got)
	}
	if repairLeftSomethingUnresolved(false, []repairResult{result}) {
		t.Fatal("repairLeftSomethingUnresolved flagged a successfully fixed finding")
	}
}

// TestRepairLeftSomethingUnresolvedTreatsDryRunAsUnresolved: a fixable
// finding under --dry-run is never touched, so it must still count as
// unresolved even though inspectTarget alone cannot tell dry-run apart from
// a real run.
func TestRepairLeftSomethingUnresolvedTreatsDryRunAsUnresolved(t *testing.T) {
	f := finding{target: repairTarget{path: "/irrelevant"}, problem: problemWrongMode}
	if !repairLeftSomethingUnresolved(true, []repairResult{{finding: f}}) {
		t.Fatal("a fixable finding under dry-run was not flagged as unresolved")
	}
}

func mustModeInternal(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	return info.Mode().Perm()
}
