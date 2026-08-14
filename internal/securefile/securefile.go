// Package securefile reads and writes secret-bearing files without letting a
// hostile filesystem redirect, widen or truncate them.
//
// It is the single home for the hardening that internal/store and
// internal/cryptostore both need: owner-only permissions, refusal of symlinks and
// of anything that is not a regular file, bounded reads, and an atomic write.
//
// # Why every operation is relative to a verified directory
//
// Validating a path and then opening or renaming it by pathname is a
// time-of-check to time-of-use race: an attacker who can swap one ancestor
// directory redirects the operation, and O_NOFOLLOW on the final component does
// not see it. Every function here therefore walks the path once, component by
// component, and ends up holding an *os.Root — a directory file descriptor — for
// the parent. All further work (open, create, rename, link, chmod, remove) is
// relative to that descriptor, so a later rename of any ancestor cannot move the
// target: the descriptor still names the directory that was verified.
//
// Each component is verified before it is used and again after it is opened: an
// Lstat must report a real directory rather than a symlink, and the opened
// descriptor must be the same object (os.SameFile) as the one that was verified.
// A swap between the two observations changes the identity and is refused, so the
// check cannot be outrun.
//
// # Regular files only
//
// A read opens non-blocking and requires a regular file both before and after the
// open. A FIFO, a device or a socket planted at the path is refused rather than
// waited on: a mode 0600 FIFO with no writer would otherwise hang start-up.
//
// # Permissions
//
// On Unix a file must carry no group or other permission bit, and the mode is
// enforced with an explicit chmod after creation, because the mode passed to open
// is masked by the process umask, which is global and may be hostile.
//
// On Windows there are no POSIX modes, so owner-only means an explicit access
// control list: the current user owns the object, inheritance is switched off, and
// no access-allowed entry names any other principal. Both the write side and the
// read side use that definition; see acl.go.
package securefile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Sentinel errors. Every failure is comparable with errors.Is, because the
// calling packages translate them onto their own sentinels.
var (
	// ErrInsecurePath means the path, or one of its ancestors, is a symlink, is
	// not the kind of object it must be (a directory in the ancestry, a regular
	// file at the end), or cannot be verified. An uncheckable path is not a safe
	// path.
	ErrInsecurePath = errors.New("securefile: path is not safe")

	// ErrInsecurePermissions means the file grants access beyond its owner: a
	// group or other permission bit on Unix, or a foreign, inherited or
	// unrecognized access control entry on Windows.
	ErrInsecurePermissions = errors.New("securefile: file is not owner-only")

	// ErrNotFound means the file, or a directory in its path, does not exist. It
	// wraps fs.ErrNotExist, so a caller may test for either.
	ErrNotFound = fmt.Errorf("securefile: file does not exist: %w", fs.ErrNotExist)

	// ErrExists means InstallNewFile found the target already in place. The
	// content that is already there is the winner and is left untouched.
	ErrExists = fmt.Errorf("securefile: file already exists: %w", fs.ErrExist)
)

// ReadFile returns the content of path, at most limit bytes.
//
// It refuses a symlink, a directory, a FIFO, a device, a socket, a file any other
// local account can reach, and content over limit. An absent file or directory
// reports ErrNotFound.
func ReadFile(path string, limit int64) ([]byte, error) {
	parent, name, err := openParent(path)
	if err != nil {
		return nil, err
	}
	defer parent.close()
	return parent.readFile(name, limit)
}

// WriteFile writes content to path atomically with mode mode.
//
// The content lands in a fresh temporary sibling, which is synced and then
// renamed over path, so a concurrent reader sees either the previous content or
// the new content and never a truncated file. An existing path that is not a
// regular file — in particular a planted symlink — is refused rather than
// replaced.
func WriteFile(path string, content []byte, mode fs.FileMode) error {
	parent, name, err := openParent(path)
	if err != nil {
		return err
	}
	defer parent.close()
	return parent.writeFile(name, content, mode)
}

// InstallNewFile writes content to path only if nothing is there yet, and reports
// ErrExists otherwise.
//
// The installation is atomic and does not replace: the content is written and
// synced into a temporary sibling and then hard-linked into place, which fails
// when the name is taken. Two processes that create the same file therefore agree
// on one winner, and the loser must discard its material and read the winner's.
func InstallNewFile(path string, content []byte, mode fs.FileMode) error {
	parent, name, err := openParent(path)
	if err != nil {
		return err
	}
	defer parent.close()
	return parent.installNewFile(name, content, mode)
}

// RestrictExisting enforces mode on a file this package did not create.
//
// It exists for files a third-party library opens for itself — the SQLite
// database, its write-ahead log and its shared-memory file — where the creation
// mode was masked by the process umask. Changing the umask is not an option: it is
// process-global mutable state, and a library that forks a helper would inherit
// it. Tightening the file right after it appears is.
//
// The path is verified component by component like every other operation here, the
// object must be a regular file whose identity does not change across the open,
// and the chmod is applied to the open descriptor, so a symlink planted at the path
// is refused rather than followed. An absent file reports ErrNotFound, which a
// caller may ignore for a log file SQLite has not created yet.
func RestrictExisting(path string, mode fs.FileMode) error {
	parent, name, err := openParent(path)
	if err != nil {
		return err
	}
	defer parent.close()
	return parent.restrictExisting(name, mode)
}

// Remove deletes path. An absent path, and an absent parent directory, are not
// errors.
func Remove(path string) error {
	parent, name, err := openParent(path)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.close()
	return parent.remove(name)
}

// EnsureDir creates dir and every missing parent, then enforces mode on dir
// itself.
//
// Only the final directory's mode is enforced: an intermediate directory may be
// shared with unrelated content, and tightening it is not this package's
// business. The enforcement happens through the verified directory descriptor,
// after the object's type and identity are confirmed, so no chmod can land on a
// symlink target.
func EnsureDir(dir string, mode fs.FileMode) error {
	handle, err := openDirTree(dir, mode)
	if err != nil {
		return err
	}
	defer handle.close()
	return handle.restrict(mode)
}

// CheckAncestry reports whether path and every existing ancestor of it is free of
// symlinks.
//
// It is a pre-flight check for a configured path that need not exist yet, so a
// misconfiguration is reported once at start-up rather than at the first write.
// It is not the safety mechanism: the operations above do not trust it, they
// re-verify every component against a directory descriptor.
func CheckAncestry(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("securefile: empty path: %w", ErrInsecurePath)
	}

	for current := filepath.Clean(path); ; {
		if err := checkAncestryComponent(path, current); err != nil {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func checkAncestryComponent(path, current string) error {
	info, err := os.Lstat(current)
	switch {
	case err == nil && info.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("securefile: path %q has a symlinked component %q: %w",
			path, current, ErrInsecurePath)
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("securefile: path %q component %q cannot be checked: %w",
			path, current, ErrInsecurePath)
	}
	return nil
}

// readBounded reads at most limit bytes and fails if there is more, so a
// pathological file cannot be loaded into memory in full.
func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("content exceeds %d bytes", limit)
	}
	return raw, nil
}
