//go:build unix

package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"github.com/tamcore/garmin-mcp/internal/securefile"
)

// problem classifies what, if anything, is wrong with one repairTarget.
type problem int

const (
	// problemNone means the target already carries exactly the required mode
	// and owner, or does not need one because it does not exist.
	problemNone problem = iota
	// problemAbsent means the target does not exist. A fresh deployment has no
	// key, no token record, and no database, so this is not reported as a
	// failure.
	problemAbsent
	// problemWrongType means a symlink, or an object of the wrong kind (a file
	// where a directory belongs, or the reverse), sits at the target's path.
	// It is never touched: this command repairs a mode, not a filesystem
	// layout, and a symlink there is exactly the kind of redirection
	// internal/securefile's own discipline exists to refuse.
	problemWrongType
	// problemForeignOwner means the target is owned by a local account other
	// than this process's effective user. It is never touched: an
	// unprivileged process cannot legitimately fix another account's file, and
	// guessing at one would extend trust to an object this process does not
	// control.
	problemForeignOwner
	// problemWrongMode means the target is owned by this process and carries a
	// mode broader than required. This is the one problem repair-permissions
	// can actually fix.
	problemWrongMode
	// problemInspectFailed means the target could not be classified at all —
	// an Lstat failure other than "does not exist", or an owner this platform
	// cannot report.
	problemInspectFailed
	// problemEnumerationFailed means a directory this command must list to
	// discover further targets (the key directory, the token records
	// directory) could not be listed, even after the one-shot recovery
	// resolveScan attempts. Treating this as "empty" — the bug item 2 in
	// AGENTS.md's review names — would hide every file inside it from the
	// rest of this command, and from its own exit status.
	problemEnumerationFailed
)

// groupOrOtherAccessBits restates securefile's own unexported
// groupAndOtherBits (perm_unix.go in internal/securefile): the permission
// bits that make a file reachable by an account other than its owner. It is
// the ceiling repairTarget.noGroupOrOther compares against, for material
// this command does not own the shape of and so cannot chmod to one exact
// value — see clientSecretHashTargets.
const groupOrOtherAccessBits fs.FileMode = 0o077

// finding is the outcome of inspecting one repairTarget, before any fix is
// attempted.
type finding struct {
	target  repairTarget
	problem problem
	gotMode fs.FileMode
	detail  string
}

// fixable reports whether applyFix may legitimately be attempted for this
// finding. Every other problem — including problemNone and problemAbsent — is
// never passed to applyFix.
func (f finding) fixable() bool {
	return f.problem == problemWrongMode && !f.target.noGroupOrOther
}

// unresolved reports whether this finding, on its own, must make the command
// report failure: everything except a clean pass or a legitimately absent
// target.
func (f finding) unresolved() bool {
	return f.problem != problemNone && f.problem != problemAbsent
}

// inspectTarget reports target's state without ever opening it: os.Lstat is
// the only syscall this function makes, and it never follows a symlink.
//
// euid is the identity a target must be owned by to be legitimately fixable —
// this process's real effective uid in production, injected explicitly here
// so a test can exercise the foreign-owner refusal without the privilege a
// real chown to another account would need. internal/securefile makes the
// same comparison internally (checkStatOwner in perm_unix.go) but exposes no
// seam for it, and this command must decide fixable-or-not BEFORE it ever
// calls into securefile, so the comparison is repeated here as its own pure,
// injectable function: see ownershipMismatch.
func inspectTarget(target repairTarget, euid uint32) finding {
	info, err := os.Lstat(target.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return finding{target: target, problem: problemAbsent}
	case err != nil:
		return finding{target: target, problem: problemInspectFailed, detail: statErrorDetail(err)}
	}

	if wrong, detail := wrongType(target, info); wrong {
		return finding{target: target, problem: problemWrongType, detail: detail}
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return finding{target: target, problem: problemInspectFailed, detail: "cannot determine owner"}
	}
	if owner, mismatched := ownershipMismatch(stat, euid); mismatched {
		return finding{target: target, problem: problemForeignOwner,
			detail: fmt.Sprintf("owned by uid %d, not this process's effective uid %d", owner, euid)}
	}

	gotMode := info.Mode().Perm()
	if target.noGroupOrOther {
		if gotMode&groupOrOtherAccessBits == 0 {
			return finding{target: target, problem: problemNone, gotMode: gotMode}
		}
		return finding{target: target, problem: problemWrongMode, gotMode: gotMode,
			detail: fmt.Sprintf(
				"mode %04o grants group or other access; this command never guesses a "+
					"replacement mode for operator-supplied material, so it is reported, not fixed",
				gotMode)}
	}
	if gotMode == target.mode {
		return finding{target: target, problem: problemNone, gotMode: gotMode}
	}
	return finding{target: target, problem: problemWrongMode, gotMode: gotMode,
		detail: fmt.Sprintf("mode %04o, want %04o", gotMode, target.mode)}
}

// wrongType reports whether info is not the kind of object target declares:
// a symlink anywhere, a non-directory where a directory belongs, or a
// non-regular file (a FIFO, a device, a socket) where a file belongs.
func wrongType(target repairTarget, info fs.FileInfo) (bool, string) {
	if info.Mode()&fs.ModeSymlink != 0 {
		return true, "is a symlink; refusing to inspect or change it further"
	}
	if target.isDir && !info.IsDir() {
		return true, "expected a directory, found a different object type"
	}
	if !target.isDir && !info.Mode().IsRegular() {
		return true, fmt.Sprintf("expected a regular file, found type %v", info.Mode().Type())
	}
	return false, ""
}

// ownershipMismatch reports whether stat names a uid other than want.
func ownershipMismatch(stat *syscall.Stat_t, want uint32) (owner uint32, mismatched bool) {
	return stat.Uid, stat.Uid != want
}

// statErrorDetail reports an Lstat failure without repeating the error's own
// text: some failures on some platforms can quote more of the surrounding
// path than this report should carry, and the path is already named by the
// caller.
func statErrorDetail(err error) string {
	if errors.Is(err, os.ErrPermission) {
		return "cannot inspect: permission denied"
	}
	return "cannot inspect: unreadable"
}

// applyFix tightens f's target to its required mode through
// internal/securefile, which supplies the symlink-refusing,
// identity-confirming discipline every other write in this codebase already
// depends on: an Lstat, a non-blocking open, a post-open identity
// confirmation, an owner check against the real os.Geteuid(), and only then
// the chmod, applied through the open descriptor rather than by path.
//
// It is called only for a finding whose problem is problemWrongMode: a
// foreign owner or the wrong object type is refused before a finding ever
// reaches this function (see finding.fixable), so applyFix is never the
// first thing to touch an object this process does not legitimately own.
// Neither securefile.RestrictExisting nor RestrictExistingDir reads a byte of
// the target's content — each opens it only to fstat and chmod the
// descriptor — so no target this command tightens is ever loaded.
func applyFix(f finding) error {
	if f.target.isDir {
		return securefile.RestrictExistingDir(f.target.path, f.target.mode)
	}
	return securefile.RestrictExisting(f.target.path, f.target.mode)
}

// currentEUID reports this process's real effective uid, the identity a
// production run requires every fixable target to be owned by.
func currentEUID() uint32 {
	return uint32(os.Geteuid())
}

// resolveScan reports the targets discovered inside scan.target.path, or an
// unresolved problemEnumerationFailed finding when its contents cannot be
// read even after one recovery attempt.
//
// dirFinding is this same directory's own, already-computed finding from
// phase 1 (see runRepairPermissions) — inspectTarget has already confirmed,
// moments earlier, that this is a real directory this process owns, so the
// recovery below is never a blind chmod of an unverified path.
//
// The recovery is a deliberate, narrow exception to the inspect-everything-
// then-fix-everything rule the rest of this command follows (see item 6 in
// AGENTS.md's review): a directory this command cannot read hides every
// target inside it, so there is no way to even report the rest of the
// deployment's state — let alone decide whether anything else is
// structurally unresolved — until the directory itself can be listed. It
// never runs in --dry-run, and never runs when dirFinding is not itself a
// fixable wrong-mode problem (a foreign owner or the wrong object type is
// refused exactly as everywhere else). fixedEarly reports whether this
// function is the one that tightened the directory, so the caller can fold
// that outcome into its report instead of re-inspecting a directory that
// will, by then, already look clean.
//
// The first attempt goes through applyFix, the same descriptor-based,
// identity-confirming chmod every other fix in this command uses. That is
// not enough on its own for the exact case item 2 names: a mode of 0300
// (write and execute, no read) cannot be opened at all — Go's os.Root, like
// opendir(3), needs read access to obtain a directory descriptor — so
// applyFix itself fails with a permission error before it ever reaches the
// chmod. There is no descriptor-based way to recover read access to a
// directory that cannot be opened for reading. The fallback is a single,
// narrowly-scoped pathname chmod (os.Chmod, not through a verified
// descriptor), applied only to the exact path inspectTarget already
// confirmed is this process's own directory a moment ago, and its result is
// never trusted on its own: applyFix is called again immediately afterward,
// through the normal secure, descriptor-based path, which will simply find
// the mode already correct (a no-op) or, if the pathname chmod landed on
// something that changed identity in between, fail closed the same way it
// always does. This narrows, but does not eliminate, the same
// time-of-check-to-time-of-use gap internal/securefile's package doc warns
// about — accepted here, and only here, because there is no way to reopen a
// directory this process has locked itself out of otherwise.
func resolveScan(
	scan dirScan, dirFinding finding, dryRun bool,
) (extra []repairTarget, failure *finding, fixedEarly bool) {
	entries, err := os.ReadDir(scan.target.path)
	if err == nil {
		return matchEntries(scan.target.path, entries, scan.pattern, scan.fileMode), nil, false
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, false
	}
	if dryRun || !dirFinding.fixable() {
		return nil, enumerationFailureFinding(scan.target.path, err), false
	}

	fixed := applyFix(dirFinding) == nil
	if !fixed && os.Chmod(scan.target.path, scan.target.mode) == nil {
		fixed = applyFix(dirFinding) == nil
	}

	retried, retryErr := os.ReadDir(scan.target.path)
	if retryErr == nil {
		return matchEntries(scan.target.path, retried, scan.pattern, scan.fileMode), nil, fixed
	}
	return nil, enumerationFailureFinding(scan.target.path, retryErr), fixed
}

// enumerationFailureFinding reports scan.target.path as unresolved because
// its contents could not be enumerated.
func enumerationFailureFinding(path string, cause error) *finding {
	return &finding{
		target:  repairTarget{path: path, isDir: true},
		problem: problemEnumerationFailed,
		detail:  enumerationErrorDetail(cause),
	}
}

// enumerationErrorDetail reports a ReadDir failure without repeating the
// error's own text, matching statErrorDetail's discipline for the same
// reason.
func enumerationErrorDetail(err error) string {
	if errors.Is(err, os.ErrPermission) {
		return "cannot enumerate: permission denied"
	}
	return "cannot enumerate: unreadable"
}
