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
	}))
}
