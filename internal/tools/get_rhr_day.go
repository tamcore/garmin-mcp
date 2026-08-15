package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetRestingHeartRateDay is the upstream compatibility name of the resting
// heart-rate tool.
const ToolGetRestingHeartRateDay = "get_rhr_day"

// RestingHeartRateDay is one day of resting heart rate.
//
// The endpoint answers one metric per request and echoes the requested day even when
// it holds no reading for it, so an absent value is a normal state rather than a
// failure. It is health data: never log it.
type RestingHeartRateDay struct {
	Date    string `json:"date" jsonschema:"the calendar day that was requested, YYYY-MM-DD"`
	HasData bool   `json:"has_data" jsonschema:"whether Garmin held a resting heart rate for the day"`

	RestingBPM *float64 `json:"resting_heart_rate_bpm,omitempty" jsonschema:"the resting heart rate in beats per minute"`
}

// LogValue reports the shape of the day and never the reading.
func (r RestingHeartRateDay) LogValue() slog.Value {
	return shape("restingHeartRateDay", slog.Bool("hasData", r.HasData))
}

// getRestingHeartRateDayInput is the strict argument set: one calendar day.
type getRestingHeartRateDayInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getRestingHeartRateDayContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetRestingHeartRateDay,
			Title: "Get resting heart rate for a day",
			Description: "read the account's resting heart rate for one calendar day. A " +
				"day the account wore no device carries no reading, which is reported " +
				"rather than treated as a failure",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetRestingHeartRateDay registers the tool.
func registerGetRestingHeartRateDay(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getRestingHeartRateDayInput) (
		*mcp.CallToolResult, RestingHeartRateDay, error,
	) {
		out, err := svc.readRestingHeartRateDay(ctx, in.Date)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getRestingHeartRateDayContract().Registration(), handler)
}

// readRestingHeartRateDay performs the read behind the tool.
func (s *service) readRestingHeartRateDay(
	ctx context.Context, date string,
) (RestingHeartRateDay, error) {
	read, err := s.resolveDailyRead(ctx, date)
	if err != nil {
		return RestingHeartRateDay{}, err
	}
	day, err := s.wellness.Cardio().RestingHeartRateDay(ctx, read.session, read.name, read.date)
	if err != nil {
		return RestingHeartRateDay{}, fail(err)
	}
	return newRestingHeartRateDay(read.date, day), nil
}

// newRestingHeartRateDay maps the domain model onto the curated result.
func newRestingHeartRateDay(day client.Date, stats api.RestingHeartRateDay) RestingHeartRateDay {
	out := RestingHeartRateDay{Date: day.String()}
	reading, ok := stats.RestingHeartRate(day)
	if !ok {
		return out
	}
	out.HasData = true
	out.RestingBPM = optionalFloat(reading)
	return out
}
