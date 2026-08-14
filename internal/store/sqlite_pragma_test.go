package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// The pragma assertions ask the database what it is configured with. Asserting on the
// DSN string would only prove that the string was built, not that SQLite accepted it,
// and journal_mode in particular can silently stay "delete" — for an in-memory
// database, for instance.

func TestOpenDatabaseAppliesWALForeignKeysAndBusyTimeout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)

	state, err := store.Pragmas(ctx, db)
	if err != nil {
		t.Fatalf("Pragmas: %v", err)
	}
	if state.JournalMode != journalModeWAL {
		t.Errorf("journal_mode = %q, want %q", state.JournalMode, journalModeWAL)
	}
	if !state.ForeignKeys {
		t.Error("foreign_keys is off; every ON DELETE CASCADE in the schema depends on it")
	}
	if state.BusyTimeoutMillis != 5000 {
		t.Errorf("busy_timeout = %d ms, want the 5000 ms default", state.BusyTimeoutMillis)
	}
	if state.Synchronous != 1 {
		t.Errorf("synchronous = %d, want 1 (NORMAL)", state.Synchronous)
	}
}

// TestEveryPooledConnectionCarriesThePragmas is the reason the pragmas travel in the
// DSN instead of a one-off Exec. A one-off Exec configures whichever connection served
// that call; every later connection the pool opens would run with foreign keys off.
func TestEveryPooledConnectionCarriesThePragmas(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)

	const connections = 4
	// Every connection is held open until all of them have been checked, so the pool
	// is forced to open distinct connections rather than handing out one repeatedly.
	var ready sync.WaitGroup
	var release sync.WaitGroup
	ready.Add(connections)
	release.Add(1)

	failures := make(chan string, connections)
	var group sync.WaitGroup
	for range connections {
		group.Go(func() {
			defer release.Wait()
			conn, err := db.Conn(ctx)
			if err != nil {
				failures <- "open connection: " + err.Error()
				ready.Done()
				return
			}
			defer func() { _ = conn.Close() }()

			state, err := store.Pragmas(ctx, conn)
			switch {
			case err != nil:
				failures <- "read pragmas: " + err.Error()
			case state.JournalMode != journalModeWAL:
				failures <- "journal_mode = " + state.JournalMode
			case !state.ForeignKeys:
				failures <- "foreign_keys is off on a pooled connection"
			case state.BusyTimeoutMillis != 5000:
				failures <- "busy_timeout is not 5000 ms on a pooled connection"
			}
			ready.Done()
		})
	}
	ready.Wait()
	release.Done()
	group.Wait()
	close(failures)

	for failure := range failures {
		t.Error(failure)
	}
}

func TestOpenDatabaseHonorsAConfiguredBusyTimeout(t *testing.T) {
	t.Parallel()

	db, err := store.OpenDatabase(testDBPath(t), store.DatabaseOptions{
		BusyTimeout:  2500 * time.Millisecond,
		MaxOpenConns: 2,
	})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	state, err := store.Pragmas(context.Background(), db)
	if err != nil {
		t.Fatalf("Pragmas: %v", err)
	}
	if state.BusyTimeoutMillis != 2500 {
		t.Errorf("busy_timeout = %d ms, want 2500", state.BusyTimeoutMillis)
	}
	if got := db.Stats().MaxOpenConnections; got != 2 {
		t.Errorf("MaxOpenConnections = %d, want the configured 2", got)
	}
}

func TestOpenDatabaseRefusesBadConfiguration(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		path string
		opts store.DatabaseOptions
	}{
		"an empty path":            {path: "", opts: store.DatabaseOptions{}},
		"another user's home":      {path: "~someoneelse/db.sqlite", opts: store.DatabaseOptions{}},
		"a negative busy timeout":  {path: casePathIgnored, opts: store.DatabaseOptions{BusyTimeout: -1}},
		"a negative pool bound":    {path: casePathIgnored, opts: store.DatabaseOptions{MaxOpenConns: -1}},
		"a negative idle lifetime": {path: casePathIgnored, opts: store.DatabaseOptions{ConnMaxIdleTime: -1}},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			db, err := store.OpenDatabase(testCase.path, testCase.opts)
			if db != nil {
				t.Cleanup(func() { _ = db.Close() })
			}
			if err == nil {
				t.Fatalf("OpenDatabase with %s returned no error", name)
			}
			if !errors.Is(err, store.ErrInvalidConfig) && !errors.Is(err, store.ErrForeignHomePath) {
				t.Fatalf("err = %v, want ErrInvalidConfig or ErrForeignHomePath", err)
			}
		})
	}
}

// TestStorePragmasAndVersions covers the metadata a caller and an operator need: the
// schema version the store opened at, the encryption key version recorded in the
// database, and the pragmas as seen through the store rather than a bare connection.
func TestStorePragmasAndVersions(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	if got := opened.SchemaVersion(); got < 1 {
		t.Errorf("SchemaVersion() = %d, want at least 1", got)
	}
	keyVersion, err := opened.EncryptionKeyVersion(ctx)
	if err != nil {
		t.Fatalf("EncryptionKeyVersion: %v", err)
	}
	if keyVersion != 1 {
		t.Errorf("EncryptionKeyVersion() = %d, want 1 for the test key", keyVersion)
	}

	state, err := opened.Pragmas(ctx)
	if err != nil {
		t.Fatalf("Pragmas: %v", err)
	}
	if state.JournalMode != journalModeWAL || !state.ForeignKeys {
		t.Errorf("store pragmas = %+v, want wal with foreign keys on", state)
	}
}

// TestForeignKeysAreEnforcedInPractice is the behavioral counterpart to the pragma
// read: a row referencing a principal that does not exist must be refused.
func TestForeignKeysAreEnforcedInPractice(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()

	_, err := opened.Save(ctx, testUnknownID, newSQLTestTokens(), 0)
	if !errors.Is(err, store.ErrPrincipalNotFound) {
		t.Fatalf("Save for an unknown principal: err = %v, want ErrPrincipalNotFound", err)
	}
}
