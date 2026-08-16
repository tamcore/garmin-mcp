package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetProgressSummaryBetweenDates is the upstream compatibility name.
const ToolGetProgressSummaryBetweenDates = "get_progress_summary_between_dates"

// DefaultMaxProgressActivityTypes bounds how many activity types one progress summary
// reports.
//
// Garmin's own activity-type catalog is the basis: get_activity_types serves the full
// list, and this bound is past that whole catalog rather than a guess at how many types
// an account uses. A summary that outgrows it is truncated with the flag set, never
// silently shortened.
const DefaultMaxProgressActivityTypes = 128

// maxProgressMetricArgumentLen bounds the metric argument. It matches the bound the
// domain client enforces, so the declared contract and the behaviour cannot drift.
const maxProgressMetricArgumentLen = 32

// ProgressActivityStats is one activity type's aggregate for the requested metric.
type ProgressActivityStats struct {
	ActivityType string   `json:"activity_type" jsonschema:"the Garmin activity type key"`
	Count        *int     `json:"count,omitempty" jsonschema:"how many activities contributed"`
	Sum          *float64 `json:"sum,omitempty" jsonschema:"the total over the window"`
	Avg          *float64 `json:"avg,omitempty" jsonschema:"the mean over the window"`
	Min          *float64 `json:"min,omitempty" jsonschema:"the smallest contributing value"`
	Max          *float64 `json:"max,omitempty" jsonschema:"the largest contributing value"`
}

// LogValue reports which activity type was aggregated, never the aggregate.
func (s ProgressActivityStats) LogValue() slog.Value {
	return shape("progressActivityStats", slog.String("count", presence(s.Count != nil)))
}

// ProgressSummary is the aggregated progress for one metric over one window.
//
// It is training data derived from the account's activities: never log it.
type ProgressSummary struct {
	Metric    string `json:"metric" jsonschema:"the metric that was aggregated"`
	StartDate string `json:"start_date" jsonschema:"the first calendar day of the window"`
	EndDate   string `json:"end_date" jsonschema:"the last calendar day of the window"`

	HasData           bool   `json:"has_data" jsonschema:"whether Garmin held a summary for the window"`
	Date              string `json:"date,omitempty" jsonschema:"the day Garmin dated the summary"`
	CountOfActivities *int   `json:"count_of_activities,omitempty" jsonschema:"how many activities the window held"`

	StatsByActivityType []ProgressActivityStats `json:"stats_by_activity_type" jsonschema:"one entry per activity type"`
	Truncated           bool                    `json:"truncated" jsonschema:"whether the list was cut at the bound"`
}

// LogValue reports the shape of the summary, never an aggregate.
func (s ProgressSummary) LogValue() slog.Value {
	return shape("progressSummary",
		slog.Bool("hasData", s.HasData),
		slog.Int("activityTypes", len(s.StatsByActivityType)),
		slog.Bool("truncated", s.Truncated),
	)
}

// getProgressSummaryInput is the strict argument set.
type getProgressSummaryInput struct {
	StartDate string `json:"start_date" jsonschema:"the first calendar day of the window"`
	EndDate   string `json:"end_date" jsonschema:"the last calendar day of the window"`
	Metric    string `json:"metric" jsonschema:"the Garmin metric to aggregate"`
}

func getProgressSummaryBetweenDatesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetProgressSummaryBetweenDates,
			Title: "Get a progress summary between dates",
			Description: "aggregate one activity metric over a date window, grouped by " +
				"parent activity type. Garmin documents elevationGain, duration, distance " +
				"and movingDuration; the endpoint takes a Garmin field name, so another " +
				"metric is passed through rather than refused",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(
			dateProperty("start_date", "the first calendar day of the window"),
			dateProperty("end_date", "the last calendar day of the window, inclusive"),
			Property{
				Name:  "metric",
				Types: []string{typeString},
				Description: "the Garmin metric to aggregate, for example elevationGain, " +
					"duration, distance or movingDuration",
				Pattern:   `^[A-Za-z][A-Za-z0-9]*$`,
				MaxLength: new(maxProgressMetricArgumentLen),
				Required:  true,
			},
		),
	}
}

// registerGetProgressSummaryBetweenDates registers the tool.
func registerGetProgressSummaryBetweenDates(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getProgressSummaryInput) (
		*mcp.CallToolResult, ProgressSummary, error,
	) {
		out, err := svc.readProgressSummary(ctx, in)
		return nil, out, err
	}
	return mcpserver.AddTool(registry,
		getProgressSummaryBetweenDatesContract().Registration(), handler)
}

// readProgressSummary performs the read behind the tool.
func (s *service) readProgressSummary(
	ctx context.Context, in getProgressSummaryInput,
) (ProgressSummary, error) {
	span, err := parseWindow(in.StartDate, in.EndDate, s.limits)
	if err != nil {
		return ProgressSummary{}, err
	}
	metric, err := api.ParseProgressMetric(in.Metric)
	if err != nil {
		return ProgressSummary{}, invalidArgument(
			"metric must be a Garmin field name of letters and digits, such as distance")
	}
	session, err := s.session(ctx)
	if err != nil {
		return ProgressSummary{}, err
	}

	document, err := s.trends().ProgressSummary(ctx, session, span, metric)
	if err != nil {
		return ProgressSummary{}, fail(err)
	}
	return newProgressSummary(span.Start().String(), span.End().String(),
		metric.String(), document), nil
}

// newProgressSummary maps the domain model onto the bounded result.
func newProgressSummary(
	start, end, metric string, document api.ProgressSummaries,
) ProgressSummary {
	out := ProgressSummary{
		Metric:              metric,
		StartDate:           start,
		EndDate:             end,
		StatsByActivityType: []ProgressActivityStats{},
	}
	entry, ok := document.First()
	if !ok {
		return out
	}

	out.HasData = true
	if entry.Date != nil {
		out.Date = *entry.Date
	}
	out.CountOfActivities = optionalInt(entry.CountOfActivities)
	out.StatsByActivityType, out.Truncated = progressStats(entry, metric)
	return out
}

// progressStats renders the per-activity-type aggregates of the requested metric, in a
// stable order and under the bound.
func progressStats(entry api.ProgressSummary, metric string) ([]ProgressActivityStats, bool) {
	out := make([]ProgressActivityStats, 0, len(entry.Stats))
	for _, activityType := range sortedStatKeys(entry.Stats) {
		stats, ok := entry.Stats[activityType][metric]
		if !ok {
			continue
		}
		out = append(out, ProgressActivityStats{
			ActivityType: activityType,
			Count:        optionalInt(stats.Count),
			Sum:          optionalFloat(stats.Sum),
			Avg:          optionalFloat(stats.Avg),
			Min:          optionalFloat(stats.Min),
			Max:          optionalFloat(stats.Max),
		})
	}
	if len(out) > DefaultMaxProgressActivityTypes {
		return out[:DefaultMaxProgressActivityTypes], true
	}
	return out, false
}
