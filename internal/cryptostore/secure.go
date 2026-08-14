package cryptostore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// maxKeyFileBytes bounds a key document read. A key file is a few hundred bytes;
// anything larger is a mistake or an attempt to exhaust memory.
const maxKeyFileBytes = 4 << 10

// checkNoSymlinkAncestry refuses path if it, or any of its parent directories, is
// a symlink. A component that cannot be inspected is also refused: an
// uncheckable path is not a safe path.
//
// Source: token_file_path in client.py (0.3.10). O_NOFOLLOW on the final open
// covers the last component only, so an intermediate symlinked directory
// (~/.garminconnect -> /attacker/dir) would still redirect the read or write.
func checkNoSymlinkAncestry(path string, sentinel error) error {
	for current := filepath.Clean(path); ; {
		info, err := os.Lstat(current)
		switch {
		case err == nil && info.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("cryptostore: path %q has a symlinked component %q: %w", path, current, sentinel)
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("cryptostore: path %q component %q cannot be checked: %w", path, current, sentinel)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
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

// writeFileAtomically writes content to a fresh temporary sibling of path, syncs
// it, then renames it over path. A reader therefore sees either the previous
// content or the new content, never a truncated file.
//
// Source: Client.dump in client.py (0.3.10), which writes a sibling temporary
// file and calls replace() so a concurrent reader never observes a partial token
// file. The explicit chmod is kept for the reason given there: the mode passed
// to open is masked by the process umask.
func writeFileAtomically(path string, content []byte, mode fs.FileMode) error {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return fmt.Errorf("temporary name: %w", err)
	}
	tmp := path + ".tmp-" + hex.EncodeToString(suffix)

	if err := writeTempFile(tmp, content, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename temporary file: %w", err)
	}
	syncDir(filepath.Dir(path))
	return nil
}

// writeTempFile creates tmp exclusively, writes content, enforces owner-only
// access and syncs the data before the caller renames it into place.
func writeTempFile(tmp string, content []byte, mode fs.FileMode) error {
	file, err := createExclusiveNoFollow(tmp, mode)
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	defer func() { _ = file.Close() }()

	if _, err := file.Write(content); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := restrictToOwner(file, tmp, mode); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	return nil
}

// syncDir flushes a directory entry so a rename survives a crash. Failure is not
// fatal: some platforms refuse to open a directory, and the data is already
// synced.
func syncDir(dir string) {
	handle, err := os.Open(dir)
	if err != nil {
		return
	}
	defer func() { _ = handle.Close() }()
	_ = handle.Sync()
}
