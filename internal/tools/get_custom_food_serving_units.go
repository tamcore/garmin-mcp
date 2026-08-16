package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetCustomFoodServingUnits is the upstream compatibility name.
const ToolGetCustomFoodServingUnits = "get_custom_food_serving_units"

// mirroredMaxServingUnits mirrors internal/garmin/api's own unexported
// maxServingUnits bound (nutritionreadservingunits.go). ServingUnits.Units()
// applies that cut already but exposes no truncation flag of its own, and the
// bound is unexported so it cannot be referenced directly from this package;
// this tool treats a returned count at exactly that bound as possibly cut, which
// is the closest an outside caller can get to the domain client's own signal
// without it exporting one.
const mirroredMaxServingUnits = 256

// ServingUnitsResult is the valid serving-unit catalog for a custom food.
type ServingUnitsResult struct {
	Units []string `json:"units" jsonschema:"the valid serving-unit codes, for example G, ML, OZ"`
	Count int      `json:"count" jsonschema:"how many units this result carries"`

	// Truncated reports that the unit count reached this server's bound, so the
	// catalog may have been cut rather than returned in full.
	Truncated bool `json:"truncated" jsonschema:"whether the catalog may have been cut at this server's bound"`
}

// LogValue reports the unit count, never the codes.
func (r ServingUnitsResult) LogValue() slog.Value {
	return shape("servingUnits", slog.Int("units", r.Count), slog.Bool("truncated", r.Truncated))
}

func getCustomFoodServingUnitsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetCustomFoodServingUnits,
			Title: "Get custom-food serving units",
			Description: "read the closed set of serving-unit codes Garmin accepts for a " +
				"custom food, for example G, ML and OZ",
			Tier:        policy.TierReadOnly,
			Category:    categoryNutrition,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(),
	}
}

// registerGetCustomFoodServingUnits registers the tool.
func registerGetCustomFoodServingUnits(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, _ noArguments) (
		*mcp.CallToolResult, ServingUnitsResult, error,
	) {
		out, err := svc.getCustomFoodServingUnits(ctx)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getCustomFoodServingUnitsContract().Registration(), handler)
}

// getCustomFoodServingUnits performs the read behind the tool.
func (s *service) getCustomFoodServingUnits(ctx context.Context) (ServingUnitsResult, error) {
	session, err := s.session(ctx)
	if err != nil {
		return ServingUnitsResult{}, err
	}
	units, err := s.nutrition.CustomFoodServingUnits(ctx, session)
	if err != nil {
		return ServingUnitsResult{}, fail(err)
	}

	codes := units.Units()
	return ServingUnitsResult{
		Units:     codes,
		Count:     len(codes),
		Truncated: len(codes) >= mirroredMaxServingUnits,
	}, nil
}
