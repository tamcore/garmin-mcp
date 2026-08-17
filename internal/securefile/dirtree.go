package securefile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

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
//
// The chmod is skipped only when the current mode exactly equals mode: chmod is the
// one operation that fails with EPERM on a directory this process does not own (a
// Kubernetes volume mount root is exactly that case), so an already-correct
// directory must not be forced through it.
func (d *dir) restrict(mode fs.FileMode) error {
	info, err := d.root.Stat(".")
	if err != nil || !info.IsDir() {
		return fmt.Errorf("securefile: %q is not a usable directory: %w", d.path, ErrInsecurePath)
	}
	// Perm() masks away setgid/setuid/sticky, so a 2700 directory compares equal
	// to 0700 here and keeps its setgid bit; acceptable since group has no
	// permission bits.
	if info.Mode().Perm() == mode {
		return nil
	}
	return restrictDir(d.root, d.path, mode)
}
