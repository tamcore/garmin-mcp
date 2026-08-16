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

// ToolGetVO2MaxTrend is the upstream compatibility name.
const ToolGetVO2MaxTrend = "get_vo2max_trend"

// The sports the VO2 max series is reported for. Garmin names the running series
// "generic"; source: the candidate paths of upstream's _extract_vo2_measurements.
const (
	sportRunning = "running"
	sportCycling = "cycling"
)

// The sanitized labels a trend point names as its source.
const (
	sourceMaxMetrics     = "max_metrics"
	sourceTrainingStatus = "training_status"
	sourceUserSettings   = "user_settings"
)

// VO2MaxPoint is one dated VO2 max estimate.
type VO2MaxPoint struct {
	Date   string  `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`
	VO2Max float64 `json:"vo2_max" jsonschema:"the estimate for that day"`
	Source string  `json:"source" jsonschema:"which Garmin read the estimate came from"`
}

// LogValue reports that a point exists, never the estimate.
func (p VO2MaxPoint) LogValue() slog.Value { return shape("vo2MaxPoint") }

// VO2MaxEstimate is the current profile estimate, reported only when the window held
// no history at all.
type VO2MaxEstimate struct {
	VO2Max float64 `json:"vo2_max" jsonschema:"the estimate Garmin's profile carries"`
	Sport  string  `json:"sport" jsonschema:"which sport the estimate is for"`
	Source string  `json:"source" jsonschema:"which Garmin read it came from"`
}

// LogValue reports that an estimate exists, never the estimate.
func (e VO2MaxEstimate) LogValue() slog.Value { return shape("vo2MaxEstimate") }

// VO2MaxTrend is the VO2 max trend over a bounded window.
//
// A VO2 max is a health reading: never log it. The trend carries only the days on
// which the estimate changed, which is what upstream reports; days_with_data says how
// many days actually carried a measurement, so the shorter list is never mistaken for
// the coverage.
type VO2MaxTrend struct {
	StartDate string `json:"start_date" jsonschema:"the first calendar day of the window"`
	EndDate   string `json:"end_date" jsonschema:"the last calendar day of the window"`

	Sport        string `json:"sport,omitempty" jsonschema:"the sport with the best coverage in the window"`
	DaysWithData int    `json:"days_with_data" jsonschema:"how many days carried a measurement"`
	DataPoints   int    `json:"data_points" jsonschema:"how many entries the trend holds, one per change"`

	FirstVO2Max  *float64 `json:"first_vo2_max,omitempty" jsonschema:"the earliest estimate in the window"`
	LatestVO2Max *float64 `json:"latest_vo2_max,omitempty" jsonschema:"the latest estimate in the window"`
	Change       *float64 `json:"change,omitempty" jsonschema:"latest minus first"`

	Trend    []VO2MaxPoint   `json:"trend" jsonschema:"one entry per day the estimate changed, oldest first"`
	Current  *VO2MaxEstimate `json:"current_vo2_max_estimate,omitempty" jsonschema:"the profile estimate"`
	Note     string          `json:"note,omitempty" jsonschema:"why the profile estimate is reported instead of a history"`
	Coverage TrendCoverage   `json:"coverage" jsonschema:"how complete this trend is"`
}

// LogValue reports the shape of the trend and never an estimate.
func (t VO2MaxTrend) LogValue() slog.Value {
	return shape("vo2MaxTrend",
		slog.Int("points", len(t.Trend)),
		slog.String("current", presence(t.Current != nil)),
		slog.Any("coverage", t.Coverage),
	)
}

func getVO2MaxTrendContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetVO2MaxTrend,
			Title: "Get the VO2 max trend",
			Description: "read the account's VO2 max estimates over a date window. The " +
				"whole window is asked for in one request first; only the days that " +
				"request did not cover are then read one at a time. The estimates are " +
				"smoothed, so the trend carries the days on which the value changed. If no " +
				"history is available the current profile estimate is reported separately " +
				"and never as a historical point. The window is at most 90 days",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(trendWindowProperties(MaxVO2MaxTrendDays)...),
	}
}

// registerGetVO2MaxTrend registers the tool.
func registerGetVO2MaxTrend(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in trendWindowInput) (
		*mcp.CallToolResult, VO2MaxTrend, error,
	) {
		out, err := svc.readVO2MaxTrend(ctx, in.StartDate, in.EndDate)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getVO2MaxTrendContract().Registration(), handler)
}

// vo2Measurement is one sport's estimate for one day, with the read it came from.
type vo2Measurement struct {
	value  float64
	source string
}

// vo2Collector accumulates the per-sport series while the window is walked.
type vo2Collector struct {
	bySport map[string]map[string]vo2Measurement
}

func newVO2Collector() *vo2Collector {
	return &vo2Collector{bySport: map[string]map[string]vo2Measurement{
		sportRunning: {},
		sportCycling: {},
	}}
}

// add records a measurement unless that sport already has one for the day. The first
// source wins, which is what makes the range read authoritative over the per-day
// fallback.
func (c *vo2Collector) add(sport, date string, value client.Number, source string) bool {
	number, ok := value.Float64()
	if !ok {
		return false
	}
	if _, taken := c.bySport[sport][date]; taken {
		return false
	}
	c.bySport[sport][date] = vo2Measurement{value: number, source: source}
	return true
}

// addSection records both sports of one VO2 max section.
func (c *vo2Collector) addSection(
	date string, generic, cycling *api.VO2MaxEntry, source string,
) bool {
	// Both calls must run, so the two results are taken before they are combined.
	running := generic != nil && c.add(sportRunning, date, generic.Value(), source)
	cycled := cycling != nil && c.add(sportCycling, date, cycling.Value(), source)
	return running || cycled
}

// has reports whether any sport already carries the day.
func (c *vo2Collector) has(date string) bool {
	for _, series := range c.bySport {
		if _, ok := series[date]; ok {
			return true
		}
	}
	return false
}

// best picks the sport with the most days, running winning a tie, and returns its
// series ordered by date.
func (c *vo2Collector) best() (string, []VO2MaxPoint) {
	sport := sportRunning
	if len(c.bySport[sportCycling]) > len(c.bySport[sportRunning]) {
		sport = sportCycling
	}
	if len(c.bySport[sport]) == 0 {
		return "", nil
	}

	series := c.bySport[sport]
	points := make([]VO2MaxPoint, 0, len(series))
	for _, date := range sortedStatKeys(series) {
		points = append(points, VO2MaxPoint{
			Date: date, VO2Max: series[date].value, Source: series[date].source,
		})
	}
	return sport, points
}

// readVO2MaxTrend reads the window in one request, then fills the days that request
// did not cover, one at a time.
func (s *service) readVO2MaxTrend(ctx context.Context, start, end string) (VO2MaxTrend, error) {
	window, err := s.resolveTrendWindow(ctx, start, end, MaxVO2MaxTrendDays)
	if err != nil {
		return VO2MaxTrend{}, err
	}

	collector := newVO2Collector()
	s.collectRangeVO2Max(ctx, window, collector)

	read := func(ctx context.Context, day client.Date) (bool, error) {
		if collector.has(day.String()) {
			return true, nil
		}
		document, err := s.trends().VO2MaxFromTrainingStatus(ctx, window.session, day)
		if err != nil {
			return false, err
		}
		section := document.MostRecentVO2Max
		if section == nil {
			return false, nil
		}
		return collector.addSection(day.String(), section.Generic, section.Cycling,
			sourceTrainingStatus), nil
	}

	coverage, err := walkTrendDays(ctx, window.span, read)
	if err != nil {
		return VO2MaxTrend{}, err
	}
	return s.newVO2MaxTrend(ctx, window, collector, coverage), nil
}

// collectRangeVO2Max asks for the whole window in one request.
//
// A failure here is deliberately not fatal: the per-day fallback covers the same
// ground, and refusing the call because the cheap path failed would be worse than
// paying for the expensive one. Nothing is recorded from a failed read.
func (s *service) collectRangeVO2Max(
	ctx context.Context, window trendWindow, collector *vo2Collector,
) {
	metrics, err := s.trends().MaxMetrics(ctx, window.session, window.span)
	if err != nil {
		return
	}
	for _, day := range metrics.Days() {
		date, ok := day.Day()
		if !ok {
			continue
		}
		// Garmin decides what this range read answers with, and it has answered
		// with days outside the one it was asked for. The window is both the
		// caller's question and this tool's declared bound, so a day that cannot
		// be placed inside it is dropped rather than counted.
		parsed, err := client.ParseDate(date)
		if err != nil || !window.span.Contains(parsed) {
			continue
		}
		collector.addSection(date, day.Generic, day.Cycling, sourceMaxMetrics)
	}
}

// newVO2MaxTrend renders the collected series, falling back to the profile estimate
// when the window carried no history at all.
func (s *service) newVO2MaxTrend(
	ctx context.Context, window trendWindow, collector *vo2Collector, coverage TrendCoverage,
) VO2MaxTrend {
	sport, points := collector.best()
	trend := changePoints(points)

	out := VO2MaxTrend{
		StartDate:    window.span.Start().String(),
		EndDate:      window.span.End().String(),
		Sport:        sport,
		DaysWithData: len(points),
		DataPoints:   len(trend),
		Trend:        trend,
		Coverage:     coverage,
	}
	if len(trend) > 0 {
		first, latest := trend[0].VO2Max, trend[len(trend)-1].VO2Max
		change := latest - first
		out.FirstVO2Max, out.LatestVO2Max, out.Change = &first, &latest, &change
		return out
	}

	if current := s.currentVO2Max(ctx, window.session); current != nil {
		out.Current = current
		out.Note = "Garmin held no historical VO2 max for this window, so the current " +
			"profile estimate is reported instead. It is not a historical trend point."
	}
	return out
}

// currentVO2Max reads the profile estimate. A failed read is reported as no estimate
// rather than as a failed call: the trend itself succeeded, it was simply empty.
func (s *service) currentVO2Max(ctx context.Context, session client.Session) *VO2MaxEstimate {
	profile, err := s.trends().ProfileVO2Max(ctx, session)
	if err != nil {
		return nil
	}
	if running, ok := profile.Running(); ok {
		value, _ := running.Float64()
		return &VO2MaxEstimate{VO2Max: value, Sport: sportRunning, Source: sourceUserSettings}
	}
	if cycling, ok := profile.Cycling(); ok {
		value, _ := cycling.Float64()
		return &VO2MaxEstimate{VO2Max: value, Sport: sportCycling, Source: sourceUserSettings}
	}
	return nil
}

// changePoints keeps the days on which the estimate changed.
//
// Source: upstream drops a day whose value equals the previous one, because Garmin's
// estimate is smoothed and repeats for weeks. days_with_data reports the days that
// actually carried a measurement, so the shorter list loses no information.
func changePoints(points []VO2MaxPoint) []VO2MaxPoint {
	out := make([]VO2MaxPoint, 0, len(points))
	previous := 0.0
	for index, point := range points {
		if index > 0 && point.VO2Max == previous {
			continue
		}
		out = append(out, point)
		previous = point.VO2Max
	}
	return out
}
