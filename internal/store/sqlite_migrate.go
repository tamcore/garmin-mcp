package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// createMigrationsTable is the bookkeeping table. It is created by the migrator
// rather than by migration 0001, so an empty database and a partially migrated one
// take the same path, and so a migration file never has to know about it.
const createMigrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    checksum   TEXT NOT NULL,
    applied_at TEXT NOT NULL
) STRICT;`

// maxMigrationBytes bounds one migration file. The whole v1 schema is a few
// kilobytes; a larger file is a mistake.
const maxMigrationBytes = 256 << 10

// migrationNamePattern is the only accepted file name shape: four digits, an
// underscore, a lower-snake-case name, and .sql. Anything else is a build defect,
// because a name the migrator cannot order is a migration nobody can reason about.
//
// It is built per call rather than kept in a package variable: a compiled regexp is
// package-level state, and this package keeps none. The set is a handful of files
// read once at start-up, so the compile is not on any hot path.
func migrationNamePattern() *regexp.Regexp {
	return regexp.MustCompile(`^(\d{4})_([a-z0-9]+(?:_[a-z0-9]+)*)\.sql$`)
}

// Migration is one forward-only schema step.
//
// The zero value is not a migration. Values are produced by LoadMigrations and are
// immutable: nothing in this package mutates one after it is built.
type Migration struct {
	// Version is the ordering key, parsed from the file name. Versions start at 1
	// and increase by exactly one.
	Version int

	// Name is the descriptive part of the file name, without the version prefix
	// and without the extension.
	Name string

	// Checksum is the hex SHA-256 of the file's bytes. It is recorded when the
	// migration is applied and compared on every later run, so an edit to a
	// shipped migration is refused instead of silently ignored.
	Checksum string

	// statements is the SQL body. It is unexported so a caller cannot execute a
	// migration outside Migrate, which is what provides the transaction.
	statements string
}

// AppliedMigration records that a migration ran.
type AppliedMigration struct {
	Version   int
	Name      string
	AppliedAt time.Time
}

// MigrationResult reports what one Migrate call did. Applied is empty when the
// database was already current, which makes the call safe to run on every start-up.
type MigrationResult struct {
	FromVersion int
	ToVersion   int
	Applied     []AppliedMigration
}

// LoadMigrations reads and validates a migration set.
//
// It reports ErrMigrationSet for an empty set, a name that does not match the
// required shape, a duplicated version, a version below 1, and a gap in the
// sequence. The returned slice is sorted by version.
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("store: list migrations: %w: %w", ErrMigrationSet, err)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("store: no migration files: %w", ErrMigrationSet)
	}

	pattern := migrationNamePattern()
	set := make([]Migration, 0, len(names))
	for _, name := range names {
		migration, err := loadMigration(fsys, pattern, name)
		if err != nil {
			return nil, err
		}
		set = append(set, migration)
	}
	sort.Slice(set, func(i, j int) bool { return set[i].Version < set[j].Version })

	if err := checkMonotonic(set); err != nil {
		return nil, err
	}
	return set, nil
}

// loadMigration parses one file name and reads its bytes.
func loadMigration(fsys fs.FS, pattern *regexp.Regexp, name string) (Migration, error) {
	groups := pattern.FindStringSubmatch(name)
	if groups == nil {
		return Migration{}, fmt.Errorf(
			"store: migration %q is not NNNN_name.sql: %w", name, ErrMigrationSet)
	}
	version, err := strconv.Atoi(groups[1])
	if err != nil || version < 1 {
		return Migration{}, fmt.Errorf(
			"store: migration %q has version %q, want a positive integer: %w",
			name, groups[1], ErrMigrationSet)
	}

	body, err := readMigrationFile(fsys, name)
	if err != nil {
		return Migration{}, err
	}
	digest := sha256.Sum256(body)
	return Migration{
		Version:    version,
		Name:       groups[2],
		Checksum:   hex.EncodeToString(digest[:]),
		statements: string(body),
	}, nil
}

// readMigrationFile reads one migration, bounded.
func readMigrationFile(fsys fs.FS, name string) ([]byte, error) {
	body, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("store: read migration %q: %w: %w", name, ErrMigrationSet, err)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("store: migration %q is empty: %w", name, ErrMigrationSet)
	}
	if len(body) > maxMigrationBytes {
		return nil, fmt.Errorf("store: migration %q is %d bytes, over the %d byte bound: %w",
			name, len(body), maxMigrationBytes, ErrMigrationSet)
	}
	return body, nil
}

// checkMonotonic requires versions 1..n with no gap and no duplicate.
func checkMonotonic(set []Migration) error {
	for index, migration := range set {
		want := index + 1
		if migration.Version != want {
			return fmt.Errorf(
				"store: migration %d sits at position %d, want version %d "+
					"(a gap or a duplicate breaks the ordering): %w",
				migration.Version, index, want, ErrMigrationSet)
		}
	}
	return nil
}

// SchemaVersion reports the highest migration version recorded in db, or 0 when
// nothing has been applied. It does not create the bookkeeping table, so it is safe
// to call against a database this build must not touch.
func SchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&version)
	switch {
	case err != nil && isMissingTable(err):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("store: read schema version: %w", err)
	case !version.Valid:
		return 0, nil
	}
	return int(version.Int64), nil
}

// isMissingTable reports whether err is SQLite's "no such table". A database with
// no bookkeeping table is at version 0, which is a state and not a failure. The
// driver does not export a typed error for it, so the text is the only signal
// available; a false negative only turns a state into a wrapped error, never into
// silent data loss.
func isMissingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

// Migrate brings db up to the version the set in fsys describes.
//
// Each migration runs inside its own transaction together with the bookkeeping
// insert that records it, so a migration is applied completely or not at all: a
// failure leaves the database at the last successfully applied version with nothing
// partial, and reports ErrMigrationFailed. SQLite makes DDL transactional, which is
// what allows a CREATE TABLE and a failing INSERT in the same file to roll back
// together.
//
// Migrate is idempotent. It reports ErrSchemaTooNew when the database has been
// migrated past what this build knows, and ErrMigrationChanged when an already
// applied migration's bytes no longer match what was recorded.
//
// Concurrency: Migrate is safe against concurrent goroutines in this process
// because each step is one transaction and a duplicate bookkeeping insert violates
// the primary key. It is not a distributed lock. Two processes migrating the same
// file at the same time is outside the single-active-instance deployment this store
// is built for.
func Migrate(ctx context.Context, db *sql.DB, fsys fs.FS) (MigrationResult, error) {
	set, err := LoadMigrations(fsys)
	if err != nil {
		return MigrationResult{}, err
	}
	if _, err := db.ExecContext(ctx, createMigrationsTable); err != nil {
		return MigrationResult{}, fmt.Errorf("store: create schema_migrations: %w", err)
	}

	recorded, err := readAppliedChecksums(ctx, db)
	if err != nil {
		return MigrationResult{}, err
	}
	from, err := checkCompatibility(set, recorded)
	if err != nil {
		return MigrationResult{}, err
	}

	result := MigrationResult{FromVersion: from, ToVersion: from}
	for _, migration := range set {
		if migration.Version <= from {
			continue
		}
		applied, err := applyMigration(ctx, db, migration)
		if err != nil {
			return MigrationResult{}, err
		}
		result.Applied = append(result.Applied, applied)
		result.ToVersion = migration.Version
	}
	return result, nil
}

// readAppliedChecksums returns version -> checksum for every recorded migration.
func readAppliedChecksums(ctx context.Context, db *sql.DB) (map[int]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("store: read schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	recorded := map[int]string{}
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("store: scan schema_migrations: %w", err)
		}
		recorded[version] = checksum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate schema_migrations: %w", err)
	}
	return recorded, nil
}

// checkCompatibility compares what ran against what this build carries, and returns
// the version the database is at.
func checkCompatibility(set []Migration, recorded map[int]string) (int, error) {
	known := make(map[int]string, len(set))
	for _, migration := range set {
		known[migration.Version] = migration.Checksum
	}

	current := 0
	for version, checksum := range recorded {
		want, ok := known[version]
		switch {
		case !ok:
			return 0, fmt.Errorf(
				"store: database has migration %d applied, this build knows %d migrations: %w",
				version, len(set), ErrSchemaTooNew)
		case want != checksum:
			return 0, fmt.Errorf("store: migration %d was applied with a different body: %w",
				version, ErrMigrationChanged)
		case version > current:
			current = version
		}
	}
	return current, nil
}

// applyMigration runs one migration and records it, in one transaction.
func applyMigration(ctx context.Context, db *sql.DB, migration Migration) (AppliedMigration, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return AppliedMigration{}, fmt.Errorf("store: begin migration %d: %w: %w",
			migration.Version, ErrMigrationFailed, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, migration.statements); err != nil {
		return AppliedMigration{}, fmt.Errorf("store: apply migration %d (%s): %w: %w",
			migration.Version, migration.Name, ErrMigrationFailed, err)
	}

	appliedAt := time.Now().UTC()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?, ?, ?, ?)`,
		migration.Version, migration.Name, migration.Checksum, formatTime(appliedAt))
	if err != nil {
		return AppliedMigration{}, fmt.Errorf("store: record migration %d: %w: %w",
			migration.Version, ErrMigrationFailed, err)
	}
	if err := tx.Commit(); err != nil {
		return AppliedMigration{}, fmt.Errorf("store: commit migration %d: %w: %w",
			migration.Version, ErrMigrationFailed, err)
	}
	return AppliedMigration{Version: migration.Version, Name: migration.Name, AppliedAt: appliedAt}, nil
}
