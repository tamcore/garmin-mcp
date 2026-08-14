package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tamcore/garmin-mcp/internal/securefile"

	// The pure-Go SQLite driver. It is pure Go on purpose: release binaries stay
	// CGO_ENABLED=0, so a cgo driver would break every cross-compiled artifact.
	// The blank import registers the driver under the name "sqlite".
	_ "modernc.org/sqlite"
)

// driverName is the name modernc.org/sqlite registers itself under.
const driverName = "sqlite"

// Connection defaults. They are deliberately small: SQLite serializes writers, so
// a large pool buys nothing and only turns contention into a pile of connections
// each waiting on the same write lock.
const (
	// defaultBusyTimeout is how long a statement waits for the write lock before
	// it reports SQLITE_BUSY. It must exceed the longest transaction this store
	// runs, which is a revocation cascade over one principal's rows.
	defaultBusyTimeout = 5 * time.Second

	// defaultMaxOpenConns bounds the pool. One writer plus a few readers is the
	// shape WAL mode rewards.
	defaultMaxOpenConns = 4

	// defaultConnMaxIdleTime recycles idle connections, so a long-lived process
	// does not hold file descriptors on a database an operator has replaced.
	defaultConnMaxIdleTime = 5 * time.Minute

	// databaseFileMode is the owner-only mode enforced on the database file and
	// its sidecars.
	databaseFileMode = 0o600
)

// DatabaseOptions tunes the connection pool and the per-connection pragmas. The
// zero value selects the documented defaults, so a caller that does not care
// passes DatabaseOptions{}.
type DatabaseOptions struct {
	// BusyTimeout is the SQLITE_BUSY wait. Zero selects five seconds; a negative
	// value is refused.
	BusyTimeout time.Duration

	// MaxOpenConns bounds the pool. Zero selects four; a negative value is
	// refused. There is no unlimited setting: an unbounded pool against SQLite is
	// a way to turn a slow query into file-descriptor exhaustion.
	MaxOpenConns int

	// ConnMaxIdleTime is how long an idle connection is kept. Zero selects five
	// minutes.
	ConnMaxIdleTime time.Duration
}

// resolved fills the zero fields with their defaults and refuses a negative one.
func (o DatabaseOptions) resolved() (DatabaseOptions, error) {
	if o.BusyTimeout < 0 || o.MaxOpenConns < 0 || o.ConnMaxIdleTime < 0 {
		return DatabaseOptions{}, fmt.Errorf("store: negative database option: %w", ErrInvalidConfig)
	}
	if o.BusyTimeout == 0 {
		o.BusyTimeout = defaultBusyTimeout
	}
	if o.MaxOpenConns == 0 {
		o.MaxOpenConns = defaultMaxOpenConns
	}
	if o.ConnMaxIdleTime == 0 {
		o.ConnMaxIdleTime = defaultConnMaxIdleTime
	}
	return o, nil
}

// OpenDatabase opens the SQLite database at path with the connection settings and
// pragmas this store requires, and applies no schema.
//
// It is exported separately from OpenSQLite because the migrate command needs a
// connection before a store exists, and because the pragma assertions must be able
// to observe a plain connection.
//
// What it guarantees, and how:
//
//   - The parent directory exists and is owner-only, and no component of the path
//     is a symlink. Both go through internal/securefile.
//   - The database file, its write-ahead log and its shared-memory file are
//     owner-only. The driver creates them with a mode the process umask masks, so
//     they are tightened afterwards rather than by changing the umask, which is
//     process-global state this package must not touch.
//   - Every pooled connection runs with journal_mode=WAL, foreign_keys=ON, a busy
//     timeout and synchronous=NORMAL, because the pragmas travel in the DSN and the
//     driver therefore applies them to each connection it opens, not only to the
//     first one.
//   - Write transactions begin immediately (_txlock=immediate) rather than
//     deferring the lock until the first write. A deferred transaction that starts
//     by reading and then writes has to upgrade its lock, and an upgrade cannot
//     wait: SQLite reports SQLITE_BUSY at once instead of honoring the busy
//     timeout. Every compare-and-set in this store reads before it writes, so
//     deferring would make the CAS fail spuriously under concurrency.
func OpenDatabase(path string, opts DatabaseOptions) (*sql.DB, error) {
	settings, err := opts.resolved()
	if err != nil {
		return nil, err
	}
	resolved, err := resolveDatabasePath(path)
	if err != nil {
		return nil, err
	}
	if err := ensureOwnerOnlyDir(filepath.Dir(resolved)); err != nil {
		return nil, err
	}

	db, err := sql.Open(driverName, databaseDSN(resolved, settings))
	if err != nil {
		return nil, fmt.Errorf("store: open database %q: %w", resolved, err)
	}
	db.SetMaxOpenConns(settings.MaxOpenConns)
	db.SetMaxIdleConns(settings.MaxOpenConns)
	db.SetConnMaxIdleTime(settings.ConnMaxIdleTime)

	if err := prepareDatabaseFiles(db, resolved); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return db, nil
}

// prepareDatabaseFiles forces one connection so the files exist, then makes them
// owner-only.
func prepareDatabaseFiles(db *sql.DB, resolved string) error {
	if err := db.Ping(); err != nil {
		return fmt.Errorf("store: reach database %q: %w", resolved, err)
	}
	return restrictDatabaseFiles(resolved)
}

// restrictDatabaseFiles makes the database file and its sidecars owner-only. A
// sidecar that does not exist yet is not an error: SQLite creates the shared-memory
// file only once a second connection needs it.
func restrictDatabaseFiles(resolved string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		err := securefile.RestrictExisting(resolved+suffix, databaseFileMode)
		if err != nil && !errors.Is(err, securefile.ErrNotFound) {
			return translate("restrict", resolved+suffix, err, ErrInsecurePath)
		}
	}
	return nil
}

// resolveDatabasePath expands a leading ~, refuses ~username, refuses a symlinked
// ancestor and returns an absolute path.
func resolveDatabasePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("store: empty database path: %w", ErrInvalidConfig)
	}
	expanded, err := expandHome(path)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("store: resolve database path %q: %w: %w", path, ErrInvalidConfig, err)
	}
	if err := checkPathAncestry(absolute); err != nil {
		return "", err
	}
	return absolute, nil
}

// databaseDSN renders the SQLite URI. The pragmas travel in the DSN so the driver
// replays them on every connection it opens; setting them with a one-off Exec would
// configure whichever pooled connection happened to serve that call.
//
// The path goes through url.URL, so a '?' or a '#' in a directory name cannot end
// the path and inject a parameter.
func databaseDSN(resolved string, opts DatabaseOptions) string {
	query := url.Values{}
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "busy_timeout("+strconv.FormatInt(opts.BusyTimeout.Milliseconds(), 10)+")")
	query.Add("_pragma", "synchronous(NORMAL)")
	query.Set("_txlock", "immediate")

	uri := url.URL{Scheme: "file", Opaque: (&url.URL{Path: filepath.ToSlash(resolved)}).EscapedPath()}
	uri.RawQuery = query.Encode()
	return uri.String()
}

// Querier is the read side of a *sql.DB, a *sql.Conn and a *sql.Tx. Pragmas takes
// it so a caller can ask a specific connection what it is configured with, rather
// than whichever one the pool hands out.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// PragmaState is what a connection reports about itself. It is the answer to "are
// the settings actually applied", asked of the database rather than of the DSN
// string, which proves nothing.
type PragmaState struct {
	// JournalMode is "wal" for a correctly configured connection.
	JournalMode string

	// ForeignKeys reports whether foreign key enforcement is on. SQLite defaults
	// it off, and every ON DELETE CASCADE in the schema depends on it.
	ForeignKeys bool

	// BusyTimeoutMillis is the SQLITE_BUSY wait in milliseconds.
	BusyTimeoutMillis int

	// Synchronous is the durability level: 1 is NORMAL, which is the safe choice
	// under WAL.
	Synchronous int
}

// Pragmas reads the connection settings back out of the database.
func Pragmas(ctx context.Context, q Querier) (PragmaState, error) {
	state := PragmaState{}
	if err := scanPragma(ctx, q, "journal_mode", &state.JournalMode); err != nil {
		return PragmaState{}, err
	}
	var foreignKeys int
	if err := scanPragma(ctx, q, "foreign_keys", &foreignKeys); err != nil {
		return PragmaState{}, err
	}
	state.ForeignKeys = foreignKeys == 1
	if err := scanPragma(ctx, q, "busy_timeout", &state.BusyTimeoutMillis); err != nil {
		return PragmaState{}, err
	}
	if err := scanPragma(ctx, q, "synchronous", &state.Synchronous); err != nil {
		return PragmaState{}, err
	}
	return state, nil
}

// isReadablePragma is the allowlist of pragma names Pragmas may read. A pragma
// name cannot be a bound parameter, so it is concatenated into the statement — and
// a concatenated identifier is only safe if it can never come from outside this
// file. It is a function rather than a map so the allowlist is not package-level
// mutable state.
func isReadablePragma(name string) bool {
	switch name {
	case "journal_mode", "foreign_keys", "busy_timeout", "synchronous":
		return true
	}
	return false
}

func scanPragma(ctx context.Context, q Querier, name string, target any) error {
	if !isReadablePragma(name) {
		return fmt.Errorf("store: pragma %q is not in the allowlist: %w", name, ErrInvalidArgument)
	}
	if err := q.QueryRowContext(ctx, "PRAGMA "+name).Scan(target); err != nil {
		return fmt.Errorf("store: read pragma %s: %w", name, err)
	}
	return nil
}
