package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// TestRevocationReportsDatabaseBusyUnderRealContention is the CLI's own failure
// mode from AGENTS.md's revoke/unlink contention rule, reproduced for real: a
// second writer (a raw connection, standing in for a live "serve") holds the
// write lock, and a store opened with a short busy timeout must fail with
// store.ErrDatabaseBusy — a clean, named error rather than a raw driver string
// — leaving nothing partially applied.
func TestRevocationReportsDatabaseBusyUnderRealContention(t *testing.T) {
	t.Parallel()
	path, key := testDBPath(t), testKey(t)
	ctx := context.Background()

	// First connection: migrates the schema, then closes so the raw contending
	// connection below is the only thing on the file when it grabs the lock.
	seed, err := store.OpenSQLite(ctx, store.SQLiteConfig{Path: path, Key: key})
	if err != nil {
		t.Fatalf("OpenSQLite (seed): %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("closing the seed store: %v", err)
	}

	// The contending writer: a raw connection that opens an immediate
	// transaction and never commits it, exactly what a live server's own
	// in-flight write looks like from the outside.
	rawDB, err := store.OpenDatabase(path, store.DatabaseOptions{})
	if err != nil {
		t.Fatalf("OpenDatabase (contender): %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	held, err := rawDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx (contender): %v", err)
	}
	t.Cleanup(func() { _ = held.Rollback() })
	if _, err := held.ExecContext(ctx, "PRAGMA user_version"); err != nil {
		t.Fatalf("touching the held transaction: %v", err)
	}

	// The command's own store: a short busy timeout so the test does not wait
	// out the production five-second default.
	//
	// OpenSQLite itself writes (its schema-metadata install runs on every open,
	// even against an already-migrated database), so the contention can
	// legitimately surface there instead of at RevokePrincipalTokens — either is
	// the property under test: whichever step hits the lock must report
	// ErrDatabaseBusy, not a raw driver string.
	contended, err := store.OpenSQLite(ctx, store.SQLiteConfig{
		Path:     path,
		Key:      key,
		Database: store.DatabaseOptions{BusyTimeout: 50 * time.Millisecond},
	})
	if err != nil {
		if !errors.Is(err, store.ErrDatabaseBusy) {
			t.Fatalf("OpenSQLite (contended) = %v, want it to wrap ErrDatabaseBusy", err)
		}
		return
	}
	t.Cleanup(func() { _ = contended.Close() })

	_, err = contended.RevokePrincipalTokens(ctx, "some-principal-id")
	if err == nil {
		t.Fatal("RevokePrincipalTokens succeeded while the write lock was held, want ErrDatabaseBusy")
	}
	if !errors.Is(err, store.ErrDatabaseBusy) {
		t.Errorf("err = %v, want it to wrap ErrDatabaseBusy", err)
	}
}
