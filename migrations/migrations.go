// Package migrations carries the embedded, forward-only SQL schema migrations
// for the SQLite storage backend in internal/store.
//
// # Rules
//
// Migrations are monotonic and forward-only. A file is named
// NNNN_snake_case_name.sql, where NNNN is a zero-padded, strictly increasing
// integer starting at 0001. There are no gaps and no duplicates. Down migrations
// do not exist: rolling back a deployment means restoring a backup, because a
// down migration that drops a column silently destroys token material.
//
// A migration file that has shipped is immutable. Change the schema by adding the
// next file, never by editing an applied one; the migrator records the applied
// version and refuses a database whose recorded version is newer than this build
// knows.
//
// The bookkeeping table (schema_migrations) is created by the migrator, not by a
// migration file, so an empty database and a partially migrated one are handled
// by the same code path.
package migrations

import (
	"embed"
	"io/fs"
)

// files holds the .sql migrations. The pattern is explicit so a stray file in the
// directory is not embedded by accident.
//
//go:embed *.sql
var files embed.FS

// FS returns the embedded migration set.
//
// It is returned as a read-only fs.FS so a caller cannot reach the embed.FS
// methods and cannot mutate the set. The same value is returned on every call;
// embed.FS is immutable and safe for concurrent use.
func FS() fs.FS { return files }
