package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetRespirationSummary is the upstream compatibility name of the compact
// respiration tool.
const ToolGetRespirationSummary = "get_respiration_summary"

// RespirationSummary is one day of breathing rate without the series.
//
// It reads the same Garmin document as get_respiration_data and keeps the scalars, so
// the two tools are one read with two views rather than two requests. It is health
// data: never log it.
type RespirationSummary struct {
	Date    string `json:"date" jsonschema:"the calendar day that was requested, YYYY-MM-DD"`
	HasData bool   `json:"has_data" jsonschema:"whether Garmin held respiration data for the day"`

	LowestBreathsPerMin    *float64 `json:"lowest_breaths_per_min,omitempty" jsonschema:"the day's lowest reading"`
	HighestBreathsPerMin   *float64 `json:"highest_breaths_per_min,omitempty" jsonschema:"the day's highest reading"`
	AvgWakingBreathsPerMin *float64 `json:"avg_waking_breaths_per_min,omitempty" jsonschema:"the average while awake"`
	AvgSleepBreathsPerMin  *float64 `json:"avg_sleep_breaths_per_min,omitempty" jsonschema:"the average while asleep"`

	AvgNextNightBPM *float64 `json:"avg_tomorrow_sleep_breaths_per_min,omitempty" jsonschema:"the coming night's average"`
}

// LogValue reports the shape of the day and never a reading.
func (r RespirationSummary) LogValue() slog.Value {
	return shape("respirationSummary", slog.Bool("hasData", r.HasData))
}

// getRespirationSummaryInput is the strict argument set: one calendar day.
type getRespirationSummaryInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getRespirationSummaryContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetRespirationSummary,
			Title: "Get respiration summary",
			Description: "read one calendar day of the account's breathing rate as a " +
				"compact summary: the day's lowest and highest reading and the waking " +
				"and sleeping averages. The intraday series is not returned",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetRespirationSummary registers the tool.
func registerGetRespirationSummary(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getRespirationSummaryInput) (
		*mcp.CallToolResult, RespirationSummary, error,
	) {
		out, err := svc.readRespirationSummary(ctx, in.Date)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getRespirationSummaryContract().Registration(), handler)
}

// readRespirationSummary performs the read behind the tool.
func (s *service) readRespirationSummary(
	ctx context.Context, date string,
) (RespirationSummary, error) {
	day, session, err := s.resolveDateOnlyRead(ctx, date)
	if err != nil {
		return RespirationSummary{}, err
	}
	document, err := s.wellness.Cardio().RespirationSummary(ctx, session, day)
	if err != nil {
		return RespirationSummary{}, fail(err)
	}
	return newRespirationSummary(day.String(), document), nil
}

// newRespirationSummary maps the domain model onto the compact result.
func newRespirationSummary(date string, day api.DailyRespiration) RespirationSummary {
	out := RespirationSummary{
		Date:                   date,
		LowestBreathsPerMin:    optionalFloat(day.LowestRespirationValue),
		HighestBreathsPerMin:   optionalFloat(day.HighestRespirationValue),
		AvgWakingBreathsPerMin: optionalFloat(day.AvgWakingRespirationValue),
		AvgSleepBreathsPerMin:  optionalFloat(day.AvgSleepRespirationValue),
		AvgNextNightBPM:        optionalFloat(day.AvgTomorrowSleepRespirationValue),
	}
	out.HasData = out.LowestBreathsPerMin != nil || out.HighestBreathsPerMin != nil ||
		out.AvgWakingBreathsPerMin != nil || out.AvgSleepBreathsPerMin != nil
	return out
}
