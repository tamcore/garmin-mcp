package cmd

import (
	"github.com/spf13/cobra"

	"github.com/tamcore/garmin-mcp/internal/config"
)

// NewServeCommand runs the MCP server.
//
// The command resolves and fully validates the effective configuration first, so
// an operator learns about a rejected transport, an unprotected listener, or a
// missing master key before anything is opened. The MCP server itself does not
// exist yet, so a valid configuration is reported as an explicit gap rather than
// as a running server.
//
// Nothing here writes to the result stream. In stdio mode that stream carries MCP
// frames exclusively, and a single stray diagnostic byte would corrupt the
// session.
func NewServeCommand(opts Options) *cobra.Command {
	_ = opts // The server will need the build identity and the writers; the gap does not.

	return &cobra.Command{
		Use:   "serve",
		Short: "Serve MCP over stdio or Streamable HTTP",
		Long: "Serve MCP over the configured transport.\n\n" +
			"In stdio mode standard output is reserved exclusively for MCP frames;\n" +
			"logs, diagnostics, and errors go to standard error.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			return notImplemented("garmin-mcp serve", "the MCP "+string(cfg.Transport)+" server")
		},
	}
}

// loadConfig resolves the effective configuration for cmd. It reads the command's
// full flag set, which includes the persistent flags inherited from the root, so
// every command observes the same precedence.
func loadConfig(cmd *cobra.Command) (config.Config, error) {
	return config.Load(config.LoadOptions{Flags: cmd.Flags()})
}
