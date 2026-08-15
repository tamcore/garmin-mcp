// Command garmin-mcp is a Model Context Protocol server for Garmin Connect.
//
// This package stays thin on purpose: it owns the release identity the linker
// injects and the process exit status, and nothing else. Every behavior lives
// under internal/, where it can be tested without running a binary.
package main

import (
	"context"
	"os"

	"github.com/tamcore/garmin-mcp/internal/cmd"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// version and commit are injected by the GoReleaser ldflags
// (-X main.version=... -X main.commit=...). The local defaults make an
// unreleased build obvious.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	os.Exit(cmd.Execute(context.Background(), cmd.Options{
		BuildInfo: cmd.BuildInfo{Version: version, Commit: commit},
		Args:      os.Args[1:],
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Tools:     garminTools,
		Catalog:   garminCatalog,
	}))
}

// garminCatalog reports the tool surface for `tools list`.
//
// It is the read-only half of the same seam garminTools plugs into, and it exists
// separately because listing the surface must not need what serving it needs: this
// reads the declared contracts, so the command answers without a Garmin client, a
// token, or a database.
func garminCatalog() []cmd.ToolEntry {
	contracts := tools.Contracts()
	entries := make([]cmd.ToolEntry, 0, len(contracts))
	for _, contract := range contracts {
		entries = append(entries, cmd.ToolEntry{
			Name:       contract.Spec.Name,
			Tier:       contract.Spec.Tier,
			Idempotent: contract.Spec.Annotations.Idempotent,
		})
	}
	return entries
}

// garminTools builds the Garmin tool set for the server.
//
// It lives here rather than in internal/cmd so that the command layer depends on
// the registration interface instead of on the tools themselves: a test can hand
// it a fake registry, and the tool package can grow without the composition root
// knowing. The tier lists travel with the registrar because the policy is
// validated against the registered set before the server starts, so a tool that
// is registered but untiered fails at start-up rather than at first call.
func garminTools(deps cmd.ToolDeps) (cmd.ToolSet, error) {
	registrar, err := tools.New(tools.Deps{Client: deps.Client, Caller: deps.Caller})
	if err != nil {
		return cmd.ToolSet{}, err
	}

	return cmd.ToolSet{
		Registrar:   registrar,
		ReadOnly:    tools.ReadOnlyTools(),
		Write:       tools.WriteTools(),
		Destructive: tools.DestructiveTools(),
	}, nil
}
