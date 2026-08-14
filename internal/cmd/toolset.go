package cmd

import (
	"slices"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
)

// ToolDeps is everything a tool package needs from the composition root.
//
// It carries no configuration and no secret. A tool resolves its principal from
// the request context and builds a [client.Session] from Caller, so no tool can
// name an account: there is no field here through which one could.
type ToolDeps struct {
	// Client is the shared authenticated request layer: retries, bounds, and
	// typed errors. It holds no token.
	Client *client.Client

	// Caller performs one authenticated request for one principal. It is the
	// Refresher, which attaches the DI bearer token and enforces the Garmin host
	// allowlist, so a tool never sees a token.
	Caller client.Caller

	// Activities lists and filters activities.
	Activities *api.Activities
	// ActivityDetails reads the positional and set-level activity payloads.
	ActivityDetails *api.ActivityDetails
	// Devices reads the registered devices.
	Devices *api.Devices
	// Profile reads the social profile and the user settings.
	Profile *api.Profile
	// Wellness reads the daily wellness summaries.
	Wellness *api.Wellness
}

// ToolSet is one tool package's contribution to a server: the registrar that adds
// the tools, plus the tier name lists the policy is built from.
//
// The tier lists come from the tool package rather than from the registry, because
// the policy must exist before registration: mcpserver.New validates the
// configured names against the actually-registered set, which is what turns a typo
// into a start-up failure.
type ToolSet struct {
	// Registrar adds the tools. A nil registrar contributes none.
	Registrar mcpserver.ToolRegistrar

	// ReadOnly, Write and Destructive name every tool in each tier. A name may
	// appear in exactly one list, and every name must be registered by Registrar.
	ReadOnly    []string
	Write       []string
	Destructive []string
}

// ToolFactory builds a [ToolSet] from the dependencies the composition root
// assembled.
//
// This is the seam a tool package plugs into. This package deliberately does not
// import one: the dependency points inwards, so a fake registrar in a test and the
// real registry in the binary are wired identically. A nil factory registers no
// Garmin tool, which leaves the server with its built-in tool only.
type ToolFactory func(ToolDeps) (ToolSet, error)

// tierNames returns the three tier lists, guaranteeing that the server's built-in
// tool has a tier. That tool is always registered, so it must always be tiered:
// policy validation refuses a registered tool that has none.
//
// The name is added only when the factory did not already supply it. A tool set
// that validates the whole registered set against its own lists has to name
// server_info too, so both sides legitimately carry it, and a duplicate is also
// refused by policy validation.
func (s ToolSet) tierNames() (readOnly, write, destructive []string) {
	readOnly = make([]string, 0, len(s.ReadOnly)+1)
	if !slices.Contains(s.ReadOnly, mcpserver.ServerInfoToolName) {
		readOnly = append(readOnly, mcpserver.ServerInfoToolName)
	}
	readOnly = append(readOnly, s.ReadOnly...)
	return readOnly, copyNames(s.Write), copyNames(s.Destructive)
}

// registrars returns the registrar list for mcpserver.Deps, omitting a nil one:
// mcpserver rejects a nil entry rather than skipping it, and contributing no tools
// is not a wiring mistake.
func (s ToolSet) registrars() []mcpserver.ToolRegistrar {
	if s.Registrar == nil {
		return nil
	}
	return []mcpserver.ToolRegistrar{s.Registrar}
}

// copyNames returns a copy, so a built policy cannot observe a later change to the
// caller's slice.
func copyNames(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
