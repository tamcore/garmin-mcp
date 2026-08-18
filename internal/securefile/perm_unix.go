//go:build unix

package securefile

import (
	"errors"
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
		return wrapChmodDirError(path, err)
	}
	return nil
}

// wrapChmodDirError names the remedy when a directory chmod is refused for
// permission, because "chmodat: operation not permitted" does not obviously
// mean "point this setting at a subdirectory instead." fs.ErrPermission matches
// both EACCES and EPERM, and EPERM can also mean an immutable flag, so the
// message gives the remedy as typical guidance, not an asserted cause. Any
// other chmod failure is reported plainly, with no remedy text attached.
func wrapChmodDirError(path string, err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("securefile: secure directory %q: cannot set owner-only permissions, "+
			"most commonly because this process does not own the directory, which is what a volume "+
			"mount root typically is: point the setting at a subdirectory of the mount instead and "+
			"let it be created: %w", path, err)
	}
	return fmt.Errorf("securefile: secure directory %q: %w", path, err)
}

// checkOwnerOnly refuses a file any group or other principal can reach, or one
// owned by an account other than this process's effective user. The mode and
// owner both come from the open descriptor, not from the path, so they
// describe the object that was actually opened.
func checkOwnerOnly(file *os.File, path string) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("securefile: inspect %q: %w: %w", path, ErrInsecurePermissions, err)
	}
	if perm := info.Mode().Perm(); perm&groupAndOtherBits != 0 {
		return wrapInsecureFileModeError(path, perm)
	}
	return checkOwner(file, path)
}

// wrapInsecureFileModeError names the likely cause of a group- or
// other-accessible secret file, the way wrapChmodDirError names its own for a
// directory chmod refusal: a Kubernetes fsGroup mount recursion applies group
// permissions on every mount rather than only when a volume's ownership
// changed, and that widens a previously owner-only 0600 file to 0660. This is
// typical guidance, not an asserted cause — any process able to chmod the file
// could have widened it — and the message leaks nothing but the path and mode.
func wrapInsecureFileModeError(path string, perm fs.FileMode) error {
	return fmt.Errorf("securefile: %q has mode %04o, want owner-only, most commonly because an fsGroup "+
		"mount recursion widened it on a remount: %w", path, perm, ErrInsecurePermissions)
}

// checkOwner refuses a file owned by an account other than this process's
// effective user — an actor other than the expected owner controlling or
// observing the content even though the permission bits alone look correct.
// It is split out from checkOwnerOnly so restrictExisting can run it on its
// own: the file restrictExisting is about to tighten may legitimately still
// carry the wrong mode bits (that is exactly what it exists to fix), but must
// never be owned by someone else, both before and after the chmod.
func checkOwner(file *os.File, path string) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("securefile: inspect %q: %w: %w", path, ErrInsecurePermissions, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("securefile: %q: cannot determine owner: %w", path, ErrInsecurePermissions)
	}
	// Effective uid, not real uid: POSIX ties fchmod/fchown authority to the
	// effective uid, which is the identity that matters for "does this process
	// control the file".
	return checkStatOwner(path, stat, os.Geteuid())
}

// checkStatOwner is the pure comparison behind checkOwner, split out so it can
// be driven directly with a synthesized syscall.Stat_t: constructing a file
// this process does not own needs privilege (chown) an unprivileged test does
// not have.
func checkStatOwner(path string, stat *syscall.Stat_t, euid int) error {
	if stat.Uid != uint32(euid) {
		return fmt.Errorf("securefile: %q is owned by uid %d, not this process's effective uid %d: %w",
			path, stat.Uid, euid, ErrInsecurePermissions)
	}
	return nil
}

// checkDirOwner refuses a directory owned by an account other than this
// process's effective user.
func checkDirOwner(info fs.FileInfo, path string) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("securefile: %q: cannot determine owner: %w", path, ErrInsecurePermissions)
	}
	if euid := os.Geteuid(); stat.Uid != uint32(euid) {
		return fmt.Errorf("securefile: %q is owned by uid %d, not this process's effective uid %d: %w",
			path, stat.Uid, euid, ErrInsecurePermissions)
	}
	return nil
}
