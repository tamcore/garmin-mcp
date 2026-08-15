package cmd_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/cmd"
	"github.com/tamcore/garmin-mcp/internal/store"
	"github.com/tamcore/garmin-mcp/migrations"
)

// databaseFlag is the setting migrate reads the database location from.
const databaseFlag = "--database-path="

// migrateTarget returns a database path inside a directory no symlink leads to.
//
// The ancestry is resolved because the store refuses a symlinked ancestor, and on
// macOS t.TempDir() sits under /var, which is a symlink to /private/var. Without
// this every migrate test would fail on that platform for a reason that has
// nothing to do with migrate.
func migrateTarget(t *testing.T) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the temporary directory: %v", err)
	}
	return filepath.Join(resolved, "state.db")
}

// schemaVersionOf reports the version recorded in the database at path, read
// through the same store the command uses.
func schemaVersionOf(t *testing.T, path string) int {
	t.Helper()

	db, err := store.OpenDatabase(path, store.DatabaseOptions{})
	if err != nil {
		t.Fatalf("opening the migrated database: %v", err)
	}
	defer func() { _ = db.Close() }()

	version, err := store.SchemaVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("reading the schema version: %v", err)
	}
	return version
}

// latestMigrationVersion is the highest version the embedded set carries.
func latestMigrationVersion(t *testing.T) int {
	t.Helper()

	set, err := store.LoadMigrations(migrations.FS())
	if err != nil {
		t.Fatalf("loading the embedded migrations: %v", err)
	}
	return set[len(set)-1].Version
}

// TestMigrateAppliesTheEmbeddedSchema is the behavior the command exists for: an
// empty location becomes a database at the version this build carries, and the
// operator is told what happened.
func TestMigrateAppliesTheEmbeddedSchema(t *testing.T) {
	clearGarminEnv(t)
	path := migrateTarget(t)
	wantVersion := latestMigrationVersion(t)

	stdout, err := runCommand(t, cmdMigrate, databaseFlag+path)
	if err != nil {
		t.Fatalf("migrate = %v, want it to apply the schema", err)
	}

	if got := schemaVersionOf(t, path); got != wantVersion {
		t.Errorf("schema version = %d, want %d", got, wantVersion)
	}
	for _, want := range []string{path, "applied"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to mention %q", stdout, want)
		}
	}
}

// TestMigrateIsSafeToRunTwice fixes idempotency, which is what makes migrate
// usable from a start-up script: the second run applies nothing, reports the same
// version before and after, and still succeeds.
func TestMigrateIsSafeToRunTwice(t *testing.T) {
	clearGarminEnv(t)
	path := migrateTarget(t)

	if _, err := runCommand(t, cmdMigrate, databaseFlag+path); err != nil {
		t.Fatalf("first migrate = %v, want success", err)
	}
	first := schemaVersionOf(t, path)

	stdout, err := runCommand(t, cmdMigrate, databaseFlag+path)
	if err != nil {
		t.Fatalf("second migrate = %v, want success", err)
	}

	if second := schemaVersionOf(t, path); second != first {
		t.Errorf("schema version = %d after the second run, want %d", second, first)
	}
	if strings.Contains(stdout, "applied") {
		t.Errorf("stdout = %q, want no applied migration on the second run", stdout)
	}
	if !strings.Contains(stdout, "already current") {
		t.Errorf("stdout = %q, want it to report that nothing was left to do", stdout)
	}
}

// TestMigrateReportsTheVersionBeforeAndAfter keeps the report auditable: an
// operator has to see which versions this run moved the database between, not only
// that it succeeded.
func TestMigrateReportsTheVersionBeforeAndAfter(t *testing.T) {
	clearGarminEnv(t)

	stdout, err := runCommand(t, cmdMigrate, databaseFlag+migrateTarget(t))
	if err != nil {
		t.Fatalf("migrate = %v, want success", err)
	}

	for _, want := range []string{"schema version before", "schema version after"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to report %q", stdout, want)
		}
	}
}

// TestMigrateRefusesWhenNoDatabaseIsNamed is the "no silent creation" rule: with
// no configured path there is no safe default, so the command refuses and names
// the setting instead of creating a database wherever it happens to run.
func TestMigrateRefusesWhenNoDatabaseIsNamed(t *testing.T) {
	clearGarminEnv(t)

	stdout, err := runCommand(t, cmdMigrate)

	if !errors.Is(err, cmd.ErrNoDatabasePath) {
		t.Fatalf("error = %v, want cmd.ErrNoDatabasePath", err)
	}
	if !strings.Contains(err.Error(), "database-path") {
		t.Errorf("error %q does not name the setting to fix", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing before the refusal", stdout)
	}
	assertNoDatabaseInWorkingDirectory(t)
}

// assertNoDatabaseInWorkingDirectory fails when a refused run left a database
// behind where the command happened to be started.
func assertNoDatabaseInWorkingDirectory(t *testing.T) {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the working directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".db") {
			t.Fatalf("migrate created %q in the working directory", entry.Name())
		}
	}
}

// TestMigrateRefusesAnUnsafeDatabasePath keeps the security rule with the command:
// a path reached through a symlinked ancestor is refused rather than migrated,
// because the symlink, and not the operator, decides where the file lands.
func TestMigrateRefusesAnUnsafeDatabasePath(t *testing.T) {
	clearGarminEnv(t)

	target := filepath.Dir(migrateTarget(t))
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this platform does not allow a symlink: %v", err)
	}

	_, err := runCommand(t, cmdMigrate, databaseFlag+filepath.Join(link, "state.db"))

	if !errors.Is(err, store.ErrInsecurePath) {
		t.Fatalf("error = %v, want store.ErrInsecurePath", err)
	}
}

// TestMigrateValidatesConfigurationFirst keeps a misconfigured deployment
// distinguishable from a migration failure.
func TestMigrateValidatesConfigurationFirst(t *testing.T) {
	clearGarminEnv(t)

	_, err := runCommand(t, cmdMigrate,
		databaseFlag+migrateTarget(t), "--log-level=trace")

	if err == nil {
		t.Fatal("an invalid log level was accepted")
	}
	if errors.Is(err, cmd.ErrNoDatabasePath) {
		t.Errorf("error = %v, want the configuration fault", err)
	}
}

// TestMigrateLeavesTheDatabaseUsableByTheStore is the end of the chain: what
// migrate applied has to be what the store expects, so re-running the migrator
// afterwards must neither fail nor apply anything.
func TestMigrateLeavesTheDatabaseUsableByTheStore(t *testing.T) {
	clearGarminEnv(t)
	path := migrateTarget(t)

	if _, err := runCommand(t, cmdMigrate, databaseFlag+path); err != nil {
		t.Fatalf("migrate = %v, want success", err)
	}

	db, err := store.OpenDatabase(path, store.DatabaseOptions{})
	if err != nil {
		t.Fatalf("reopening the database: %v", err)
	}
	defer func() { _ = db.Close() }()

	result, err := store.Migrate(context.Background(), db, migrations.FS())
	if err != nil {
		t.Fatalf("re-running the migrator: %v", err)
	}
	if len(result.Applied) != 0 {
		t.Errorf("the migrator applied %d migrations after migrate ran", len(result.Applied))
	}
	assertTableExists(t, db, "schema_migrations")
}

// assertTableExists fails when the named table is missing.
func assertTableExists(t *testing.T, db *sql.DB, table string) {
	t.Helper()

	var name string
	err := db.QueryRowContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	if err != nil {
		t.Fatalf("table %q is missing: %v", table, err)
	}
}
