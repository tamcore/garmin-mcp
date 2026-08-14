package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// renderVersion formats the build identity on one line. The same rendering backs
// both `garmin-mcp version` and the root command's --version flag, so the two can
// never disagree.
func renderVersion(info BuildInfo) string {
	return fmt.Sprintf("garmin-mcp %s (commit %s, %s, %s/%s)",
		info.version(), info.commit(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

// NewVersionCommand reports the release identity injected by the GoReleaser
// ldflags into cmd/garmin-mcp.
//
// The line goes to the result writer, because a build identity is this command's
// result rather than a diagnostic. That is not an exception to the MCP frame rule:
// the stdio server is started by `serve`, and `version` never starts one.
func NewVersionCommand(opts Options) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version and commit this binary was built from",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), renderVersion(opts.BuildInfo))
			return err
		},
	}
}
