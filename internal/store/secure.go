package store

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

// File and directory modes for token material. Both are enforced with an explicit
// chmod after creation, because the modes passed to os.MkdirAll and os.OpenFile are
// masked by the process umask, which is global and may be hostile.
//
// Source: Client.dump in client.py (0.3.10), which writes 0o600 inside 0o700 and
// chmods both unconditionally (GHSA-wjhr-76vg-2hvc).
const (
	tokenFileMode = 0o600
	tokenDirMode  = 0o700
)

// maxRecordBytes bounds a record read. A token record is a few kilobytes; anything
// larger is a mistake or an attempt to exhaust memory.
const maxRecordBytes = 64 << 10

// ensureOwnerOnlyDir creates dir and every parent, then enforces owner-only access
// on dir itself. MkdirAll's mode is masked by the umask and is a no-op for a
// directory that already exists, so the follow-up is not redundant.
func ensureOwnerOnlyDir(dir string) error {
	if err := checkNoSymlinkAncestry(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, tokenDirMode); err != nil {
		return fmt.Errorf("store: create directory %q: %w", dir, err)
	}
	return restrictDirToOwner(dir)
}

// readOwnerOnlyFile reads path, refusing to follow a symlink and refusing a file
// any other local account can reach. An absent file reports notFound, so the
// caller maps it onto its own sentinel.
func readOwnerOnlyFile(path string, notFound error) ([]byte, error) {
	if err := checkNoSymlinkAncestry(path); err != nil {
		return nil, err
	}

	file, err := openNoFollow(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("store: %q: %w", path, notFound)
		}
		return nil, fmt.Errorf("store: open %q: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("store: stat %q: %w", path, err)
	}
	if err := checkOwnerOnly(info.Mode(), path); err != nil {
		return nil, err
	}
	raw, err := readBounded(file, maxRecordBytes)
	if err != nil {
		return nil, fmt.Errorf("store: read %q: %w", path, err)
	}
	return raw, nil
}

// readBounded reads at most limit bytes and fails if there is more.
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

// writeFileAtomically writes content to a fresh temporary sibling of path, syncs it,
// then renames it over path. A concurrent reader therefore sees either the previous
// record or the new one, never a truncated file.
//
// Source: Client.dump in client.py (0.3.10), which writes a sibling temporary file
// and calls replace() for exactly this reason.
func writeFileAtomically(path string, content []byte) error {
	if err := checkNoSymlinkAncestry(path); err != nil {
		return err
	}

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return fmt.Errorf("store: temporary name for %q: %w", path, err)
	}
	tmp := path + ".tmp-" + hex.EncodeToString(suffix)

	if err := writeTempFile(tmp, content); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("store: write %q: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("store: commit %q: %w", path, err)
	}
	syncDir(filepath.Dir(path))
	return nil
}

// writeTempFile creates tmp exclusively, writes content, enforces owner-only access
// and syncs the data before the caller renames it into place.
func writeTempFile(tmp string, content []byte) error {
	file, err := createExclusiveNoFollow(tmp)
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	defer func() { _ = file.Close() }()

	if _, err := file.Write(content); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := restrictFileToOwner(file, tmp); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	return nil
}

// syncDir flushes a directory entry so a rename survives a crash. Failure is not
// fatal: some platforms refuse to open a directory, and the data is already synced.
func syncDir(dir string) {
	handle, err := os.Open(dir)
	if err != nil {
		return
	}
	defer func() { _ = handle.Close() }()
	_ = handle.Sync()
}
