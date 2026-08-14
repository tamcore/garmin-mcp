package migrations_test

import (
	"io/fs"
	"regexp"
	"strconv"
	"testing"

	"github.com/tamcore/garmin-mcp/migrations"
)

// The rules the package documents, asserted here so a badly named or empty file fails the
// build of this package rather than at start-up on an operator's machine. The migrator in
// internal/store checks the same rules against a database; this is the cheap guard that
// needs no database at all.

func TestEmbeddedSetIsNamedAndOrderedCorrectly(t *testing.T) {
	t.Parallel()

	names, err := fs.Glob(migrations.FS(), "*.sql")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no migrations are embedded")
	}

	pattern := regexp.MustCompile(`^(\d{4})_[a-z0-9]+(?:_[a-z0-9]+)*\.sql$`)
	seen := map[int]string{}
	for _, name := range names {
		groups := pattern.FindStringSubmatch(name)
		if groups == nil {
			t.Errorf("migration %q is not NNNN_snake_case_name.sql", name)
			continue
		}
		version, err := strconv.Atoi(groups[1])
		if err != nil || version < 1 {
			t.Errorf("migration %q has an unusable version", name)
			continue
		}
		if previous, duplicate := seen[version]; duplicate {
			t.Errorf("version %d is used by both %q and %q", version, previous, name)
		}
		seen[version] = name
	}

	// Monotonic with no gap: versions 1..n must all be present.
	for version := 1; version <= len(seen); version++ {
		if _, ok := seen[version]; !ok {
			t.Errorf("version %d is missing from the sequence", version)
		}
	}
}

func TestEmbeddedMigrationsAreReadableAndNonEmpty(t *testing.T) {
	t.Parallel()

	names, err := fs.Glob(migrations.FS(), "*.sql")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	for _, name := range names {
		body, err := fs.ReadFile(migrations.FS(), name)
		if err != nil {
			t.Errorf("read %q: %v", name, err)
			continue
		}
		if len(body) == 0 {
			t.Errorf("migration %q is empty", name)
		}
	}
}

// TestFSExposesTheInitialMigration: the set is handed out as an fs.FS so a caller cannot
// reach the embed.FS methods, and every call must see the same immutable content.
func TestFSExposesTheInitialMigration(t *testing.T) {
	t.Parallel()

	for _, set := range []fs.FS{migrations.FS(), migrations.FS()} {
		if _, err := fs.Stat(set, "0001_initial.sql"); err != nil {
			t.Fatalf("the initial migration is not embedded: %v", err)
		}
	}
}
