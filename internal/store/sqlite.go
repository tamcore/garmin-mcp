package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/migrations"
)

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
	key    cryptostore.Key
	keys   indexKeys
	now    func() time.Time
	schema int
}

// SQLiteConfig configures a SQLiteStore. Every field is explicit: nothing here is
// read from the environment.
type SQLiteConfig struct {
	// Path is the database file. Its parent directory is created 0700 and the
	// database and its sidecars are forced to 0600. A ~username path and a
	// symlinked ancestor are refused.
	Path string

	// Key encrypts every sealed column. Obtain it from cryptostore.LoadOrCreateKey.
	Key cryptostore.Key

	// Database tunes the connection pool and the pragmas. The zero value selects
	// the documented defaults.
	Database DatabaseOptions

	// Migrations is the migration set to apply. Nil selects the embedded set,
	// which is what a deployment wants; a test supplies its own.
	Migrations fs.FS

	// Now is the clock. Nil selects time.Now. It exists so expiry is tested by
	// moving time rather than by sleeping.
	Now func() time.Time
}

// OpenSQLite opens, migrates and returns the store.
//
// It fails closed. An unusable encryption key, an unsafe path, a database migrated
// by a newer build (ErrSchemaTooNew) and an altered applied migration
// (ErrMigrationChanged) all refuse to open rather than degrade.
func OpenSQLite(ctx context.Context, cfg SQLiteConfig) (*SQLiteStore, error) {
	if err := checkKeyUsable(cfg.Key); err != nil {
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
	opened, err := buildStore(ctx, db, cfg.Key, clock, set)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return opened, nil
}

// buildStore migrates the database and loads the database-wide state. It is split
// out so OpenSQLite can close the connection on any failure along the way.
func buildStore(ctx context.Context, db *sql.DB, key cryptostore.Key,
	clock func() time.Time, set fs.FS,
) (*SQLiteStore, error) {
	result, err := Migrate(ctx, db, set)
	if err != nil {
		return nil, err
	}
	keys, err := loadIndexKeys(ctx, db, key, clock)
	if err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db, key: key, keys: keys, now: clock, schema: result.ToVersion}, nil
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
func loadIndexKeys(ctx context.Context, db *sql.DB, key cryptostore.Key,
	clock func() time.Time,
) (indexKeys, error) {
	sealed, err := sealFreshIndexRoot(key)
	if err != nil {
		return indexKeys{}, err
	}
	version, err := keyVersionOf(key)
	if err != nil {
		return indexKeys{}, err
	}
	_, err = db.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_meta (id, encryption_key_version, index_root_sealed, created_at)
		 VALUES (1, ?, ?, ?)`,
		version, sealed, formatTime(clock()))
	if err != nil {
		return indexKeys{}, fmt.Errorf("store: install schema metadata: %w", err)
	}

	var stored []byte
	err = db.QueryRowContext(ctx,
		`SELECT index_root_sealed FROM schema_meta WHERE id = 1`).Scan(&stored)
	if err != nil {
		return indexKeys{}, fmt.Errorf("store: read index root: %w", err)
	}
	opened, err := cryptostore.Decrypt(key, indexRootPrincipal, indexRootRecordType, stored)
	if err != nil {
		// The cause names versions and sizes only, never material. A failure here
		// almost always means the wrong encryption key was supplied for this
		// database, which must refuse to open rather than create a second root.
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

// sealFreshIndexRoot generates and seals a candidate root.
func sealFreshIndexRoot(key cryptostore.Key) ([]byte, error) {
	material, err := newIndexRootMaterial()
	if err != nil {
		return nil, err
	}
	sealed, err := cryptostore.Encrypt(key, indexRootPrincipal, indexRootRecordType, material)
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
		return fmt.Errorf("store: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := body(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit transaction: %w", err)
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
