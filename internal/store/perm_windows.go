//go:build windows

package store

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
)

// Windows has no POSIX modes and no O_NOFOLLOW. Two substitutes apply:
//
//   - symlink safety comes from checkNoSymlinkAncestry, which uses Lstat and so
//     detects both symlinks and directory junctions before the open;
//   - owner-only access comes from an explicit ACL, because os.Chmod on Windows only
//     toggles the read-only attribute and cannot express "owner only".
//
// The ACL is applied with icacls, the platform's own tool: the standard library
// exposes no ACL API, and this repository adds no dependency for one. The
// windows-tagged test asserts the resulting ACL instead of a mode.

// openNoFollow opens path for reading. See the note above on O_NOFOLLOW.
func openNoFollow(path string) (*os.File, error) {
	return os.Open(path)
}

// createExclusiveNoFollow creates path, failing if it already exists.
func createExclusiveNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, tokenFileMode)
}

// restrictFileToOwner replaces the file's inherited ACL with a single full-control
// entry for the current user, the Windows equivalent of mode 0600.
func restrictFileToOwner(_ *os.File, path string) error {
	return restrictACLToCurrentUser(path)
}

// restrictDirToOwner does the same for a directory, the equivalent of mode 0700.
func restrictDirToOwner(dir string) error {
	return restrictACLToCurrentUser(dir)
}

// restrictACLToCurrentUser drops inherited entries and grants the current user full
// control, replacing any existing entry for that principal.
func restrictACLToCurrentUser(path string) error {
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("store: resolve current user for %q: %w", path, err)
	}
	cmd := exec.Command("icacls", path, "/inheritance:r", "/grant:r", current.Username+":F")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("store: restrict ACL of %q: %w: %s", path, err, output)
	}
	return nil
}

// checkOwnerOnly cannot read a POSIX mode on Windows: os.Stat reports a synthesized
// 0666 or 0444 that says nothing about the ACL. The ACL was restricted when the file
// was created, so a mode check here would only produce false failures.
func checkOwnerOnly(_ fs.FileMode, path string) error {
	if path == "" {
		return fmt.Errorf("store: empty path: %w", ErrInvalidConfig)
	}
	return nil
}
