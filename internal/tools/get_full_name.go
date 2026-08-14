package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetFullName is the upstream compatibility name of the full-name tool.
const ToolGetFullName = "get_full_name"

// FullName is the account's full name and nothing else.
//
// The narrow result is the point: a caller that only needs the name does not have to
// receive the whole profile. Upstream's manifest declares the same single key.
type FullName struct {
	// FullName is the account's full name as Garmin holds it.
	FullName string `json:"full_name" jsonschema:"the Garmin account's full name"`
}

// LogValue reports presence, never the name.
func (n FullName) LogValue() slog.Value {
	return shape("fullName", slog.String("fullName", presence(n.FullName != "")))
}

func getFullNameContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetFullName,
			Title: "Get full name",
			Description: "read the authenticated Garmin account's full name. Takes no " +
				"arguments; the account is the one this session is authenticated for",
			Tier:        policy.TierReadOnly,
			Category:    categoryProfile,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

// registerGetFullName registers the tool.
func registerGetFullName(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, FullName, error,
	) {
		profile, err := svc.socialProfile(ctx)
		if err != nil {
			return nil, FullName{}, err
		}
		if profile.FullName == nil || *profile.FullName == "" {
			return nil, FullName{}, incompleteProfile("it carries no full name")
		}
		return nil, FullName{FullName: *profile.FullName}, nil
	}
	return mcpserver.AddTool(registry, getFullNameContract().Registration(), handler)
}
