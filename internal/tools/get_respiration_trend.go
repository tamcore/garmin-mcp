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

// ToolGetRespirationTrend is the upstream compatibility name.
const ToolGetRespirationTrend = "get_respiration_trend"

// RespirationTrendPoint is one day of the respiration trend.
type RespirationTrendPoint struct {
	Date string `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`

	AvgWakingBreathsPerMin *float64 `json:"avg_waking_breaths_per_min,omitempty" jsonschema:"the average while awake"`
	AvgSleepBreathsPerMin  *float64 `json:"avg_sleep_breaths_per_min,omitempty" jsonschema:"the average while asleep"`
	HighestBreathsPerMin   *float64 `json:"highest_breaths_per_min,omitempty" jsonschema:"the day's highest reading"`
	LowestBreathsPerMin    *float64 `json:"lowest_breaths_per_min,omitempty" jsonschema:"the day's lowest reading"`
}

// LogValue reports that a day carried a reading, never the reading.
func (p RespirationTrendPoint) LogValue() slog.Value {
	return shape("respirationTrendPoint",
		slog.String("avgSleep", presence(p.AvgSleepBreathsPerMin != nil)))
}

// RespirationTrend is the overnight breathing-rate trend over a bounded window.
//
// Every rate is a health reading: never log it. Coverage says how much of the window
// was actually read.
type RespirationTrend struct {
	StartDate string `json:"start_date" jsonschema:"the first calendar day of the window"`
	EndDate   string `json:"end_date" jsonschema:"the last calendar day of the window"`

	DaysWithData         int      `json:"days_with_data" jsonschema:"how many days yielded a reading"`
	PeriodAvgSleepPerMin *float64 `json:"period_avg_sleep_breaths_per_min,omitempty" jsonschema:"the mean sleep rate"`

	Trend    []RespirationTrendPoint `json:"trend" jsonschema:"one entry per day that yielded a reading, oldest first"`
	Coverage TrendCoverage           `json:"coverage" jsonschema:"how complete this trend is"`
}

// LogValue reports the shape of the trend and never a reading.
func (t RespirationTrend) LogValue() slog.Value {
	return shape("respirationTrend",
		slog.Int("points", len(t.Trend)),
		slog.Any("coverage", t.Coverage),
	)
}

func getRespirationTrendContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetRespirationTrend,
			Title: "Get the respiration trend",
			Description: "read the account's overnight breathing rate over a date window, " +
				"one Garmin request per day. A resting rate above the personal baseline is " +
				"an early sign of poor recovery. The result reports how many days were " +
				"read, how many held nothing and which days failed. The window is at most " +
				"30 days",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(trendWindowProperties(MaxRespirationTrendDays)...),
	}
}

// registerGetRespirationTrend registers the tool.
func registerGetRespirationTrend(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in trendWindowInput) (
		*mcp.CallToolResult, RespirationTrend, error,
	) {
		out, err := svc.readRespirationTrend(ctx, in.StartDate, in.EndDate)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getRespirationTrendContract().Registration(), handler)
}

// readRespirationTrend walks the window one day at a time.
//
// It reads the health tier's daily respiration document through the cardio client
// rather than an endpoint of its own: the trend is the same document once per day, and
// the sanitized operation label is what separates the two in a log line.
func (s *service) readRespirationTrend(
	ctx context.Context, start, end string,
) (RespirationTrend, error) {
	window, err := s.resolveTrendWindow(ctx, start, end, MaxRespirationTrendDays)
	if err != nil {
		return RespirationTrend{}, err
	}

	trend := make([]RespirationTrendPoint, 0, window.span.Days())
	sleeping := make([]float64, 0, window.span.Days())
	read := func(ctx context.Context, day client.Date) (bool, error) {
		document, err := s.wellness.Cardio().RespirationTrendDay(ctx, window.session, day)
		if err != nil {
			return false, err
		}
		point, ok := newRespirationTrendPoint(day.String(), document)
		if !ok {
			return false, nil
		}
		trend = append(trend, point)
		if point.AvgSleepBreathsPerMin != nil {
			sleeping = append(sleeping, *point.AvgSleepBreathsPerMin)
		}
		return true, nil
	}

	coverage, err := walkTrendDays(ctx, window.span, read)
	if err != nil {
		return RespirationTrend{}, err
	}
	return RespirationTrend{
		StartDate:            window.span.Start().String(),
		EndDate:              window.span.End().String(),
		DaysWithData:         coverage.DaysWithData,
		PeriodAvgSleepPerMin: meanOf(sleeping),
		Trend:                trend,
		Coverage:             coverage,
	}, nil
}

// newRespirationTrendPoint maps one day onto a trend point, reporting whether it
// carried anything at all.
func newRespirationTrendPoint(
	date string, document api.DailyRespiration,
) (RespirationTrendPoint, bool) {
	point := RespirationTrendPoint{
		Date:                   date,
		AvgWakingBreathsPerMin: optionalFloat(document.AvgWakingRespirationValue),
		AvgSleepBreathsPerMin:  optionalFloat(document.AvgSleepRespirationValue),
		HighestBreathsPerMin:   optionalFloat(document.HighestRespirationValue),
		LowestBreathsPerMin:    optionalFloat(document.LowestRespirationValue),
	}
	empty := point.AvgWakingBreathsPerMin == nil && point.AvgSleepBreathsPerMin == nil &&
		point.HighestBreathsPerMin == nil && point.LowestBreathsPerMin == nil
	return point, !empty
}
