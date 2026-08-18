package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"modernc.org/sqlite"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/migrations"
)

// sqliteBusyCode is SQLITE_BUSY: the write lock could not be acquired within the
// busy timeout. It is the one SQLite result code this package needs to recognize
// by number, so it is named here with its source rather than inlined: SQLite's
// own C header, sqlite3.h, defines it as 5.
const sqliteBusyCode = 5

// isBusyError reports whether err is the driver's own SQLITE_BUSY, unwrapping
// through any wrapping database/sql or this package has already applied.
func isBusyError(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqliteBusyCode
}

// wrapSQLError translates a database-boundary failure, naming ErrDatabaseBusy
// when the driver reports SQLITE_BUSY so a caller can tell contention — most
// likely a running server holding the write lock — apart from every other
// failure without inspecting driver internals. Every write this package issues
// outside an explicit transaction (schema install, migration bookkeeping) goes
// through it too, because SQLITE_BUSY can surface there just as it can inside
// inTx: OpenSQLite's own schema-metadata install runs on every open, including
// one against an already-migrated database.
func wrapSQLError(operation string, err error) error {
	if isBusyError(err) {
		return fmt.Errorf("store: %s: %w", operation, ErrDatabaseBusy)
	}
	return fmt.Errorf("store: %s: %w", operation, err)
}

// SQLiteStore is the multi-principal storage backend.
//
// # What it holds
//
// Principals with an encrypted Garmin identity linkage, versioned encrypted Garmin
// DI token sets, registered OAuth clients with their exact redirect URIs,
// per-principal client consents, hashed authorization transactions and codes,
// hashed opaque MCP token material grouped into revocable families, the schema and
// encryption-key versions, and audit events. The schema lives in migrations/.
//
// # What is safe with one instance
//
// One process is the supported deployment, and inside it these hold:
//
//   - Compare-and-set on the Garmin token set is a single UPDATE ... WHERE version
//     = ? inside an immediate transaction, so two goroutines racing a rotation
//     produce exactly one winner and one ErrVersionConflict. The loser reloads.
//   - Refresh rotation, reuse detection, consent revocation and Garmin unlinking
//     are single transactions. They are idempotent: running one twice reaches the
//     same state and reports no error the second time.
//   - Compare-and-set on an authorization transaction is the same single UPDATE
//     ... WHERE version = ?, and reports ErrTransactionConflict to every loser.
//     Consuming a transaction is a read and a delete in one transaction with a
//     one-row requirement, so concurrent completions elect exactly one winner —
//     which a compare-and-set alone cannot do, because two callers can serialize
//     and each win its own in turn.
//   - Expiry is enforced on every read, so an expired transaction, code or token is
//     never returned even before Cleanup runs.
//
// # What needs shared coordination and is therefore NOT claimed
//
// This is a single-active-instance design. It does not scale horizontally, and none
// of the following is provided:
//
//   - Two processes on one database file. SQLite's own locking would keep each
//     individual transaction atomic, so the CAS and the cascades would still be
//     correct, but the busy timeout is the only backpressure and a second writer
//     turns every contended write into a timeout. Nothing here elects a leader.
//   - A database on a network filesystem. SQLite's locking is not reliable there,
//     which invalidates every guarantee above.
//   - Cleanup coordination. Cleanup is bounded per call and safe to run twice, but
//     two instances would each pay the full scan.
//   - Client-cache invalidation across instances. A revocation recorded here is
//     visible to this process; another process's cached Garmin client would not
//     hear about it. In-memory cookie jars and MFA transaction state stay
//     per-process by design and are lost on restart.
//
// A SQLiteStore is safe for concurrent use by multiple goroutines and holds no
// package-level state.
type SQLiteStore struct {
	db     *sql.DB
	crypt  keySet
	keys   indexKeys
	now    func() time.Time
	schema int

	// revocations receives an event after every revocation cascade commits. Nil
	// means nobody is listening, which is the single-user shape.
	revocations RevocationSink
}

// SQLiteConfig configures a SQLiteStore. Every field is explicit: nothing here is
// read from the environment.
type SQLiteConfig struct {
	// Path is the database file. Its parent directory is created 0700 and the
	// database and its sidecars are forced to 0600. A ~username path and a
	// symlinked ancestor are refused.
	Path string

	// Key encrypts every sealed column, and is the active key every write is
	// sealed under. Obtain it from cryptostore.LoadOrCreateKey.
	Key cryptostore.Key

	// RetiredKeys are additional key versions kept only to read a sealed column a
	// staged rotation has not yet re-sealed onto Key. Never used to seal a write.
	// A nil slice is the pre-rotation shape: every sealed column must already be
	// sealed under Key.
	RetiredKeys []cryptostore.Key

	// Database tunes the connection pool and the pragmas. The zero value selects
	// the documented defaults.
	Database DatabaseOptions

	// Migrations is the migration set to apply. Nil selects the embedded set,
	// which is what a deployment wants; a test supplies its own.
	Migrations fs.FS

	// Now is the clock. Nil selects time.Now. It exists so expiry is tested by
	// moving time rather than by sleeping.
	Now func() time.Time

	// Revocations receives an event after each revocation cascade commits, so a
	// live session can be closed rather than surviving until its next request. Nil
	// means nobody listens, which is the supported single-user shape. The sink is
	// called on the goroutine that revoked and must not block.
	Revocations RevocationSink
}

// OpenSQLite opens, migrates and returns the store.
//
// It fails closed. An unusable encryption key, an unsafe path, a database migrated
// by a newer build (ErrSchemaTooNew) and an altered applied migration
// (ErrMigrationChanged) all refuse to open rather than degrade.
func OpenSQLite(ctx context.Context, cfg SQLiteConfig) (*SQLiteStore, error) {
	crypt, err := newKeySet(cfg.Key, cfg.RetiredKeys)
	if err != nil {
		return nil, err
	}
	set := cfg.Migrations
	if set == nil {
		set = migrations.FS()
	}
	clock := cfg.Now
	if clock == nil {
		clock = time.Now
	}

	db, err := OpenDatabase(cfg.Path, cfg.Database)
	if err != nil {
		return nil, err
	}
	opened, err := buildStore(ctx, db, crypt, clock, set)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	opened.revocations = cfg.Revocations
	return opened, nil
}

// buildStore migrates the database and loads the database-wide state. It is split
// out so OpenSQLite can close the connection on any failure along the way.
func buildStore(ctx context.Context, db *sql.DB, crypt keySet,
	clock func() time.Time, set fs.FS,
) (*SQLiteStore, error) {
	result, err := Migrate(ctx, db, set)
	if err != nil {
		return nil, err
	}
	keys, err := loadIndexKeys(ctx, db, crypt, clock)
	if err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db, crypt: crypt, keys: keys, now: clock, schema: result.ToVersion}, nil
}

// Close releases the connection pool.
func (s *SQLiteStore) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("store: close database: %w", err)
	}
	return nil
}

// SchemaVersion reports the migration version this store opened at.
func (s *SQLiteStore) SchemaVersion() int { return s.schema }

// Ping reports whether the database answers.
//
// It exists for a readiness probe, which must distinguish a process that is alive
// from one that can actually serve. A pool that cannot reach its file answers
// here and nowhere else: every other method would report the same failure only
// once a request had already arrived and failed.
func (s *SQLiteStore) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("store: ping database: %w", err)
	}
	return nil
}

// EncryptionKeyVersion reports the cryptostore key version recorded when the
// database was created. It is the starting point of a staged key rotation: a row
// whose own key_version is lower still opens under the key of that version.
func (s *SQLiteStore) EncryptionKeyVersion(ctx context.Context) (int, error) {
	var version int
	err := s.db.QueryRowContext(ctx,
		`SELECT encryption_key_version FROM schema_meta WHERE id = 1`).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("store: read encryption key version: %w", err)
	}
	return version, nil
}

// Pragmas reports what the connection this call is served by is configured with. It
// is how an operator, and the test suite, verify WAL, foreign keys and the busy
// timeout against the database rather than against the DSN string.
func (s *SQLiteStore) Pragmas(ctx context.Context) (PragmaState, error) {
	return Pragmas(ctx, s.db)
}

// loadIndexKeys reads the lookup-derivation root, creating it on a fresh database.
//
// The insert uses OR IGNORE against the id = 1 primary key, so two goroutines that
// open a fresh database at the same time cannot both install a root: the loser's
// insert is a no-op and the following read returns the winner's row. Whoever loses
// discards its generated material, which is why the material is re-read rather than
// reused after the insert.
func loadIndexKeys(ctx context.Context, db *sql.DB, crypt keySet,
	clock func() time.Time,
) (indexKeys, error) {
	sealed, err := sealFreshIndexRoot(crypt)
	if err != nil {
		return indexKeys{}, err
	}
	version, err := crypt.activeVersion()
	if err != nil {
		return indexKeys{}, err
	}
	_, err = db.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_meta (id, encryption_key_version, index_root_sealed, created_at)
		 VALUES (1, ?, ?, ?)`,
		version, sealed, formatTime(clock()))
	if err != nil {
		return indexKeys{}, wrapSQLError("install schema metadata", err)
	}

	var stored []byte
	err = db.QueryRowContext(ctx,
		`SELECT index_root_sealed FROM schema_meta WHERE id = 1`).Scan(&stored)
	if err != nil {
		return indexKeys{}, fmt.Errorf("store: read index root: %w", err)
	}
	opened, _, err := crypt.decrypt(indexRootPrincipal, indexRootRecordType, stored)
	if err != nil {
		// The cause names versions and sizes only, never material. A failure here
		// almost always means none of the configured keys open this database,
		// which must refuse to open rather than create a second root.
		return indexKeys{}, fmt.Errorf("store: open index root: %w: %w", ErrCorruptRecord, err)
	}
	return newIndexKeys(opened)
}

// keyVersionOf reports the cryptostore key version, which the schema records so a
// staged rotation can tell which key opens which row.
//
// cryptostore exposes no accessor for the version — deliberately, because it
// exposes no accessor for anything about a Key. Its redacted JSON rendering does
// carry the version, and that rendering is documented public behavior of the type,
// so it is the supported way to read the version from outside the package. The
// alternative, parsing the version out of an envelope header, would couple this
// file to cryptostore's wire format, which is a worse dependency.
func keyVersionOf(key cryptostore.Key) (int, error) {
	encoded, err := json.Marshal(key)
	if err != nil {
		return 0, fmt.Errorf("store: read encryption key version: %w: %w", ErrInvalidConfig, err)
	}
	var rendered struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(encoded, &rendered); err != nil {
		return 0, fmt.Errorf("store: decode encryption key version: %w: %w", ErrInvalidConfig, err)
	}
	if rendered.Version < 1 {
		return 0, fmt.Errorf("store: encryption key reports version %d, want a positive version: %w",
			rendered.Version, ErrInvalidConfig)
	}
	return rendered.Version, nil
}

// sealFreshIndexRoot generates and seals a candidate root under the active key.
func sealFreshIndexRoot(crypt keySet) ([]byte, error) {
	material, err := newIndexRootMaterial()
	if err != nil {
		return nil, err
	}
	sealed, err := crypt.encrypt(indexRootPrincipal, indexRootRecordType, material)
	if err != nil {
		return nil, fmt.Errorf("store: seal index root: %w", err)
	}
	return sealed, nil
}

// inTx runs body in one immediate transaction and commits it, rolling back on any
// error.
//
// Every multi-row change in this store goes through it, which is what makes the
// revocation cascades all-or-nothing. The deferred Rollback after a successful
// Commit is a no-op, so the single defer covers both paths.
func (s *SQLiteStore) inTx(ctx context.Context, body func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapSQLError("begin transaction", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := body(tx); err != nil {
		if isBusyError(err) {
			return wrapSQLError("transaction", err)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return wrapSQLError("commit transaction", err)
	}
	return nil
}

// checkStoreRequest is the boundary check every method starts with.
func checkStoreRequest(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store: request cancelled: %w", err)
	}
	return nil
}
