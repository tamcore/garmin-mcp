package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetHydrationData is the upstream compatibility name of the hydration tool.
const ToolGetHydrationData = "get_hydration_data"

// Hydration is one day of fluid intake.
//
// Every figure is optional: a day nobody logged carries a document with no intake, and
// an account that never set a goal carries none. It is health data: never log it.
type Hydration struct {
	Date    string `json:"date" jsonschema:"the calendar day that was requested, YYYY-MM-DD"`
	HasData bool   `json:"has_data" jsonschema:"whether Garmin held hydration data for the day"`

	ValueML          *float64 `json:"value_ml,omitempty" jsonschema:"the fluid logged for the day, in millilitres"`
	GoalML           *float64 `json:"goal_ml,omitempty" jsonschema:"the day's hydration goal, in millilitres"`
	DailyAverageML   *float64 `json:"daily_average_ml,omitempty" jsonschema:"the daily average, in millilitres"`
	SweatLossML      *float64 `json:"sweat_loss_ml,omitempty" jsonschema:"estimated sweat loss, in millilitres"`
	ActivityIntakeML *float64 `json:"activity_intake_ml,omitempty" jsonschema:"activity intake, in millilitres"`

	LastEntryLocal *string `json:"last_entry_local,omitempty" jsonschema:"the local time of the last entry"`
}

// LogValue reports the shape of the day and never a figure.
func (h Hydration) LogValue() slog.Value {
	return shape("hydration",
		slog.Bool("hasData", h.HasData),
		slog.String("goal", presence(h.GoalML != nil)),
	)
}

// getHydrationDataInput is the strict argument set: one calendar day.
type getHydrationDataInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getHydrationDataContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetHydrationData,
			Title: "Get hydration data",
			Description: "read one calendar day of the account's hydration: the fluid " +
				"logged, the day's goal, the account's daily average, the estimated sweat " +
				"loss and the intake recorded during activities",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetHydrationData registers the tool.
func registerGetHydrationData(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getHydrationDataInput) (
		*mcp.CallToolResult, Hydration, error,
	) {
		out, err := svc.readHydration(ctx, in.Date)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getHydrationDataContract().Registration(), handler)
}

// readHydration performs the read behind the tool.
func (s *service) readHydration(ctx context.Context, date string) (Hydration, error) {
	day, session, err := s.resolveDateOnlyRead(ctx, date)
	if err != nil {
		return Hydration{}, err
	}
	document, err := s.wellness.Cardio().Hydration(ctx, session, day)
	if err != nil {
		return Hydration{}, fail(err)
	}
	return newHydration(day.String(), document), nil
}

// newHydration maps the domain model onto the curated result.
func newHydration(date string, day api.DailyHydration) Hydration {
	out := Hydration{
		Date:             date,
		ValueML:          optionalFloat(day.ValueInML),
		GoalML:           optionalFloat(day.GoalInML),
		DailyAverageML:   optionalFloat(day.DailyAverageInML),
		SweatLossML:      optionalFloat(day.SweatLossInML),
		ActivityIntakeML: optionalFloat(day.ActivityIntakeInML),
		LastEntryLocal:   optionalText(day.LastEntryTimestampLocal),
	}
	out.HasData = out.ValueML != nil || out.GoalML != nil || out.DailyAverageML != nil
	return out
}
