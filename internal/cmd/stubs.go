package cmd

import (
	"github.com/spf13/cobra"
)

// This file holds the commands whose subsystem does not exist yet. Each one is
// deliberately thin, and each one fails: it resolves and validates the effective
// configuration, then reports the missing subsystem through
// [NotImplementedError]. None of them is working behavior, and none may be
// counted as parity.
//
// They exist now for two reasons. The command surface is part of the operator
// contract, so an unknown command must be distinguishable from an unfinished one;
// and configuration validation is real today, so an operator can already check a
// deployment's settings without waiting for the subsystem.

// NewMigrateCommand will apply the embedded, monotonic database migrations.
func NewMigrateCommand(opts Options) *cobra.Command {
	return newPendingCommand(opts, pending{
		use:       "migrate",
		short:     "Apply database migrations",
		subsystem: "the storage layer and its migrations",
	})
}

// NewToolsCommand groups the tool inspection commands. The group itself shows
// help; `tools list` reports the registered MCP tools once any exist.
func NewToolsCommand(opts Options) *cobra.Command {
	group := &cobra.Command{
		Use:   "tools",
		Short: "Inspect the MCP tools this build registers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	group.AddCommand(newPendingCommand(opts, pending{
		use:       "list",
		short:     "List the registered MCP tools, their tiers, and their scopes",
		subsystem: "the MCP tool registry",
	}))
	return group
}

// pending describes one command whose subsystem is still missing.
type pending struct {
	// use is the Cobra use line.
	use string
	// short is the one-line description.
	short string
	// subsystem names what does not exist yet, in the operator's terms.
	subsystem string
}

// newPendingCommand builds a command that validates configuration and then
// reports the gap. Configuration is validated first on purpose: a misconfigured
// deployment is a different problem from an unfinished one, and conflating the two
// would hide the fixable half.
func newPendingCommand(opts Options, spec pending) *cobra.Command {
	_ = opts // The real implementations will need the writers and the build identity.

	return &cobra.Command{
		Use:   spec.use,
		Short: spec.short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := loadConfig(cmd); err != nil {
				return err
			}
			return notImplemented(cmd.CommandPath(), spec.subsystem)
		},
	}
}
