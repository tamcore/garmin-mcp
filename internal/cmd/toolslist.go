package cmd

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// Column layout of the listing. The tab writer aligns on tabs, so these only set
// the minimum width and the padding.
const (
	listMinWidth = 4
	listTabWidth = 4
	listPadding  = 2
)

// A ToolEntry is one registered tool as `tools list` reports it.
//
// It describes the surface and nothing else: no dependency, no handler and no
// schema. That is what lets the command answer without a Garmin client and without
// a database, which matters because listing the surface is not a privileged
// operation.
type ToolEntry struct {
	// Name is the wire name the tool is registered under.
	Name string

	// Tier is the policy tier that gates the tool.
	Tier policy.Tier

	// Idempotent reports whether repeating the call with the same arguments has a
	// further effect. It is meaningful for the two higher tiers only.
	Idempotent bool
}

// A ToolCatalog reports the tools a build registers.
//
// It is the read-only twin of [ToolFactory]. The factory needs the assembled
// Garmin dependencies and can therefore not be called by a command that must touch
// nothing; this one takes no argument at all, so there is nothing it could open,
// call or authenticate. A nil catalog reports no Garmin tool, which is the same
// wiring a nil factory describes.
type ToolCatalog func() []ToolEntry

// NewToolsCommand groups the tool inspection commands. The group itself shows
// help; `tools list` reports the registered tools.
func NewToolsCommand(opts Options) *cobra.Command {
	group := &cobra.Command{
		Use:   "tools",
		Short: "Inspect the MCP tools this build registers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	group.AddCommand(newToolsListCommand(opts))
	return group
}

// newToolsListCommand builds `tools list`.
//
// It resolves the configuration, because the enablement of the two higher tiers is
// part of the answer, and because a setting the operator got wrong must be
// reported as such rather than by a listing that quietly describes a deployment
// which would not start. It opens nothing else.
func newToolsListCommand(opts Options) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the registered MCP tools, their tiers, and their effect",
		Long: "List every tool this build registers, with the policy tier that gates it\n" +
			"and the effect a call has on the Garmin account.\n\n" +
			"The listing reaches nothing: no Garmin request is made and no database is\n" +
			"opened, so it answers the same way on a deployment that has never served.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			writeToolListing(opts.stdout(), cfg, registeredTools(opts.Catalog))
			return nil
		},
	}
}

// registeredTools returns what the build registers, in listing order.
//
// The server's own built-in tool is added here rather than expected from the
// catalog, for the same reason [ToolSet.tierNames] adds it to the tier lists: the
// server registers it itself, so a listing built only from the Garmin tools would
// name fewer tools than a client sees.
func registeredTools(catalog ToolCatalog) []ToolEntry {
	entries := []ToolEntry{
		{Name: mcpserver.ServerInfoToolName, Tier: policy.TierReadOnly, Idempotent: true},
	}
	if catalog != nil {
		for _, entry := range catalog() {
			if entry.Name != mcpserver.ServerInfoToolName {
				entries = append(entries, entry)
			}
		}
	}

	slices.SortFunc(entries, func(a, b ToolEntry) int {
		if byTier := cmp.Compare(a.Tier, b.Tier); byTier != 0 {
			return byTier
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return entries
}

// writeToolListing renders the table and the enablement summary.
func writeToolListing(out io.Writer, cfg config.Config, entries []ToolEntry) {
	table := tabwriter.NewWriter(out, listMinWidth, listTabWidth, listPadding, ' ', 0)
	_, _ = fmt.Fprintln(table, "NAME\tTIER\tEFFECT")
	for _, entry := range entries {
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\n", entry.Name, entry.Tier, entry.effect())
	}
	_ = table.Flush()

	writeTierSummary(out, cfg, entries)
}

// effect states what a call does, in the operator's terms rather than the enum's.
func (e ToolEntry) effect() string {
	switch e.Tier {
	case policy.TierReadOnly:
		return "reads data; changes nothing in the account"
	case policy.TierWrite:
		if e.Idempotent {
			return "adds or updates data; repeating it converges"
		}
		return "adds or updates data; repeating it creates another record"
	case policy.TierDestructive:
		return "deletes data; the client must confirm the call first"
	default:
		return "unknown effect; a tool in no tier is refused on every call"
	}
}

// writeTierSummary counts the tiers and states whether this configuration would
// let the two higher ones run.
//
// It is part of the listing because a tool's tier answers only half the question
// an operator has. On stdio enablement is sufficient; remotely a caller also
// needs the matching OAuth scope.
func writeTierSummary(out io.Writer, cfg config.Config, entries []ToolEntry) {
	counts := map[policy.Tier]int{}
	for _, entry := range entries {
		counts[entry.Tier]++
	}

	_, _ = fmt.Fprintf(out, "\n%d tools: %d read-only, %d write, %d destructive\n",
		len(entries), counts[policy.TierReadOnly],
		counts[policy.TierWrite], counts[policy.TierDestructive])
	_, _ = fmt.Fprintf(out, "write tier: %s\n", enablementOf(cfg.EnableWriteTools))
	_, _ = fmt.Fprintf(out, "destructive tier: %s\n", enablementOf(cfg.EnableDestructiveTools))
	if cfg.Transport == config.TransportStdio {
		_, _ = fmt.Fprintln(out, "On stdio, each enabled tier is authorized by its operator flag.")
		return
	}
	_, _ = fmt.Fprintln(out,
		"A remote call in either higher tier also needs the matching OAuth scope.")
}

// enablementOf renders one tier's operator switch.
func enablementOf(enabled bool) string {
	if enabled {
		return "enabled by configuration"
	}
	return "disabled by configuration"
}
