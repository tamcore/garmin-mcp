package tools

import (
	"context"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetStatsRange is the upstream compatibility name.
const ToolGetStatsRange = "get_stats_range"

// MaxStatsRangeDays is Garmin's own window limit for the per-day stats aggregate.
// Source: health_wellness.py:514.
const MaxStatsRangeDays = 28

// StatsRange is the per-day calorie and step totals over an inclusive window. It is
// health data: never log it.
type StatsRange struct {
	StartDate string          `json:"start_date" jsonschema:"the inclusive first day, YYYY-MM-DD"`
	EndDate   string          `json:"end_date" jsonschema:"the inclusive last day, YYYY-MM-DD"`
	Days      []StatsRangeDay `json:"days" jsonschema:"one row per day of the window, oldest first"`
	Count     int             `json:"count" jsonschema:"how many rows this result carries"`
}

// StatsRangeDay is one calendar day of the window.
type StatsRangeDay struct {
	Date            string   `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`
	TotalCalories   *float64 `json:"total_calories,omitempty" jsonschema:"the day's total calories"`
	ActiveCalories  *float64 `json:"active_calories,omitempty" jsonschema:"the day's active calories"`
	RestingCalories *float64 `json:"resting_calories,omitempty" jsonschema:"the day's resting calories"`
	Steps           *int     `json:"steps,omitempty" jsonschema:"the day's total steps"`
	HasData         bool     `json:"has_data" jsonschema:"whether Garmin returned anything for the day"`
	IsPartial       bool     `json:"is_partial" jsonschema:"whether the day is today and still accumulating"`
}

// LogValue reports the shape of the window, never a reading.
func (r StatsRange) LogValue() slog.Value {
	return shape("statsRange", slog.Int("days", r.Count))
}

// statsRangeInput is the strict argument set: an inclusive window.
type statsRangeInput struct {
	StartDate string `json:"start_date" jsonschema:"the inclusive first day, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"the inclusive last day, YYYY-MM-DD"`
}

func getStatsRangeContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetStatsRange,
			Title: "Get daily calories and steps for a window",
			Description: "read total, active and resting calories and steps for every day " +
				"of an inclusive window, one row per day. A day Garmin holds nothing for " +
				"reports has_data false with no values rather than zeros, and today (the " +
				"server's local date) reports is_partial true because its totals cover " +
				"only the elapsed hours: " +
				"exclude both before averaging",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(trendWindowProperties(MaxStatsRangeDays, reasonEndpointLimits)...),
	}
}

// registerGetStatsRange registers the tool.
func registerGetStatsRange(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in statsRangeInput) (
		*mcp.CallToolResult, StatsRange, error,
	) {
		out, err := svc.readStatsRange(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getStatsRangeContract().Registration(), handler)
}

// readStatsRange reads the calorie and the step series and joins them by day.
func (s *service) readStatsRange(ctx context.Context, in statsRangeInput) (StatsRange, error) {
	span, err := parseCappedWindow(in.StartDate, in.EndDate, s.limits, MaxStatsRangeDays)
	if err != nil {
		return StatsRange{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return StatsRange{}, err
	}
	calories, err := s.statsSeries(ctx, session, span, client.StatsTypeCalories)
	if err != nil {
		return StatsRange{}, err
	}
	steps, err := s.statsSeries(ctx, session, span, client.StatsTypeSteps)
	if err != nil {
		return StatsRange{}, err
	}

	today := s.now().Format(time.DateOnly)
	days := make([]StatsRangeDay, 0, span.Days())
	for offset := range span.Days() {
		date := span.Start().AddDays(offset).String()
		day := StatsRangeDay{Date: date, IsPartial: date == today}
		if values, ok := calories[date]; ok {
			day.HasData = true
			day.TotalCalories = optionalFloat(values.TotalCalories)
			day.ActiveCalories = optionalFloat(values.ActiveCalories)
			day.RestingCalories = optionalFloat(values.RestingCalories)
		}
		if values, ok := steps[date]; ok {
			day.HasData = true
			day.Steps = optionalInt(values.TotalSteps)
		}
		days = append(days, day)
	}
	return StatsRange{
		StartDate: span.Start().String(), EndDate: span.End().String(),
		Days: days, Count: len(days),
	}, nil
}

// statsSeries reads one series of the window, keyed by calendar day.
func (s *service) statsSeries(
	ctx context.Context, session client.Session, span client.DateRange, statsType client.StatsType,
) (map[string]api.DailyStatsValues, error) {
	entries, err := s.wellness.Daily().StatsRange(ctx, session, span, statsType)
	if err != nil {
		return nil, fail(err)
	}
	out := make(map[string]api.DailyStatsValues, len(entries))
	for _, entry := range entries {
		if entry.CalendarDate != nil {
			out[*entry.CalendarDate] = entry.Values
		}
	}
	return out, nil
}
