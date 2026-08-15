package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/store"
	"github.com/tamcore/garmin-mcp/migrations"
)

// NewMigrateCommand applies the embedded, forward-only database migrations.
//
// It is the one command that changes a database without serving anything, so it is
// deliberately narrow: it opens the configured database through the same hardened
// path the server uses, applies whatever the embedded set adds, and reports the
// version before and after. It reads no key material, contacts Garmin not at all,
// and stores no record of its own.
//
// Running it twice is a supported operation. The second run applies nothing and
// still succeeds, which is what makes it safe in a start-up script or an init
// container.
func NewMigrateCommand(opts Options) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply database migrations",
		Long: "Apply the embedded, forward-only schema migrations to the configured\n" +
			"database, and report the schema version before and after.\n\n" +
			"The command is idempotent: a database that is already current is left\n" +
			"untouched. There are no down migrations; roll back by restoring a backup.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			return runMigrate(cmd.Context(), cfg, opts.stdout())
		},
	}
}

// runMigrate opens the database, migrates it, and reports what it did.
//
// Nothing reaches out before the database is open and migrated, so a refused run
// leaves the operator with the refusal and nothing that reads like progress.
func runMigrate(ctx context.Context, cfg config.Config, out io.Writer) error {
	if cfg.DatabasePath == "" {
		return fmt.Errorf("%w: set database-path to the database this deployment "+
			"owns; migrate never guesses a location", ErrNoDatabasePath)
	}

	db, err := store.OpenDatabase(cfg.DatabasePath, store.DatabaseOptions{})
	if err != nil {
		return fmt.Errorf("opening the database to migrate: %w", err)
	}
	defer func() { _ = db.Close() }()

	result, err := applyMigrations(ctx, db)
	if err != nil {
		return err
	}
	writeMigrationReport(out, cfg.DatabasePath, result)
	return nil
}

// applyMigrations runs the embedded set and reads the resulting version back out
// of the database.
//
// The version after is read rather than taken from the migrator's own result,
// because the report is a statement about the database and not about what the
// migrator believed it did.
func applyMigrations(ctx context.Context, db *sql.DB) (store.MigrationResult, error) {
	before, err := store.SchemaVersion(ctx, db)
	if err != nil {
		return store.MigrationResult{}, fmt.Errorf("reading the schema version: %w", err)
	}

	result, err := store.Migrate(ctx, db, migrations.FS())
	if err != nil {
		return store.MigrationResult{}, fmt.Errorf("applying the migrations: %w", err)
	}

	after, err := store.SchemaVersion(ctx, db)
	if err != nil {
		return store.MigrationResult{}, fmt.Errorf("reading the schema version: %w", err)
	}
	return store.MigrationResult{
		FromVersion: before,
		ToVersion:   after,
		Applied:     result.Applied,
	}, nil
}

// writeMigrationReport states what the run did, in the order an operator reads it.
//
// The database path is the only value echoed. It is a configured location, which
// the diagnostic report already prints; no key, token, or row content is reachable
// from here at all.
func writeMigrationReport(out io.Writer, path string, result store.MigrationResult) {
	_, _ = fmt.Fprintf(out, "database: %s\n", path)
	_, _ = fmt.Fprintf(out, "schema version before: %d\n", result.FromVersion)

	if len(result.Applied) == 0 {
		_, _ = fmt.Fprintln(out, "no migration was pending; the schema is already current")
	}
	for _, migration := range result.Applied {
		_, _ = fmt.Fprintf(out, "applied migration %04d (%s)\n", migration.Version, migration.Name)
	}

	_, _ = fmt.Fprintf(out, "schema version after: %d\n", result.ToVersion)
}
