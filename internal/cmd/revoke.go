package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// NewRevokeCommand revokes one principal's OAuth authorization: every token
// family and every consent, with no Garmin linkage touched.
//
// It answers the "revoke my access" half of a data-deletion request without
// direct database access, and it is deliberately narrower than unlink: an
// operator revoking a compromised client grant should not have to force the
// account through Garmin re-linking, so this command never deletes the
// encrypted Garmin DI token record or the sealed identity.
func NewRevokeCommand(opts Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "revoke",
		Short: "Revoke one principal's OAuth token families and consents",
		Long: "Revoke one principal's OAuth authorization: every token family it\n" +
			"holds with any client, and every consent it granted. Pending\n" +
			"authorization transactions and codes are removed too.\n\n" +
			"The Garmin account link is left exactly as it was: this command\n" +
			"deletes no Garmin token record and no Garmin identity, so a\n" +
			"principal whose MCP access was revoked can authorize a client again\n" +
			"without re-linking Garmin. It revokes nothing at Garmin either.\n\n" +
			"The command is idempotent: revoking an already-revoked principal\n" +
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
			return runRevoke(cmd.Context(), cfg, principalID, opts.stdout())
		},
	}

	registerPrincipalFlag(command)
	return command
}

// runRevoke opens the database, checks the principal exists, revokes its
// tokens and consents, and reports what it changed.
//
// store.RevokePrincipalTokens is itself idempotent and reports a zero result
// rather than an error for an unknown principal — a caller revoking what is
// already gone has got what it asked for, which is the right answer for a
// SECOND call. It is the wrong answer for a FIRST call against a principal
// that never existed, which is almost always a typo, so this command adds its
// own existence check before it and refuses that case with
// store.ErrPrincipalNotFound.
func runRevoke(ctx context.Context, cfg config.Config, principalID string, out io.Writer) error {
	sqlite, err := openRevocationStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = sqlite.Close() }()

	if _, err := sqlite.PrincipalByID(ctx, principalID); err != nil {
		return fmt.Errorf("revoking principal %s: %w", principalID, err)
	}

	result, err := sqlite.RevokePrincipalTokens(ctx, principalID)
	if err != nil {
		return fmt.Errorf("revoking principal %s: %w", principalID, err)
	}
	writeRevokeReport(out, principalID, result)
	return nil
}

// writeRevokeReport states what the cascade changed. Only the principal id is
// echoed, and every count is a number, never a Garmin account identifier, an
// email, or a token.
func writeRevokeReport(out io.Writer, principalID string, result store.RevocationResult) {
	_, _ = fmt.Fprintf(out, "principal: %s\n", principalID)
	_, _ = fmt.Fprintf(out, "token families revoked: %d\n", result.FamiliesRevoked)
	_, _ = fmt.Fprintf(out, "tokens revoked: %d\n", result.TokensRevoked)
	_, _ = fmt.Fprintf(out, "pending authorization transactions removed: %d\n", result.TransactionsDeleted)
	_, _ = fmt.Fprintf(out, "pending authorization codes removed: %d\n", result.CodesDeleted)
	_, _ = fmt.Fprintln(out, "the garmin account link is unchanged; re-linking is not required")
}
