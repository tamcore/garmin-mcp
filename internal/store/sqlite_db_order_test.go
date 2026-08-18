package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// TestOpenDatabaseRefusesAPreExistingSymlinkedFileBeforeTouchingItsBytes is gap
// 3: OpenDatabase used to open and Ping the SQLite driver before validating a
// pre-existing file, so an insecure file's bytes were consumed (the driver
// parses the SQLite header, and can replay a WAL) ahead of any path check.
//
// A planted symlink is the property this package refuses rather than silently
// repairs (unlike a bare permission bit, which restrictDatabaseFiles tightens
// the same way it already tightens a SQLite-created sidecar's umask-masked
// mode): a symlinked database path is redirection, not a mode that can be
// fixed with a chmod, so it must be refused outright, and it is constructible
// in an unprivileged test with os.Symlink.
//
// The planted content is deliberately not a valid SQLite file. That is the
// proof, not just a convenience: if the driver ever got as far as opening a
// connection to it, modernc.org/sqlite would report a parse/format failure
// ("file is not a database") rather than this package's path sentinel.
// Asserting the sentinel AND the absence of that wording together shows the
// check ran, and refused, before the driver ever touched the file — "an error
// came back" alone would not distinguish the fixed order from the broken one,
// since both eventually fail one way or another.
func TestOpenDatabaseRefusesAPreExistingSymlinkedFileBeforeTouchingItsBytes(t *testing.T) {
	t.Parallel()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	target := filepath.Join(resolved, "elsewhere.db")
	// Not a SQLite file: no "SQLite format 3" header, just garbage bytes.
	if err := os.WriteFile(target, []byte("not a sqlite database, just garbage bytes"), 0o600); err != nil {
		t.Fatalf("plant a non-database file: %v", err)
	}
	path := filepath.Join(resolved, "garmin-mcp.db")
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	_, openErr := store.OpenDatabase(path, store.DatabaseOptions{})

	if !errors.Is(openErr, store.ErrInsecurePath) {
		t.Fatalf("OpenDatabase on a symlinked pre-existing file: err = %v, want ErrInsecurePath", openErr)
	}
	assertNoSQLiteParseWording(t, openErr)
}

// TestOpenDatabaseTightensAPreExistingPermissiveFileBeforeOpeningIt proves the
// reorder does not regress the ordinary reopen path: a real database this
// process created earlier, whose mode was later widened (the same shape as
// gap 4's directory case, but for the file), must still be tightened and must
// still open, and the tightening must happen before the driver is handed the
// file.
func TestOpenDatabaseTightensAPreExistingPermissiveFileBeforeOpeningIt(t *testing.T) {
	t.Parallel()
	path := testDBPath(t)

	created, err := store.OpenDatabase(path, store.DatabaseOptions{})
	if err != nil {
		t.Fatalf("create the database: %v", err)
	}
	if closeErr := created.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
	//nolint:gosec // deliberately widened, this is the case under test
	if chmodErr := os.Chmod(path, 0o644); chmodErr != nil {
		t.Fatalf("widen the database file's mode: %v", chmodErr)
	}

	reopened, err := store.OpenDatabase(path, store.DatabaseOptions{})
	if err != nil {
		t.Fatalf("reopen a real database with a widened mode: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := reopened.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %04o, want tightened back to 0600", perm)
	}
}

// TestOpenDatabaseStillCreatesAFreshDatabase is the companion negative control:
// the pre-existing-file check must not fire, and must not regress ordinary
// creation, when nothing is there yet.
func TestOpenDatabaseStillCreatesAFreshDatabase(t *testing.T) {
	t.Parallel()
	path := testDBPath(t)

	db, err := store.OpenDatabase(path, store.DatabaseOptions{})
	if err != nil {
		t.Fatalf("OpenDatabase on a fresh path: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat created database: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %04o, want 0600", perm)
	}
}

func assertNoSQLiteParseWording(t *testing.T, err error) {
	t.Helper()
	lower := strings.ToLower(err.Error())
	for _, sqliteParseWording := range []string{"malformed", "not a database", "file is encrypted"} {
		if strings.Contains(lower, sqliteParseWording) {
			t.Fatalf("err = %q contains SQLite parse-failure wording %q: the driver reached the file's "+
				"bytes before the check ran", err, sqliteParseWording)
		}
	}
}
