package store_test

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/tamcore/garmin-mcp/internal/store"
	"github.com/tamcore/garmin-mcp/migrations"
)

// migrationFS builds a synthetic migration set. Using an in-memory set rather
// than the embedded one is what makes the failure paths testable: the shipped
// migrations must never contain a deliberately broken statement.
func migrationFS(files map[string]string) fstest.MapFS {
	set := fstest.MapFS{}
	for name, body := range files {
		set[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return set
}

// tableExists reports whether name is a table in db.
func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	row := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("query sqlite_master for %q: %v", name, err)
	}
	return count > 0
}

func TestMigrateAppliesTheEmbeddedSetAndReportsItsVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)

	result, err := store.Migrate(ctx, db, migrations.FS())
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if result.FromVersion != 0 {
		t.Errorf("FromVersion = %d, want 0 for an empty database", result.FromVersion)
	}
	if result.ToVersion < 1 {
		t.Errorf("ToVersion = %d, want at least 1", result.ToVersion)
	}
	if len(result.Applied) != result.ToVersion {
		t.Errorf("applied %d migrations to reach version %d", len(result.Applied), result.ToVersion)
	}

	for _, table := range []string{
		"schema_migrations", "schema_meta", "principals", "garmin_token_sets",
		"oauth_clients", "oauth_client_redirect_uris", "consents",
		"auth_transactions", "auth_codes", "token_families", "mcp_tokens",
		"audit_events",
	} {
		if !tableExists(t, db, table) {
			t.Errorf("table %q is missing after Migrate", table)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)

	first, err := store.Migrate(ctx, db, migrations.FS())
	if err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	second, err := store.Migrate(ctx, db, migrations.FS())
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if len(second.Applied) != 0 {
		t.Errorf("second Migrate applied %d migrations, want 0", len(second.Applied))
	}
	if second.FromVersion != first.ToVersion || second.ToVersion != first.ToVersion {
		t.Errorf("second Migrate moved %d -> %d, want %d -> %d",
			second.FromVersion, second.ToVersion, first.ToVersion, first.ToVersion)
	}
}

// TestFailingMigrationIsAtomic is the atomicity requirement. The second migration
// creates a table and then runs an invalid statement. Nothing from that migration
// may survive, and the recorded version must stay at the last good one.
func TestFailingMigrationIsAtomic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)

	set := migrationFS(map[string]string{
		"0001_good.sql": `CREATE TABLE kept (id INTEGER PRIMARY KEY) STRICT;`,
		"0002_bad.sql": `CREATE TABLE half_applied (id INTEGER PRIMARY KEY) STRICT;
			INSERT INTO table_that_does_not_exist (id) VALUES (1);`,
	})

	_, err := store.Migrate(ctx, db, set)
	if !errors.Is(err, store.ErrMigrationFailed) {
		t.Fatalf("Migrate: err = %v, want ErrMigrationFailed", err)
	}
	if !tableExists(t, db, "kept") {
		t.Error("migration 0001 was rolled back, but only 0002 failed")
	}
	if tableExists(t, db, "half_applied") {
		t.Error("the failed migration left half_applied behind: it was not atomic")
	}

	version, err := store.SchemaVersion(ctx, db)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if version != 1 {
		t.Errorf("SchemaVersion = %d, want 1: the failed migration must not be recorded", version)
	}
}

// TestMigrateRefusesADatabaseNewerThanTheBinary is the version-compatibility
// requirement: an older binary pointed at a database a newer one migrated must
// refuse, not run against a schema it does not understand.
func TestMigrateRefusesADatabaseNewerThanTheBinary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)

	newer := migrationFS(map[string]string{
		migrationFileOne: migrationBodyOne,
		"0002_b.sql":     `CREATE TABLE b (id INTEGER PRIMARY KEY) STRICT;`,
	})
	if _, err := store.Migrate(ctx, db, newer); err != nil {
		t.Fatalf("Migrate with the newer set: %v", err)
	}

	older := migrationFS(map[string]string{
		migrationFileOne: migrationBodyOne,
	})
	_, err := store.Migrate(ctx, db, older)
	if !errors.Is(err, store.ErrSchemaTooNew) {
		t.Fatalf("Migrate with the older set: err = %v, want ErrSchemaTooNew", err)
	}
}

// TestMigrateRefusesAnAlteredAppliedMigration keeps the set forward-only: editing
// a file that already ran would leave every existing database on a schema this
// build cannot reproduce.
func TestMigrateRefusesAnAlteredAppliedMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)

	original := migrationFS(map[string]string{
		migrationFileOne: migrationBodyOne,
	})
	if _, err := store.Migrate(ctx, db, original); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	altered := migrationFS(map[string]string{
		"0001_a.sql": `CREATE TABLE a (id INTEGER PRIMARY KEY, extra TEXT) STRICT;`,
	})
	_, err := store.Migrate(ctx, db, altered)
	if !errors.Is(err, store.ErrMigrationChanged) {
		t.Fatalf("Migrate with an edited 0001: err = %v, want ErrMigrationChanged", err)
	}
}

func TestMigrateRefusesAMalformedSet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cases := map[string]map[string]string{
		"a gap in the sequence": {
			migrationFileOne: migrationBodyOne,
			"0003_c.sql":     `CREATE TABLE c (id INTEGER PRIMARY KEY) STRICT;`,
		},
		"a duplicated version": {
			migrationFileOne: migrationBodyOne,
			"0001_b.sql":     `CREATE TABLE b (id INTEGER PRIMARY KEY) STRICT;`,
		},
		"a version below one": {
			"0000_zero.sql": `CREATE TABLE z (id INTEGER PRIMARY KEY) STRICT;`,
		},
		"an unparsable name": {
			"initial.sql": `CREATE TABLE a (id INTEGER PRIMARY KEY) STRICT;`,
		},
		"an empty set": {},
	}

	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := store.Migrate(ctx, newTestDB(t), migrationFS(files))
			if !errors.Is(err, store.ErrMigrationSet) {
				t.Fatalf("Migrate with %s: err = %v, want ErrMigrationSet", name, err)
			}
		})
	}
}

// TestEmbeddedMigrationSetIsWellFormed guards the shipped set with the same rules
// the synthetic cases above exercise.
func TestEmbeddedMigrationSetIsWellFormed(t *testing.T) {
	t.Parallel()

	set, err := store.LoadMigrations(migrations.FS())
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	if len(set) == 0 {
		t.Fatal("the embedded migration set is empty")
	}
	for index, migration := range set {
		if migration.Version != index+1 {
			t.Errorf("migration %d has version %d, want %d", index, migration.Version, index+1)
		}
		if migration.Name == "" {
			t.Errorf("migration %d has an empty name", migration.Version)
		}
		if migration.Checksum == "" {
			t.Errorf("migration %d has an empty checksum", migration.Version)
		}
	}
}

// TestMigratingAnExistingDatabaseKeepsItsConsents is the upgrade path a developer's
// database takes. 0002 rebuilds consents to widen the key, and a rebuild that lost a row
// would silently drop a grant. Every migrated row must land on the empty redirect URI and
// the empty resource, which is what a row written under the narrow key meant.
func TestMigratingAnExistingDatabaseKeepsItsConsents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)

	initial, err := fs.ReadFile(migrations.FS(), "0001_initial.sql")
	if err != nil {
		t.Fatalf("read the initial migration: %v", err)
	}
	if _, err := store.Migrate(ctx, db,
		migrationFS(map[string]string{"0001_initial.sql": string(initial)})); err != nil {
		t.Fatalf("Migrate to the initial version: %v", err)
	}

	seedNarrowConsent(t, db)

	result, err := store.Migrate(ctx, db, migrations.FS())
	if err != nil {
		t.Fatalf("Migrate an existing database: %v", err)
	}
	if result.FromVersion != 1 || result.ToVersion < 2 {
		t.Fatalf("migrated %d -> %d, want 1 -> at least 2", result.FromVersion, result.ToVersion)
	}

	var (
		redirectURI string
		resource    string
		scopes      string
	)
	err = db.QueryRowContext(ctx,
		`SELECT redirect_uri, resource, scopes FROM consents WHERE principal_id = ?`,
		testUnknownID).Scan(&redirectURI, &resource, &scopes)
	if err != nil {
		t.Fatalf("the migrated consent is gone: %v", err)
	}
	if redirectURI != "" || resource != "" || scopes != testScope {
		t.Fatalf("migrated consent = redirect %q resource %q scopes %q, want empty, empty and %q",
			redirectURI, resource, scopes, testScope)
	}
}

// seedNarrowConsent writes the rows a 0001 database would hold: one principal, one
// client, and one consent under the narrow key.
func seedNarrowConsent(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO principals (id, email_normalized, key_version, created_at, updated_at)
		  VALUES (?, ?, 1, ?, ?)`,
			[]any{testUnknownID, testEmailNormalized, migrationInstant, migrationInstant}},
		{`INSERT INTO oauth_clients (id, name, is_public, created_at)
		  VALUES (?, ?, 1, ?)`,
			[]any{"legacy-client", testClientName, migrationInstant}},
		{`INSERT INTO consents (principal_id, client_id, scopes, granted_at)
		  VALUES (?, ?, ?, ?)`,
			[]any{testUnknownID, "legacy-client", testScope, migrationInstant}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed a 0001 row: %v", err)
		}
	}
}

// migrationInstant is a fixed timestamp in the format every table in the schema uses.
const migrationInstant = "2026-08-14T12:00:00Z"
