package tools

import (
	"context"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetWeeklySteps is the upstream compatibility name of the weekly step tool.
const ToolGetWeeklySteps = "get_weekly_steps"

// Week-count bounds. Both are upstream's: the default is the manifest's, and the
// ceiling is the min(weeks, 52) cap the upstream tool applies before it calls Garmin.
const (
	argWeeks             = "weeks"
	defaultWeeksArgument = 4
	maxWeeksArgument     = 52
)

// WeeklySteps is the account's weekly step aggregate.
//
// The top-level keys are the manifest's — end_date, weekly_data, weeks_requested and
// weeks_returned — and the per-week keys are upstream's curated names.
//
// It is health data — never log it, never cache it.
type WeeklySteps struct {
	EndDate        string           `json:"end_date" jsonschema:"the last day of the last week read, YYYY-MM-DD"`
	WeeksRequested int              `json:"weeks_requested" jsonschema:"how many weeks were asked for"`
	WeeksReturned  int              `json:"weeks_returned" jsonschema:"how many weeks Garmin held"`
	WeeklyData     []WeeklyStepWeek `json:"weekly_data" jsonschema:"one entry per week, most recent first"`
}

// WeeklyStepWeek is one week of the aggregate. Every field is optional because a
// week Garmin holds no wellness data for carries no values at all.
type WeeklyStepWeek struct {
	WeekStart             *string  `json:"week_start,omitempty" jsonschema:"the first day of the week, YYYY-MM-DD"`
	TotalSteps            *int     `json:"total_steps,omitempty" jsonschema:"steps taken in the week"`
	AverageSteps          *float64 `json:"average_steps,omitempty" jsonschema:"average steps per day"`
	TotalDistanceMeters   *float64 `json:"total_distance_meters,omitempty" jsonschema:"distance covered in the week"`
	AverageDistanceMeters *float64 `json:"average_distance_meters,omitempty" jsonschema:"average distance per day"`
	DaysWithData          *int     `json:"days_with_data,omitempty" jsonschema:"days of the week with wellness data"`
}

// LogValue reports the shape of the aggregate, never a step count.
func (w WeeklySteps) LogValue() slog.Value {
	return shape("weeklySteps",
		slog.Int("weeksRequested", w.WeeksRequested),
		slog.Int("weeksReturned", w.WeeksReturned),
	)
}

// getWeeklyStepsInput is the strict argument set.
type getWeeklyStepsInput struct {
	EndDate string `json:"end_date" jsonschema:"the last day of the last week, YYYY-MM-DD"`
	Weeks   *int   `json:"weeks,omitempty" jsonschema:"how many weeks to read, 1 to 52, default 4"`
}

func getWeeklyStepsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetWeeklySteps,
			Title: "Get weekly steps",
			Description: "read the account's weekly step totals for the weeks ending at " +
				"end_date, most recent first. A week Garmin holds no data for comes back " +
				"with no values rather than with zeroes",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(
			dateProperty("end_date", "the last day of the last week to read"),
			weeksProperty(),
		),
	}
}

// weeksProperty declares the week-count argument, which is the same strict shape in
// both weekly tools.
func weeksProperty() Property {
	return Property{
		Name:        argWeeks,
		Types:       []string{typeInteger},
		Description: "how many weeks to read, ending at end_date",
		Minimum:     bound(1),
		Maximum:     bound(maxWeeksArgument),
		Default:     defaultWeeksArgument,
	}
}

// resolveWeeks applies the manifest default and refuses an out-of-range week count.
func resolveWeeks(weeks *int) (int, error) {
	value := defaultWeeksArgument
	if weeks != nil {
		value = *weeks
	}
	switch {
	case value < 1:
		return 0, invalidArgument("weeks must be at least 1")
	case value > maxWeeksArgument:
		return 0, invalidArgument("weeks must not exceed " + strconv.Itoa(maxWeeksArgument))
	}
	return value, nil
}

// registerGetWeeklySteps registers the tool.
func registerGetWeeklySteps(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getWeeklyStepsInput) (
		*mcp.CallToolResult, WeeklySteps, error,
	) {
		weekly, err := svc.weeklySteps(ctx, in)
		return nil, weekly, err
	}
	return mcpserver.AddTool(registry, getWeeklyStepsContract().Registration(), handler)
}

// weeklySteps validates the arguments, reads the aggregate and curates it.
func (s *service) weeklySteps(ctx context.Context, in getWeeklyStepsInput) (WeeklySteps, error) {
	end, err := parseCalendarDate("end_date", in.EndDate)
	if err != nil {
		return WeeklySteps{}, err
	}
	weeks, err := resolveWeeks(in.Weeks)
	if err != nil {
		return WeeklySteps{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return WeeklySteps{}, err
	}

	entries, err := s.wellness.Daily().WeeklySteps(ctx, session, end, weeks)
	if err != nil {
		return WeeklySteps{}, fail(err)
	}
	return newWeeklySteps(end.String(), weeks, entries), nil
}

// newWeeklySteps maps the domain entries onto the curated result, most recent first.
func newWeeklySteps(end string, weeks int, entries []api.WeeklyStepsEntry) WeeklySteps {
	if len(entries) > weeks {
		entries = entries[:weeks]
	}

	weekly := make([]WeeklyStepWeek, 0, len(entries))
	for _, entry := range entries {
		week := WeeklyStepWeek{WeekStart: entry.CalendarDate}
		if entry.Values != nil {
			week.TotalSteps = optionalInt(entry.Values.TotalSteps)
			week.AverageSteps = optionalFloat(entry.Values.AverageSteps)
			week.TotalDistanceMeters = optionalFloat(entry.Values.TotalDistance)
			week.AverageDistanceMeters = optionalFloat(entry.Values.AverageDistance)
			week.DaysWithData = optionalInt(entry.Values.WellnessDataDaysCount)
		}
		weekly = append(weekly, week)
	}
	slices.SortStableFunc(weekly, func(a, b WeeklyStepWeek) int {
		return compareWeekStartsDescending(a.WeekStart, b.WeekStart)
	})

	return WeeklySteps{
		EndDate:        end,
		WeeksRequested: weeks,
		WeeksReturned:  len(weekly),
		WeeklyData:     weekly,
	}
}

// compareWeekStartsDescending orders week starts most recent first, with an absent
// start sorting last so a week Garmin dated nothing never displaces a dated one.
func compareWeekStartsDescending(a, b *string) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	return -strings.Compare(*a, *b)
}
