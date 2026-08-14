package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
)

// serverName is the programmatic MCP server name and serverTitle its display name.
const (
	serverName  = "garmin-mcp"
	serverTitle = "Garmin Connect"
)

// serverInstructions are the MCP server instructions connected clients receive.
// They state the two facts a client cannot infer: the API is unofficial, and this
// process serves exactly one account.
const serverInstructions = "Garmin Connect data for one local account. " +
	"Garmin's API is unofficial and undocumented, so a call can fail or change shape " +
	"without notice. The account is bound to this process: no tool accepts a user, an " +
	"email, or a token path."

// NewServeCommand runs the MCP server.
//
// The command resolves and fully validates the effective configuration first, so an
// operator learns about a rejected transport, an unprotected listener, or unusable
// key material before anything is opened. Then it assembles the dependency graph
// and serves.
//
// Nothing here writes to the result stream except the transport. In stdio mode that
// stream carries MCP frames exclusively, and a single stray diagnostic byte would
// corrupt the session, so every diagnostic goes to the error stream and every log
// record goes to the structured logger, whose sink can never be standard output.
func NewServeCommand(opts Options) *cobra.Command {
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
			if cfg.Transport != config.TransportStdio {
				return notImplemented("garmin-mcp serve",
					"the MCP "+string(cfg.Transport)+" server")
			}
			return runStdio(cmd.Context(), cfg, opts)
		},
	}
}

// runStdio assembles the dependency graph and serves MCP over standard input and
// output until the peer disconnects or the context is cancelled.
//
// A cancelled context is a graceful stop, not a failure: it is how a supervisor and
// an interrupt both ask the process to end, and reporting it as an error would make
// every clean shutdown exit non-zero.
func runStdio(ctx context.Context, cfg config.Config, opts Options) error {
	deps, err := newDependencies(cfg, &wiring{
		Logs:    opts.stderr(),
		Tools:   opts.Tools,
		Version: opts.BuildInfo.Version,
	})
	if err != nil {
		return err
	}
	defer deps.close()

	if err := deps.importConfiguredTokens(ctx); err != nil {
		return err
	}

	server, err := mcpserver.New(deps.serverDeps())
	if err != nil {
		return fmt.Errorf("assembling the MCP server: %w", err)
	}

	err = server.RunStdio(ctx, mcpserver.StdioOptions{In: opts.stdin(), Out: opts.stdout()})
	if isGracefulStop(ctx, err) {
		return nil
	}
	return err
}

// serverDeps projects the assembled graph onto the MCP server's dependency set.
func (d *dependencies) serverDeps() mcpserver.Deps {
	return mcpserver.Deps{
		Info: mcpserver.Info{
			Name:    serverName,
			Title:   serverTitle,
			Version: d.version,
		},
		Logger:       d.logger,
		Policy:       d.policy,
		Limiter:      d.limiter,
		Principals:   d.principals,
		Registrars:   d.tools.registrars(),
		Instructions: serverInstructions,
	}
}

// isGracefulStop reports whether err is the shutdown a cancelled context causes.
//
// The context is consulted as well as the error, because a cancelled read surfaces
// as an I/O failure rather than as context.Canceled on some platforms, and a stop
// the operator asked for must not become an exit code a supervisor reads as a
// crash.
func isGracefulStop(ctx context.Context, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return true
	default:
		return ctx.Err() != nil
	}
}

// loadConfig resolves the effective configuration for cmd. It reads the command's
// full flag set, which includes the persistent flags inherited from the root, so
// every command observes the same precedence.
func loadConfig(cmd *cobra.Command) (config.Config, error) {
	return config.Load(config.LoadOptions{Flags: cmd.Flags()})
}
