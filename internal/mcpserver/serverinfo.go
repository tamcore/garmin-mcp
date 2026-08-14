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
// Every field is a property of the deployment. None is a property of the account:
// no principal identifier, no email, no Garmin data.
type ServerInfo struct {
	// Name is the configured server name.
	Name string `json:"name" jsonschema:"the configured server name"`

	// Version is the build version.
	Version string `json:"version" jsonschema:"the server build version"`

	// ProtocolVersion is the MCP specification version the server targets.
	ProtocolVersion string `json:"protocolVersion" jsonschema:"the MCP specification version"`

	// ToolCount is how many tools are registered.
	ToolCount int `json:"toolCount" jsonschema:"the number of registered tools"`

	// Mode is the coarse deployment mode, local or remote.
	Mode string `json:"mode" jsonschema:"the deployment mode, local or remote"`
}

// registerServerInfo registers the built-in tool against the same AddTool entry
// point every other tool uses, so it is gated by the same middleware chain and
// validated by the same rules rather than being a privileged special case.
func registerServerInfo(registry *Registry, server *Server) error {
	handler := func(context.Context, *mcp.CallToolRequest, serverInfoInput) (
		*mcp.CallToolResult, ServerInfo, error,
	) {
		return nil, ServerInfo{
			Name:            server.deps.Info.Name,
			Version:         server.deps.Info.Version,
			ProtocolVersion: ProtocolVersion,
			ToolCount:       registry.Len(),
			Mode:            server.deps.Policy.Mode().String(),
		}, nil
	}

	return AddTool(registry, ToolSpec{
		Name:        ServerInfoToolName,
		Title:       "Server info",
		Description: "report this server's name, version, MCP protocol version, tool count and mode",
		Tier:        policy.TierReadOnly,
		Category:    "diagnostics",
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
