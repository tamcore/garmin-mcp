//go:build unix

package cryptostore

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// groupAndOtherBits are the permission bits no key file may carry. Key material
// another local account can read is treated as compromised.
const groupAndOtherBits = fs.FileMode(0o077)

// openNoFollow opens path for reading and refuses to traverse a final symlink.
// The full ancestry is checked separately by checkNoSymlinkAncestry, because
// O_NOFOLLOW covers the last component only.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}

// createExclusiveNoFollow creates path, failing if it already exists or is a
// symlink. O_EXCL turns a pre-planted target into an error rather than a
// redirect.
func createExclusiveNoFollow(path string, mode fs.FileMode) (*os.File, error) {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL | syscall.O_NOFOLLOW
	return os.OpenFile(path, flags, mode)
}

// restrictToOwner enforces mode on an open file. The mode passed to open is
// masked by the process umask, which is global to the process and may be
// hostile, so this chmod is not redundant.
func restrictToOwner(file *os.File, path string, mode fs.FileMode) error {
	if err := file.Chmod(mode); err != nil {
		return fmt.Errorf("secure file %q: %w", path, err)
	}
	return nil
}

// checkOwnerOnly refuses a file that any group or other principal can reach.
func checkOwnerOnly(mode fs.FileMode, path string, sentinel error) error {
	if perm := mode.Perm(); perm&groupAndOtherBits != 0 {
		return fmt.Errorf("cryptostore: file %q has mode %04o, want owner-only: %w", path, perm, sentinel)
	}
	return nil
}
