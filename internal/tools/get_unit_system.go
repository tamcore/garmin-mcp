package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetUnitSystem is the upstream compatibility name of the unit-system tool.
const ToolGetUnitSystem = "get_unit_system"

// UnitSystem is the account's preferred measurement system.
//
// The settings document it comes from also carries a birth date, a weight and a
// height. None of them is returned: the tool was asked for the unit system.
type UnitSystem struct {
	// UnitSystem is Garmin's measurement-system key, for example "metric".
	UnitSystem string `json:"unit_system" jsonschema:"the preferred measurement system, for example metric"`
}

// LogValue reports presence, never the value.
func (u UnitSystem) LogValue() slog.Value {
	return shape("unitSystem", slog.String("unitSystem", presence(u.UnitSystem != "")))
}

func getUnitSystemContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetUnitSystem,
			Title: "Get unit system",
			Description: "read the authenticated Garmin account's preferred measurement " +
				"system, so distances and weights can be presented in the units the account " +
				"uses. Takes no arguments",
			Tier:        policy.TierReadOnly,
			Category:    categoryProfile,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

// registerGetUnitSystem registers the tool.
func registerGetUnitSystem(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, UnitSystem, error,
	) {
		session, err := svc.session(ctx)
		if err != nil {
			return nil, UnitSystem{}, err
		}
		settings, err := svc.profile.Settings(ctx, session)
		if err != nil {
			return nil, UnitSystem{}, fail(err)
		}
		if settings.UserData == nil {
			return nil, UnitSystem{}, incompleteProfile("it carries no settings document")
		}
		system := optionalText(settings.UserData.MeasurementSystem)
		if system == nil {
			return nil, UnitSystem{}, incompleteProfile("it names no measurement system")
		}
		return nil, UnitSystem{UnitSystem: *system}, nil
	}
	return mcpserver.AddTool(registry, getUnitSystemContract().Registration(), handler)
}
