//go:build windows

package securefile

import (
	"fmt"
	"io/fs"
	"os"
)

// Windows has no POSIX modes: os.Stat reports a synthesized 0666 or 0444 that says
// nothing about who may read the file, and os.Chmod only toggles the read-only
// attribute. Owner-only therefore means an explicit access control list, applied on
// every write and inspected on every read.
//
// The requested fs.FileMode is ignored here on purpose. Windows has no equivalent
// of 0600 versus 0400, and the caller's intent in both cases is the same: nobody
// except this account.

// nonBlockingFlag is zero: Windows has no O_NONBLOCK for a file open, and a named
// pipe cannot be planted at an arbitrary filesystem path the way a FIFO can. The
// regular-file check in dir.readFile still applies.
const nonBlockingFlag = 0

// restrictFile makes the freshly created file owner-only.
func restrictFile(_ *os.File, path string, _ fs.FileMode) error {
	return applyOwnerOnlyACL(path)
}

// restrictDir makes the directory owner-only, the equivalent of mode 0700.
func restrictDir(_ *os.Root, path string, _ fs.FileMode) error {
	return applyOwnerOnlyACL(path)
}

// checkOwnerOnly refuses a file whose access control list grants anything to anyone
// but this account.
//
// The descriptor is read through the open handle, so the answer describes the object
// that was actually opened. A file created by another account, one that inherited
// entries from a permissive parent directory, and one that was widened after
// creation are all refused: the list written at creation time is not evidence about
// the file that is on disk now.
func checkOwnerOnly(file *os.File, path string) error {
	self, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("securefile: check %q: %w: %w", path, ErrInsecurePermissions, err)
	}
	control, err := readAccessControl(file)
	if err != nil {
		return fmt.Errorf("securefile: check %q: %w: %w", path, ErrInsecurePermissions, err)
	}
	return checkOwnerOnlyAccess(control, self, path)
}
