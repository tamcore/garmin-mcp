// Package cmd builds the garmin-mcp Cobra command tree.
//
// The tree is constructed per call from an explicit [Options] value: there is no
// package-level command, logger, or configuration state, so two commands can be
// built concurrently in a test without sharing anything.
//
// Output discipline: every command writes through the writers in [Options], and
// diagnostics go to the error writer. In stdio transport mode the standard
// output stream is reserved exclusively for MCP frames, so no log line, usage
// text, or error message may reach it. See [NewServeCommand].
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/tamcore/garmin-mcp/internal/config"
)

// exitFailure is the process exit status for any command that returns an error.
const exitFailure = 1

// unknownBuildValue replaces a build field the linker did not inject, so the
// output never contains an empty field a script could misread as success.
const unknownBuildValue = "unknown"

// BuildInfo carries the release identity injected by the GoReleaser ldflags into
// cmd/garmin-mcp. Both fields are plain data; an empty field renders as
// "unknown" rather than as an empty string.
type BuildInfo struct {
	// Version is the release version, for example "v1.2.3", or "dev" for a
	// local build.
	Version string
	// Commit is the commit the binary was built from, or "none" locally.
	Commit string
}

// version reports the injected version, or unknownBuildValue when the linker
// injected nothing.
func (b BuildInfo) version() string { return orUnknown(b.Version) }

// commit reports the injected commit, or unknownBuildValue when the linker
// injected nothing.
func (b BuildInfo) commit() string { return orUnknown(b.Commit) }

func orUnknown(value string) string {
	if value == "" {
		return unknownBuildValue
	}
	return value
}

// Options is the complete input to the command tree. It is copied, never
// mutated, so a caller can reuse one value for several invocations.
type Options struct {
	// BuildInfo is the ldflags-injected release identity.
	BuildInfo BuildInfo
	// Args are the command-line arguments after the program name. A nil value
	// selects os.Args[1:].
	Args []string
	// Stdin is the MCP frame source in stdio mode. A nil value selects os.Stdin.
	// No command reads a credential from it: Garmin credentials arrive only
	// through the browser form or the explicit TTY prompt.
	Stdin io.Reader
	// Stdout receives command results, and in stdio mode the MCP frames. A nil
	// value selects os.Stdout.
	Stdout io.Writer
	// Stderr receives diagnostics, logs, and errors. A nil value selects
	// os.Stderr.
	Stderr io.Writer
	// Tools contributes the Garmin MCP tools. A nil factory registers none, which
	// leaves the server with its built-in tool only.
	//
	// This is the registration seam: the composition root depends on the
	// [ToolFactory] signature and the mcpserver.ToolRegistrar interface, never on
	// a tool package, so a test can hand it a fake registry and the binary can
	// hand it the real one.
	Tools ToolFactory

	// Catalog reports the tools this build registers, for `tools list`. A nil
	// catalog reports none.
	//
	// It is separate from Tools because listing the surface must not need the
	// Garmin dependencies a factory takes: a catalog can be answered by a process
	// that opens nothing, which is what keeps the listing unprivileged.
	Catalog ToolCatalog
}

func (o Options) stdin() io.Reader {
	if o.Stdin == nil {
		return os.Stdin
	}
	return o.Stdin
}

func (o Options) stdout() io.Writer {
	if o.Stdout == nil {
		return os.Stdout
	}
	return o.Stdout
}

func (o Options) stderr() io.Writer {
	if o.Stderr == nil {
		return os.Stderr
	}
	return o.Stderr
}

func (o Options) args() []string {
	if o.Args == nil {
		return os.Args[1:]
	}
	return o.Args
}

// Execute builds the command tree, runs it with ctx, and returns the process
// exit status. Errors are reported on the error writer only, so a failing
// command cannot corrupt an MCP frame stream on standard output.
func Execute(ctx context.Context, opts Options) int {
	root := NewRootCommand(opts)
	if err := root.ExecuteContext(ctx); err != nil {
		// A failing error writer cannot be reported anywhere else, and the exit
		// status still carries the failure.
		_, _ = fmt.Fprintf(opts.stderr(), "garmin-mcp: %v\n", err)
		return exitFailure
	}
	return 0
}

// NewRootCommand returns the garmin-mcp root command with every subcommand
// attached. Usage and errors are silenced at the Cobra level because [Execute]
// renders them itself, on the error writer.
func NewRootCommand(opts Options) *cobra.Command {
	root := &cobra.Command{
		Use:   "garmin-mcp",
		Short: "Model Context Protocol server for Garmin Connect",
		Long: "garmin-mcp exposes Garmin Connect data and operations as MCP tools.\n\n" +
			"Garmin Connect is an unofficial, undocumented private API: endpoints,\n" +
			"schemas, and WAF behavior can change without notice.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       renderVersion(opts.BuildInfo),
		Args:          cobra.NoArgs,
		// RunE keeps a bare invocation informative: help goes to the result
		// writer instead of the command silently doing nothing.
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	// Configuration flags are persistent, so every subcommand resolves the same
	// settings with the same precedence. Secret-bearing settings deliberately
	// have no flag; see config.RegisterFlags.
	config.RegisterFlags(root.PersistentFlags())

	root.SetArgs(opts.args())
	root.SetOut(opts.stdout())
	root.SetErr(opts.stderr())
	root.SetVersionTemplate("{{.Version}}\n")
	root.CompletionOptions.DisableDefaultCmd = true

	root.AddCommand(
		NewServeCommand(opts),
		NewAuthCommand(opts),
		NewDoctorCommand(opts),
		NewToolsCommand(opts),
		NewMigrateCommand(opts),
		NewRotateKeyCommand(opts),
		NewRepairPermissionsCommand(opts),
		NewUnlinkCommand(opts),
		NewRevokeCommand(opts),
		NewVersionCommand(opts),
	)
	return root
}
