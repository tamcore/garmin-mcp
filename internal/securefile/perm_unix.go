//go:build unix

package securefile

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// groupAndOtherBits are the permission bits no secret-bearing file may carry.
// Material another local account can read is treated as compromised.
const groupAndOtherBits = fs.FileMode(0o077)

// nonBlockingFlag keeps an open from waiting. A read must never block on the object
// at the path: a mode 0600 FIFO with no writer would otherwise hang the process
// until someone writes to it. The type is rejected right afterwards; this flag only
// makes sure the rejection is reached.
const nonBlockingFlag = syscall.O_NONBLOCK

// restrictFile enforces mode on an open file. The mode passed to open is masked by
// the process umask, which is global to the process and may be hostile, so this
// chmod is not redundant. It works on the descriptor, so it cannot be redirected.
func restrictFile(file *os.File, path string, mode fs.FileMode) error {
	if err := file.Chmod(mode); err != nil {
		return fmt.Errorf("securefile: secure %q: %w", path, err)
	}
	return nil
}

// restrictDir enforces mode on the directory the descriptor holds. The mode passed
// to mkdir is masked by the umask and is not applied at all to a directory that
// already exists, so this is not redundant either.
func restrictDir(root *os.Root, path string, mode fs.FileMode) error {
	if err := root.Chmod(".", mode); err != nil {
		return fmt.Errorf("securefile: secure directory %q: %w", path, err)
	}
	return nil
}

// checkOwnerOnly refuses a file any group or other principal can reach. The mode
// comes from the open descriptor, not from the path, so it describes the object
// that was actually opened.
func checkOwnerOnly(file *os.File, path string) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("securefile: inspect %q: %w: %w", path, ErrInsecurePermissions, err)
	}
	if perm := info.Mode().Perm(); perm&groupAndOtherBits != 0 {
		return fmt.Errorf("securefile: %q has mode %04o, want owner-only: %w",
			path, perm, ErrInsecurePermissions)
	}
	return nil
}
