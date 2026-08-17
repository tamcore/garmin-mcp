//go:build unix

package store

import (
	"fmt"
	"os"

	"github.com/tamcore/garmin-mcp/internal/securefile"
	"golang.org/x/sys/unix"
)

// Cross-process serialization for one principal's record file.
//
// principalLocks (filestore.go) is store state: it serializes goroutines
// inside ONE *FileStore, which is all a single serving process needs. It does
// nothing for two SEPARATE processes that each open their own *FileStore over
// the same directory — which is exactly the shape a live `serve` process and a
// `rotate-key` run actually have. A content-equality re-check right before the
// write narrows that gap but does not close it: a Go-level "read, then write"
// is two separate operations, not the one atomic statement a SQL
// `UPDATE ... WHERE` is at the database engine, so a second process can still
// commit in the instant between the re-read and the write. recordLock closes
// that gap for real, with an OS-level advisory lock (flock(2)) held on a
// dedicated file beside the record, so the entire read-modify-write critical
// section of a Save or a Reseal runs exclusively of the other process's.
type recordLock struct {
	file *os.File
}

// lockRecord blocks until it holds the exclusive lock for lockPath, creating
// the lock file if absent. Release must be called exactly once.
//
// The lock file is separate from the record file itself: locking the record
// file would entangle this with the record's own open/read/write calls, and
// an empty, never-removed lock file beside the record is harmless and never
// holds anything worth protecting on its own.
//
// The open goes through internal/securefile rather than a plain os.OpenFile,
// which would follow a symlink planted at lockPath. Following one is a real
// hole here: a symlink aimed at the record file itself would, once that file
// is atomically replaced (a new inode under the same name), move a later
// lockRecord call onto the new inode while an earlier caller's descriptor
// still references the old one — splitting the lock domain in two so both
// callers believe they hold it and can write concurrently, exactly what this
// lock exists to prevent. securefile.OpenForLocking refuses the symlink
// instead.
func lockRecord(lockPath string) (*recordLock, error) {
	file, err := securefile.OpenForLocking(lockPath, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: open record lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("store: acquire record lock: %w", err)
	}
	return &recordLock{file: file}, nil
}

// release unlocks and closes the lock file. The file itself is left on disk
// on purpose: removing it here would race a second locker that already opened
// it before this call unlinks it.
func (l *recordLock) release() error {
	unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return fmt.Errorf("store: release record lock: %w", unlockErr)
	}
	return closeErr
}
