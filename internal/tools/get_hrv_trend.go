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

// ToolGetHRVTrend is the upstream compatibility name.
const ToolGetHRVTrend = "get_hrv_trend"

// HRVTrendPoint is one day of the HRV trend.
type HRVTrendPoint struct {
	Date string `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`

	LastNightAvgHRVMs      *float64 `json:"last_night_avg_hrv_ms,omitempty" jsonschema:"that night's average"`
	LastNight5MinHighHRVMs *float64 `json:"last_night_5min_high_hrv_ms,omitempty" jsonschema:"that night's 5-minute high"`
	WeeklyAvgHRVMs         *float64 `json:"weekly_avg_hrv_ms,omitempty" jsonschema:"the seven-day average as of that day"`

	Status   string `json:"status,omitempty" jsonschema:"Garmin's status for the night"`
	Feedback string `json:"feedback,omitempty" jsonschema:"Garmin's feedback phrase"`
}

// LogValue reports that a day carried a reading, never the reading.
func (p HRVTrendPoint) LogValue() slog.Value {
	return shape("hrvTrendPoint",
		slog.String("lastNightAvg", presence(p.LastNightAvgHRVMs != nil)))
}

// HRVTrend is the HRV trend over a bounded window.
//
// Every millisecond figure is a health reading: never log it. Coverage says how much of
// the window was actually read, so a short trend is never mistaken for a complete one.
type HRVTrend struct {
	StartDate string `json:"start_date" jsonschema:"the first calendar day of the window"`
	EndDate   string `json:"end_date" jsonschema:"the last calendar day of the window"`

	DaysWithData   int      `json:"days_with_data" jsonschema:"how many days yielded a reading"`
	PeriodAvgHRVMs *float64 `json:"period_avg_hrv_ms,omitempty" jsonschema:"the mean of the nightly averages"`

	Trend    []HRVTrendPoint `json:"trend" jsonschema:"one entry per day that yielded a reading, oldest first"`
	Coverage TrendCoverage   `json:"coverage" jsonschema:"how complete this trend is"`
}

// LogValue reports the shape of the trend and never a reading.
func (t HRVTrend) LogValue() slog.Value {
	return shape("hrvTrend",
		slog.Int("points", len(t.Trend)),
		slog.Any("coverage", t.Coverage),
	)
}

// trendWindowInput is the argument set every windowed trend tool takes.
type trendWindowInput struct {
	StartDate string `json:"start_date" jsonschema:"the first calendar day of the window"`
	EndDate   string `json:"end_date" jsonschema:"the last calendar day of the window"`
}

func getHRVTrendContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetHRVTrend,
			Title: "Get the HRV trend",
			Description: "read the account's heart-rate variability over a date window, " +
				"one Garmin request per day. A single night is too noisy to act on; the " +
				"trend shows baseline shifts. The result reports how many days were read, " +
				"how many held nothing and which days failed, so a short trend is never " +
				"mistaken for a complete one. The window is at most 30 days",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(trendWindowProperties(MaxHRVTrendDays)...),
	}
}

// registerGetHRVTrend registers the tool.
func registerGetHRVTrend(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in trendWindowInput) (
		*mcp.CallToolResult, HRVTrend, error,
	) {
		out, err := svc.readHRVTrend(ctx, in.StartDate, in.EndDate)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getHRVTrendContract().Registration(), handler)
}

// readHRVTrend walks the window one day at a time.
func (s *service) readHRVTrend(ctx context.Context, start, end string) (HRVTrend, error) {
	window, err := s.resolveTrendWindow(ctx, start, end, MaxHRVTrendDays)
	if err != nil {
		return HRVTrend{}, err
	}

	trend := make([]HRVTrendPoint, 0, window.span.Days())
	nightly := make([]float64, 0, window.span.Days())
	read := func(ctx context.Context, day client.Date) (bool, error) {
		document, err := s.trends().HRVForTrend(ctx, window.session, day)
		if err != nil {
			return false, err
		}
		point, ok := newHRVTrendPoint(day.String(), document)
		if !ok {
			return false, nil
		}
		trend = append(trend, point)
		if point.LastNightAvgHRVMs != nil {
			nightly = append(nightly, *point.LastNightAvgHRVMs)
		}
		return true, nil
	}

	coverage, err := walkTrendDays(ctx, window.span, read)
	if err != nil {
		return HRVTrend{}, err
	}
	return HRVTrend{
		StartDate:      window.span.Start().String(),
		EndDate:        window.span.End().String(),
		DaysWithData:   coverage.DaysWithData,
		PeriodAvgHRVMs: meanOf(nightly),
		Trend:          trend,
		Coverage:       coverage,
	}, nil
}

// newHRVTrendPoint maps one day onto a trend point, reporting whether it carried
// anything at all.
func newHRVTrendPoint(date string, document api.HRVDay) (HRVTrendPoint, bool) {
	summary := document.Summary
	if summary == nil {
		return HRVTrendPoint{}, false
	}
	point := HRVTrendPoint{
		Date:                   date,
		LastNightAvgHRVMs:      optionalFloat(summary.NightAverage()),
		LastNight5MinHighHRVMs: optionalFloat(summary.LastNight5MinHigh),
		WeeklyAvgHRVMs:         optionalFloat(summary.WeeklyAvg),
		Status:                 textOrEmpty(summary.Status),
		Feedback:               textOrEmpty(summary.FeedbackPhrase),
	}
	empty := point.LastNightAvgHRVMs == nil && point.LastNight5MinHighHRVMs == nil &&
		point.WeeklyAvgHRVMs == nil && point.Status == "" && point.Feedback == ""
	return point, !empty
}
