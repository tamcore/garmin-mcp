package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// flagPrincipal is the one selector unlink and revoke accept. There is no "all"
// and no default: a destructive command that can be run with no arguments is a
// mistake waiting to happen, and the operator names exactly the principal a
// data-deletion request identified.
const flagPrincipal = "principal"

// openRevocationStore opens the multi-user database the way serve does: through
// the same resolved state paths and the same key ring, never a guessed
// location and never a freshly-minted key when none exists to mint for. Both
// unlink and revoke need it, because both operations are SQLiteStore-only —
// neither has a FileStore counterpart, the same way a local stdio deployment
// has no unlink or revoke command either.
func openRevocationStore(ctx context.Context, cfg config.Config) (*store.SQLiteStore, error) {
	if cfg.DatabasePath == "" {
		return nil, fmt.Errorf("%w: set database-path to the database this deployment "+
			"owns; unlink and revoke never guess a location", ErrNoDatabasePath)
	}

	paths, err := resolveStatePaths(cfg)
	if err != nil {
		return nil, err
	}
	active, retired, err := loadKeyRing(paths)
	if err != nil {
		return nil, err
	}

	sqlite, err := store.OpenSQLite(ctx, store.SQLiteConfig{
		Path:        cfg.DatabasePath,
		Key:         active,
		RetiredKeys: retired,
	})
	if err != nil {
		return nil, fmt.Errorf("opening the database: %w", err)
	}
	return sqlite, nil
}

// parsePrincipalFlag reads and validates --principal. Validation goes through
// identity.NewPrincipal, the same check every principal identifier in this
// server passes, so an email typed into this flag by mistake is refused here
// rather than silently treated as an opaque id.
func parsePrincipalFlag(cmd *cobra.Command) (string, error) {
	raw, err := cmd.Flags().GetString(flagPrincipal)
	if err != nil {
		return "", fmt.Errorf("reading --%s: %w", flagPrincipal, err)
	}
	principal, err := identity.NewPrincipal(raw)
	if err != nil {
		return "", fmt.Errorf("--%s: %w", flagPrincipal, err)
	}
	return principal.ID(), nil
}

// registerPrincipalFlag adds the required --principal flag, shared verbatim by
// unlink and revoke so their help text and required-flag behavior cannot drift
// apart.
func registerPrincipalFlag(command *cobra.Command) {
	command.Flags().String(flagPrincipal, "",
		"the principal to act on; there is no default and no \"all\" (required)")
	_ = command.MarkFlagRequired(flagPrincipal)
}
