package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ServerInfoToolName is the name of the one built-in tool.
//
// It is not a Garmin tool and has no upstream compatibility obligation. It exists so
// the assembled server — registry, middleware chain, policy gate, and transport —
// is exercisable end to end before any Garmin tool exists.
const ServerInfoToolName = "server_info"

// serverInfoInput is empty: the tool takes no arguments.
//
// This is also the shape that makes the principal rule visible. There is no field
// through which a caller could name a user, an email, or a token path, and adding
// one would not help: the principal comes from the resolver, never from an argument.
type serverInfoInput struct{}

// ServerInfo is the structured result of the server_info tool.
//
// Most fields are a property of the deployment and read the same for every
// caller. EnabledTiers, GrantedScopes and VisibleToolCount are the exception:
// each is computed from the effective policy state for the request that asked,
// so it can differ between two sessions of the same deployment. None of the
// three is a property of the account, though: they name tiers, scopes and a
// count, never a principal identifier, an email, a token, or any Garmin data.
type ServerInfo struct {
	// Name is the configured server name.
	Name string `json:"name" jsonschema:"the configured server name"`

	// Version is the build version.
	Version string `json:"version" jsonschema:"the server build version"`

	// ProtocolVersion is the MCP specification version the server targets.
	ProtocolVersion string `json:"protocolVersion" jsonschema:"the MCP specification version"`

	// ToolCount is how many tools this build registers in total, regardless of
	// caller: a property of the deployment, unchanged by policy. It answers "how
	// big is this server," not "what can I call" — VisibleToolCount answers that.
	ToolCount int `json:"toolCount" jsonschema:"the total number of registered tools, the same for every caller"`

	// VisibleToolCount is how many tools tools/list returns for this caller: the
	// same policy.Decide-filtered count the tools/list result itself carries. A
	// client whose tools/list is shorter than ToolCount finds why in
	// EnabledTiers and GrantedScopes below, rather than having to infer it.
	VisibleToolCount int `json:"visibleToolCount" jsonschema:"the number of tools tools/list returns for this caller"`

	// Mode is the coarse deployment mode, local or remote.
	Mode string `json:"mode" jsonschema:"the deployment mode, local or remote"`

	// EnabledTiers names the policy tiers this caller may use: read-only is
	// always present; write and destructive appear only when the operator has
	// enabled the tier AND this caller's own granted scopes include it — the
	// same intersection the tools/call gate applies.
	EnabledTiers []string `json:"enabledTiers" jsonschema:"the policy tiers this caller may use"`

	// GrantedScopes names the OAuth scopes this caller's session holds. It is
	// empty on stdio, where no scope is ever presented.
	GrantedScopes []string `json:"grantedScopes" jsonschema:"the OAuth scopes granted to this caller; empty on stdio"`
}

// registerServerInfo registers the built-in tool against the same AddTool entry
// point every other tool uses, so it is gated by the same middleware chain and
// validated by the same rules rather than being a privileged special case.
func registerServerInfo(registry *Registry, server *Server) error {
	handler := func(ctx context.Context, req *mcp.CallToolRequest, _ serverInfoInput) (
		*mcp.CallToolResult, ServerInfo, error,
	) {
		return nil, ServerInfo{
			Name:             server.deps.Info.Name,
			Version:          server.deps.Info.Version,
			ProtocolVersion:  ProtocolVersion,
			ToolCount:        registry.Len(),
			VisibleToolCount: server.visibleToolCount(ctx, clientDeclaresElicitation(req)),
			Mode:             server.deps.Policy.Mode().String(),
			EnabledTiers:     tierStrings(server.deps.Policy.EffectiveTiers(ctx)),
			GrantedScopes:    scopeStrings(server.deps.Policy.GrantedScopes(ctx)),
		}, nil
	}

	return AddTool(registry, ToolSpec{
		Name:  ServerInfoToolName,
		Title: "Server info",
		Description: "report this server's name, version, MCP protocol version, tool counts, " +
			"mode, and this caller's effective policy: its enabled tiers and granted scopes",
		Tier:     policy.TierReadOnly,
		Category: "diagnostics",
		Annotations: Annotations{
			ReadOnly:    true,
			Destructive: false,
			Idempotent:  true,
			// Garmin is an open-world API and every tool this server registers
			// declares so, including this one, so the hint is uniform.
			OpenWorld: true,
		},
	}, handler)
}

// visibleToolCount counts what tools/list would return for ctx's caller, using
// the exact same toolVisible decision the tools/list filter middleware applies —
// policy plus, for a destructive tool, the caller's own declared elicitation
// capability — so this number can never drift from the wire.
func (s *Server) visibleToolCount(ctx context.Context, elicitation bool) int {
	count := 0
	for _, name := range s.registry.Names() {
		if s.toolVisible(ctx, elicitation, name) {
			count++
		}
	}
	return count
}

// tierStrings renders tiers in their stable string form.
func tierStrings(tiers []policy.Tier) []string {
	out := make([]string, len(tiers))
	for i, tier := range tiers {
		out[i] = tier.String()
	}
	return out
}

// scopeStrings renders scopes as plain strings.
func scopeStrings(scopes []policy.Scope) []string {
	out := make([]string, len(scopes))
	for i, scope := range scopes {
		out[i] = string(scope)
	}
	return out
}
