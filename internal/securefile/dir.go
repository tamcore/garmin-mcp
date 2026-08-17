package securefile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// dir is a verified directory: an *os.Root, which is a directory file descriptor,
// plus the path it was reached through for diagnostics. Every operation on a dir
// is relative to that descriptor, so no later change to the path can redirect it.
//
// Each exported function owns its handle for the length of one operation and
// closes it again, so nothing here is shared state.
type dir struct {
	root *os.Root
	path string
}

func (d *dir) close() {
	if d != nil && d.root != nil {
		_ = d.root.Close()
	}
}

// openParent returns a verified handle on the directory holding path, plus the
// final component. It is the entry point of every file operation.
func openParent(path string) (*dir, string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, "", fmt.Errorf("securefile: empty path: %w", ErrInsecurePath)
	}

	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return nil, "", fmt.Errorf("securefile: resolve %q: %w", path, ErrInsecurePath)
	}
	parent, name := filepath.Dir(absolute), filepath.Base(absolute)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return nil, "", fmt.Errorf("securefile: %q names no file: %w", path, ErrInsecurePath)
	}

	handle, err := openDirTree(parent, 0)
	if err != nil {
		return nil, "", err
	}
	return handle, name, nil
}

// openDirTree walks path from the volume root and returns a verified handle on it.
// A non-zero createMode creates every missing component with that mode; zero
// requires the whole path to exist already.
func openDirTree(path string, createMode fs.FileMode) (*dir, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("securefile: empty directory: %w", ErrInsecurePath)
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("securefile: resolve %q: %w", path, ErrInsecurePath)
	}

	base, names := splitPath(absolute)
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, fmt.Errorf("securefile: open %q: %w: %w", base, ErrInsecurePath, err)
	}

	current := &dir{root: root, path: base}
	for _, name := range names {
		next, childErr := current.childDir(name, createMode)
		current.close()
		if childErr != nil {
			return nil, childErr
		}
		current = next
	}
	return current, nil
}

// splitPath separates an absolute path into the volume root, which cannot be a
// symlink, and the components below it.
func splitPath(absolute string) (base string, names []string) {
	volume := filepath.VolumeName(absolute)
	base = volume + string(filepath.Separator)

	for name := range strings.SplitSeq(absolute[len(volume):], string(filepath.Separator)) {
		if name != "" && name != "." {
			names = append(names, name)
		}
	}
	return base, names
}

// childDir opens name below d as a verified directory. The Lstat proves the name
// is a real directory rather than a symlink, and os.SameFile proves the opened
// descriptor is that same directory, so a swap between the two observations is
// refused instead of followed.
func (d *dir) childDir(name string, createMode fs.FileMode) (*dir, error) {
	expected, err := d.statDirEntry(name, createMode)
	if err != nil {
		return nil, err
	}

	root, err := d.root.OpenRoot(name)
	if err != nil {
		return nil, d.pathError("open directory", name, err)
	}
	opened, err := root.Stat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(expected, opened) {
		_ = root.Close()
		return nil, fmt.Errorf("securefile: directory %q changed identity while it was opened: %w",
			filepath.Join(d.path, name), ErrInsecurePath)
	}
	return &dir{root: root, path: filepath.Join(d.path, name)}, nil
}

// statDirEntry reports the entry name must resolve to, creating it with createMode
// when it is absent and createMode is non-zero.
func (d *dir) statDirEntry(name string, createMode fs.FileMode) (fs.FileInfo, error) {
	info, err := d.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) && createMode != 0 {
		if mkErr := d.root.Mkdir(name, createMode); mkErr != nil && !errors.Is(mkErr, fs.ErrExist) {
			return nil, d.pathError("create directory", name, mkErr)
		}
		info, err = d.root.Lstat(name)
	}
	if err != nil {
		return nil, d.pathError("inspect", name, err)
	}

	full := filepath.Join(d.path, name)
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("securefile: %q is a symlink: %w", full, ErrInsecurePath)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("securefile: %q is not a directory: %w", full, ErrInsecurePath)
	}
	return info, nil
}

// restrict enforces mode on the directory itself, through the descriptor. The type
// was verified when the descriptor was opened and is confirmed again here, so this
// cannot land on a symlink target.
func (d *dir) restrict(mode fs.FileMode) error {
	info, err := d.root.Stat(".")
	if err != nil || !info.IsDir() {
		return fmt.Errorf("securefile: %q is not a usable directory: %w", d.path, ErrInsecurePath)
	}
	return restrictDir(d.root, d.path, mode)
}

// readFile reads name relative to d. The Lstat, the non-blocking open and the
// post-open Fstat together guarantee the bytes come from the regular file that was
// verified.
func (d *dir) readFile(name string, limit int64) ([]byte, error) {
	expected, err := d.root.Lstat(name)
	if err != nil {
		return nil, d.pathError("inspect", name, err)
	}
	if !expected.Mode().IsRegular() {
		return nil, fmt.Errorf("securefile: %q is not a regular file (type %v): %w",
			filepath.Join(d.path, name), expected.Mode().Type(), ErrInsecurePath)
	}

	file, err := d.root.OpenFile(name, os.O_RDONLY|nonBlockingFlag, 0)
	if err != nil {
		return nil, d.pathError("open", name, err)
	}
	defer func() { _ = file.Close() }()

	if err := d.confirmRegular(name, expected, file); err != nil {
		return nil, err
	}
	if err := checkOwnerOnly(file, filepath.Join(d.path, name)); err != nil {
		return nil, err
	}
	raw, err := readBounded(file, limit)
	if err != nil {
		return nil, fmt.Errorf("securefile: read %q: %w", filepath.Join(d.path, name), err)
	}
	return raw, nil
}

// restrictExisting applies mode to an existing regular file relative to d.
//
// The sequence mirrors readFile: Lstat, non-blocking open, post-open identity
// confirmation, and only then the permission change, which goes through the
// descriptor rather than the pathname. A FIFO or a symlink planted at the name is
// refused before any mode is touched.
func (d *dir) restrictExisting(name string, mode fs.FileMode) error {
	expected, err := d.root.Lstat(name)
	if err != nil {
		return d.pathError("inspect", name, err)
	}
	if !expected.Mode().IsRegular() {
		return fmt.Errorf("securefile: %q is not a regular file (type %v): %w",
			filepath.Join(d.path, name), expected.Mode().Type(), ErrInsecurePath)
	}

	file, err := d.root.OpenFile(name, os.O_RDONLY|nonBlockingFlag, 0)
	if err != nil {
		return d.pathError("open", name, err)
	}
	defer func() { _ = file.Close() }()

	if err := d.confirmRegular(name, expected, file); err != nil {
		return err
	}
	return restrictFile(file, filepath.Join(d.path, name), mode)
}

func (d *dir) confirmRegular(name string, expected fs.FileInfo, file *os.File) error {
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(expected, opened) {
		return fmt.Errorf("securefile: %q changed identity while it was opened: %w",
			filepath.Join(d.path, name), ErrInsecurePath)
	}
	return nil
}

// openForLocking opens name relative to d for use as an advisory (flock(2))
// lock file, creating it with mode if it is not already there, and returns it
// open: unlike every other operation here, the caller needs the live
// descriptor for as long as it holds the lock, so this one does not close it.
//
// A fresh name is created with O_EXCL, which POSIX guarantees fails against an
// existing symlink of that name regardless of what it points to, so a planted
// symlink cannot be raced into place between two calls. A name that is already
// there is opened only after the same Lstat-then-confirm discipline readFile
// uses: a symlink is refused before the open, and the post-open identity check
// refuses a swap between the two observations. Either path enforces an
// owner-only mode before returning the descriptor.
func (d *dir) openForLocking(name string, mode fs.FileMode) (*os.File, error) {
	created, err := d.root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, mode)
	switch {
	case err == nil:
		if restrictErr := restrictFile(created, filepath.Join(d.path, name), mode); restrictErr != nil {
			_ = created.Close()
			return nil, restrictErr
		}
		return created, nil
	case errors.Is(err, fs.ErrExist):
		return d.openExistingLockFile(name)
	default:
		return nil, d.pathError("create lock file", name, err)
	}
}

// openExistingLockFile opens a lock file name is expected to already name,
// refusing anything that is not the plain regular file it claimed to be.
func (d *dir) openExistingLockFile(name string) (*os.File, error) {
	expected, err := d.root.Lstat(name)
	if err != nil {
		return nil, d.pathError("inspect lock file", name, err)
	}
	if !expected.Mode().IsRegular() {
		return nil, fmt.Errorf("securefile: %q is not a regular file (type %v): %w",
			filepath.Join(d.path, name), expected.Mode().Type(), ErrInsecurePath)
	}

	file, err := d.root.OpenFile(name, os.O_RDWR|nonBlockingFlag, 0)
	if err != nil {
		return nil, d.pathError("open lock file", name, err)
	}
	if err := d.confirmRegular(name, expected, file); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := checkOwnerOnly(file, filepath.Join(d.path, name)); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// writeFile replaces name atomically. An existing name that is not a regular file
// is refused, so a planted symlink stops the write instead of absorbing it.
func (d *dir) writeFile(name string, content []byte, mode fs.FileMode) error {
	if err := d.checkReplaceable(name); err != nil {
		return err
	}

	temporary, err := d.writeTemporary(name, content, mode)
	if err != nil {
		return err
	}
	if err := d.root.Rename(temporary, name); err != nil {
		_ = d.root.Remove(temporary)
		return d.pathError("commit", name, err)
	}
	d.sync()
	return nil
}

// installNewFile links a complete temporary file into place, which fails when the
// name is taken. The temporary is always removed: after a successful link it is a
// second name for the same content, and after a collision it is unwanted.
func (d *dir) installNewFile(name string, content []byte, mode fs.FileMode) error {
	if err := d.checkReplaceable(name); err != nil {
		return err
	}

	temporary, err := d.writeTemporary(name, content, mode)
	if err != nil {
		return err
	}
	defer func() { _ = d.root.Remove(temporary) }()

	if err := d.root.Link(temporary, name); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("securefile: %q: %w", filepath.Join(d.path, name), ErrExists)
		}
		return d.pathError("install", name, err)
	}
	d.sync()
	return nil
}

// checkReplaceable refuses a target that exists as anything other than a regular
// file. A rename would replace a symlink rather than follow it, but refusing is the
// honest answer: someone planted it.
func (d *dir) checkReplaceable(name string) error {
	info, err := d.root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return d.pathError("inspect", name, err)
	case !info.Mode().IsRegular():
		return fmt.Errorf("securefile: %q exists and is not a regular file (type %v): %w",
			filepath.Join(d.path, name), info.Mode().Type(), ErrInsecurePath)
	}
	return nil
}

// writeTemporary creates a fresh sibling of name exclusively, writes content,
// enforces mode and syncs the data. It returns the temporary's name.
func (d *dir) writeTemporary(name string, content []byte, mode fs.FileMode) (string, error) {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("securefile: temporary name for %q: %w", name, err)
	}
	temporary := name + ".tmp-" + hex.EncodeToString(suffix)

	if err := d.fillTemporary(temporary, content, mode); err != nil {
		_ = d.root.Remove(temporary)
		return "", err
	}
	return temporary, nil
}

func (d *dir) fillTemporary(temporary string, content []byte, mode fs.FileMode) error {
	file, err := d.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return d.pathError("create temporary file", temporary, err)
	}
	defer func() { _ = file.Close() }()

	if _, err := file.Write(content); err != nil {
		return d.pathError("write temporary file", temporary, err)
	}
	if err := restrictFile(file, filepath.Join(d.path, temporary), mode); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return d.pathError("sync temporary file", temporary, err)
	}
	return nil
}

// remove deletes name. An absent name is not an error.
func (d *dir) remove(name string) error {
	if err := d.root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return d.pathError("remove", name, err)
	}
	d.sync()
	return nil
}

// sync flushes the directory entry so a rename or a link survives a crash. Failure
// is not fatal: some platforms refuse to open a directory for reading, and the file
// data is already synced.
func (d *dir) sync() {
	handle, err := d.root.OpenFile(".", os.O_RDONLY, 0)
	if err != nil {
		return
	}
	defer func() { _ = handle.Close() }()
	_ = handle.Sync()
}

// pathError maps a filesystem error onto this package's sentinels, so callers never
// have to interpret a raw syscall error.
func (d *dir) pathError(operation, name string, cause error) error {
	full := filepath.Join(d.path, name)
	switch {
	case errors.Is(cause, fs.ErrNotExist):
		return fmt.Errorf("securefile: %s %q: %w", operation, full, ErrNotFound)
	case errors.Is(cause, fs.ErrExist):
		return fmt.Errorf("securefile: %s %q: %w", operation, full, ErrExists)
	case errors.Is(cause, ErrInsecurePath):
		return cause
	}
	return fmt.Errorf("securefile: %s %q: %w: %w", operation, full, ErrInsecurePath, cause)
}
