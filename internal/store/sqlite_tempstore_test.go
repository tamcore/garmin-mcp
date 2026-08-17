package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestOpenedDatabaseKeepsItsTempStoreInMemory pins a deployment requirement, not a
// tuning preference.
//
// SQLite spills temp b-trees and sorters to TMPDIR by default. The release
// container runs with a read-only root filesystem, so a query needing a disk temp
// store would fail with SQLITE_CANTOPEN at request time — after the readiness probe
// had already reported the process healthy, which is the worst moment to learn about
// a filesystem dependency. temp_store=MEMORY removes the dependency rather than
// asking the operator to mount a writable /tmp and remember why.
//
// It asserts the effective pragma on a real connection rather than the DSN string,
// because a DSN that carries a pragma the driver silently ignored would satisfy a
// string check while the dependency remained. Note that the container CI job mounts
// a tmpfs at /tmp, so it cannot catch this — it would have reported success.
func TestOpenedDatabaseKeepsItsTempStoreInMemory(t *testing.T) {
	dir := tempDir(t)
	db, err := OpenDatabase(filepath.Join(dir, "temp-store.db"), DatabaseOptions{})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer func() { _ = db.Close() }()

	// 0 = default (disk), 1 = FILE, 2 = MEMORY.
	const tempStoreMemory = 2

	// Every pooled connection must carry it, not just the first: the pragmas travel
	// in the DSN precisely so the driver applies them per connection.
	for attempt := range 4 {
		var mode int
		if err := db.QueryRowContext(context.Background(), "PRAGMA temp_store").Scan(&mode); err != nil {
			t.Fatalf("reading PRAGMA temp_store on attempt %d: %v", attempt, err)
		}
		if mode != tempStoreMemory {
			t.Fatalf("PRAGMA temp_store = %d, want %d (MEMORY): a query needing a temp "+
				"store would spill to TMPDIR, which the read-only container has no "+
				"writable path for, and it would fail at request time rather than at "+
				"start-up", mode, tempStoreMemory)
		}
	}

	// And a query that actually drives the sorter must run. No table is needed for
	// that, which keeps this test independent of the migrations.
	var ordered string
	err = db.QueryRowContext(context.Background(),
		`SELECT group_concat(v) FROM (SELECT 2 AS v UNION ALL SELECT 1 ORDER BY v)`,
	).Scan(&ordered)
	if err != nil {
		t.Fatalf("a sorting query failed: %v", err)
	}
	if ordered != "1,2" {
		t.Fatalf("the sorter returned %q, want \"1,2\"", ordered)
	}
}
