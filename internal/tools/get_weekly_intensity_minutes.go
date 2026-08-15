package tools

import (
	"context"
	"log/slog"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetWeeklyIntensityMinutes is the upstream compatibility name of the weekly
// intensity-minutes tool.
const ToolGetWeeklyIntensityMinutes = "get_weekly_intensity_minutes"

// daysPerWeek is how the week count becomes the date window this endpoint takes.
// Source: the end - (weeks * 7 - 1) start date the upstream tool computes, because
// this path is the one weekly aggregate keyed by a range rather than by a count.
const daysPerWeek = 7

// WeeklyIntensityMinutes is the account's weekly intensity-minutes aggregate.
//
// The top-level keys are the manifest's and the per-week keys are upstream's curated
// names. It is health data — never log it, never cache it.
type WeeklyIntensityMinutes struct {
	EndDate        string                `json:"end_date" jsonschema:"the last day of the last week read, YYYY-MM-DD"`
	WeeksRequested int                   `json:"weeks_requested" jsonschema:"how many weeks were asked for"`
	WeeksReturned  int                   `json:"weeks_returned" jsonschema:"how many weeks Garmin held"`
	WeeklyData     []WeeklyIntensityWeek `json:"weekly_data" jsonschema:"one entry per week, most recent first"`
}

// WeeklyIntensityWeek is one week of the aggregate.
//
// TotalMinutes is the plain sum of the two measured values. Upstream's comment calls
// it a WHO-weighted total but its code adds them unweighted, and this server repeats
// the arithmetic rather than the comment.
type WeeklyIntensityWeek struct {
	WeekStart       *string `json:"week_start,omitempty" jsonschema:"the first day of the week, YYYY-MM-DD"`
	WeeklyGoal      *int    `json:"weekly_goal,omitempty" jsonschema:"the week's intensity minutes goal"`
	ModerateMinutes *int    `json:"moderate_minutes,omitempty" jsonschema:"moderate intensity minutes"`
	VigorousMinutes *int    `json:"vigorous_minutes,omitempty" jsonschema:"vigorous intensity minutes"`
	TotalMinutes    int     `json:"total_minutes" jsonschema:"moderate plus vigorous minutes"`
}

// LogValue reports the shape of the aggregate, never a minute count.
func (m WeeklyIntensityMinutes) LogValue() slog.Value {
	return shape("weeklyIntensityMinutes",
		slog.Int("weeksRequested", m.WeeksRequested),
		slog.Int("weeksReturned", m.WeeksReturned),
	)
}

// getWeeklyIntensityMinutesInput is the strict argument set.
type getWeeklyIntensityMinutesInput struct {
	EndDate string `json:"end_date" jsonschema:"the last day of the last week, YYYY-MM-DD"`
	Weeks   *int   `json:"weeks,omitempty" jsonschema:"how many weeks to read, 1 to 52, default 4"`
}

func getWeeklyIntensityMinutesContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetWeeklyIntensityMinutes,
			Title: "Get weekly intensity minutes",
			Description: "read the account's weekly moderate and vigorous intensity minutes " +
				"for the weeks ending at end_date, most recent first, with the week's goal " +
				"and the plain sum of the two values",
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

// registerGetWeeklyIntensityMinutes registers the tool.
func registerGetWeeklyIntensityMinutes(registry *mcpserver.Registry, svc *service) error {
	handler := func(
		ctx context.Context, _ *mcp.CallToolRequest, in getWeeklyIntensityMinutesInput,
	) (*mcp.CallToolResult, WeeklyIntensityMinutes, error) {
		weekly, err := svc.weeklyIntensityMinutes(ctx, in)
		return nil, weekly, err
	}
	return mcpserver.AddTool(registry,
		getWeeklyIntensityMinutesContract().Registration(), handler)
}

// weeklyIntensityMinutes turns the week count into the date window this endpoint
// takes, reads it and curates it.
func (s *service) weeklyIntensityMinutes(
	ctx context.Context, in getWeeklyIntensityMinutesInput,
) (WeeklyIntensityMinutes, error) {
	end, err := parseCalendarDate("end_date", in.EndDate)
	if err != nil {
		return WeeklyIntensityMinutes{}, err
	}
	weeks, err := resolveWeeks(in.Weeks)
	if err != nil {
		return WeeklyIntensityMinutes{}, err
	}
	span, err := client.NewDateRange(end.AddDays(-(weeks*daysPerWeek - 1)), end)
	if err != nil {
		return WeeklyIntensityMinutes{}, fail(err)
	}
	session, err := s.session(ctx)
	if err != nil {
		return WeeklyIntensityMinutes{}, err
	}

	entries, err := s.wellness.Daily().WeeklyIntensityMinutes(ctx, session, span)
	if err != nil {
		return WeeklyIntensityMinutes{}, fail(err)
	}
	return newWeeklyIntensityMinutes(end.String(), weeks, entries), nil
}

// newWeeklyIntensityMinutes maps the domain entries onto the curated result.
func newWeeklyIntensityMinutes(
	end string, weeks int, entries []api.WeeklyIntensityEntry,
) WeeklyIntensityMinutes {
	if len(entries) > weeks {
		entries = entries[:weeks]
	}

	weekly := make([]WeeklyIntensityWeek, 0, len(entries))
	for _, entry := range entries {
		moderate := optionalInt(entry.ModerateValue)
		vigorous := optionalInt(entry.VigorousValue)
		weekly = append(weekly, WeeklyIntensityWeek{
			WeekStart:       entry.CalendarDate,
			WeeklyGoal:      optionalInt(entry.WeeklyGoal),
			ModerateMinutes: moderate,
			VigorousMinutes: vigorous,
			TotalMinutes:    valueOrZero(moderate) + valueOrZero(vigorous),
		})
	}
	slices.SortStableFunc(weekly, func(a, b WeeklyIntensityWeek) int {
		return compareWeekStartsDescending(a.WeekStart, b.WeekStart)
	})

	return WeeklyIntensityMinutes{
		EndDate:        end,
		WeeksRequested: weeks,
		WeeksReturned:  len(weekly),
		WeeklyData:     weekly,
	}
}

// valueOrZero reads an optional count as a number, treating absent as none. Source:
// the "or 0" upstream applies before it sums the two values.
func valueOrZero(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
