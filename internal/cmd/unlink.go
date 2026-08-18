package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// NewUnlinkCommand removes one principal's Garmin account linkage.
//
// It answers the "delete my Garmin link" half of a data-deletion request
// without direct database access: an operator names a principal and this
// command performs the store's UnlinkGarminAccount cascade against it.
//
// It never revokes anything at Garmin. The Garmin DI refresh token stays valid
// at Garmin's own service until Garmin expires or revokes it; this command
// only removes the local copy and the local linkage.
func NewUnlinkCommand(opts Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "unlink",
		Short: "Remove one principal's Garmin account linkage",
		Long: "Unlink one principal's Garmin account: delete the encrypted Garmin\n" +
			"DI token record, the sealed identity and the account hash, plus the\n" +
			"token families and pending authorization state that cascade with it.\n\n" +
			"This is local state only. The Garmin DI refresh token stays valid at\n" +
			"Garmin's own service until Garmin expires or revokes it, so this never\n" +
			"reports \"tokens revoked\" — only \"local tokens removed\". A user who\n" +
			"wants their Garmin session ended must do that in Garmin Connect.\n\n" +
			"The command is idempotent: unlinking an already unlinked principal\n" +
			"reports a zero result and succeeds. An unknown principal is refused,\n" +
			"so a typo cannot be mistaken for a no-op.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			principalID, err := parsePrincipalFlag(cmd)
			if err != nil {
				return err
			}
			return runUnlink(cmd.Context(), cfg, principalID, opts.stdout())
		},
	}

	registerPrincipalFlag(command)
	return command
}

// runUnlink opens the database, performs the cascade, and reports what it
// removed. An unknown principal returns store.ErrPrincipalNotFound unchanged
// (wrapped with context), which is what makes it distinguishable, on exit
// status and on the error text, from an idempotent no-op.
func runUnlink(ctx context.Context, cfg config.Config, principalID string, out io.Writer) error {
	sqlite, err := openRevocationStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = sqlite.Close() }()

	result, err := sqlite.UnlinkGarminAccount(ctx, principalID)
	if err != nil {
		return fmt.Errorf("unlinking principal %s: %w", principalID, err)
	}
	writeUnlinkReport(out, principalID, result)
	return nil
}

// writeUnlinkReport states what the cascade removed, in the order an operator
// reads it. Only the principal id is echoed — the operator's own handle — and
// every count is a number, never a Garmin account identifier, an email, or a
// token.
//
// Every line says "removed", never "revoked": this command's own Long help
// text promises unlink reports local removal only, and "revoked" would read as
// a claim about Garmin's side, which this command never touches.
func writeUnlinkReport(out io.Writer, principalID string, result store.RevocationResult) {
	_, _ = fmt.Fprintf(out, "principal: %s\n", principalID)
	_, _ = fmt.Fprintf(out, "local MCP token families removed: %d\n", result.FamiliesRevoked)
	_, _ = fmt.Fprintf(out, "local MCP tokens removed: %d\n", result.TokensRevoked)
	_, _ = fmt.Fprintf(out, "garmin token record removed: %d\n", result.GarminTokensDeleted)
	_, _ = fmt.Fprintf(out, "pending authorization transactions removed: %d\n", result.TransactionsDeleted)
	_, _ = fmt.Fprintf(out, "pending authorization codes removed: %d\n", result.CodesDeleted)
	_, _ = fmt.Fprintln(out, "local tokens removed; this is not revocation at Garmin")
}
