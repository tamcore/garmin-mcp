package store

import "io/fs"

// This file exports the small set of path-shape and mode facts an offline,
// metadata-only caller needs — the permission-repair command — without
// giving it any way to open a connection or read a record. Nothing here
// performs I/O beyond the symlink-ancestry check resolveDatabasePath already
// does; opening the database, the directories, or any record is deliberately
// out of reach from this file.

// SecureDirMode is the owner-only mode this package enforces on every
// directory it manages: the FileStore root, its records subdirectory
// ([RecordsDirName]), and a SQLite database's parent directory.
const SecureDirMode fs.FileMode = tokenDirMode

// TokenRecordFileMode is the owner-only mode enforced on a FileStore record
// file and on the advisory lock file beside it (see filelock_unix.go).
const TokenRecordFileMode fs.FileMode = tokenFileMode

// RecordsDirName is the subdirectory FileStore stores one file per principal
// under, relative to its configured root.
const RecordsDirName = recordsDirName

// DatabaseFileMode is the owner-only mode [OpenDatabase] enforces on the
// database file and on its "-wal" and "-shm" sidecars.
const DatabaseFileMode fs.FileMode = databaseFileMode

// DatabaseFiles resolves path exactly the way [OpenDatabase] does — expanding
// a leading ~, refusing ~username, and refusing a symlinked ancestor — and
// reports the database file plus its write-ahead-log and shared-memory
// sidecars in that order. It opens and creates nothing: an absent path is not
// an error, and neither is an absent sidecar, because a caller must be able
// to inspect a database this process has never started before.
func DatabaseFiles(path string) ([3]string, error) {
	var files [3]string
	resolved, err := resolveDatabasePath(path)
	if err != nil {
		return files, err
	}
	for i, suffix := range databaseFileSuffixes() {
		files[i] = resolved + suffix
	}
	return files, nil
}
