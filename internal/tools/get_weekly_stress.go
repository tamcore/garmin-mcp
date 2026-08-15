package tools

import (
	"context"
	"log/slog"
	"sort"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetWeeklyStress is the upstream compatibility name of the weekly stress tool.
const ToolGetWeeklyStress = "get_weekly_stress"

// Week-count bounds. Source: get_weekly_stress, whose upstream caller defaults to four
// weeks and clamps the request to a year.
const (
	defaultStressWeeks = 4
	maxStressWeeks     = api.MaxStressWeeks
)

// WeeklyStressEntry is one week of the aggregate. Both fields are optional: a week
// Garmin holds nothing for still appears in the answer.
type WeeklyStressEntry struct {
	WeekStart   *string  `json:"week_start,omitempty" jsonschema:"the first day of the week, YYYY-MM-DD"`
	StressValue *float64 `json:"stress_value,omitempty" jsonschema:"the week's aggregate stress value"`
}

// WeeklyStressWindow is the weekly stress aggregate for a window of weeks.
//
// It is health data — never log it, never cache it. The keys are upstream's: end_date,
// weeks_requested, weeks_returned and weekly_data, newest week first.
type WeeklyStressWindow struct {
	EndDate        string `json:"end_date" jsonschema:"the last day of the last week read, YYYY-MM-DD"`
	WeeksRequested int    `json:"weeks_requested" jsonschema:"how many weeks were asked for"`
	WeeksReturned  int    `json:"weeks_returned" jsonschema:"how many weeks this result carries"`

	// Truncated reports that Garmin answered with more weeks than were asked for and
	// the surplus was cut.
	Truncated bool `json:"truncated" jsonschema:"whether the week list was cut at the requested count"`

	WeeklyData []WeeklyStressEntry `json:"weekly_data" jsonschema:"the weeks, newest first"`
}

// LogValue reports the shape of the window, never a reading.
func (w WeeklyStressWindow) LogValue() slog.Value {
	return shape("weeklyStressWindow",
		slog.Int("weeksRequested", w.WeeksRequested),
		slog.Int("weeksReturned", w.WeeksReturned),
		slog.Bool("truncated", w.Truncated),
	)
}

// getWeeklyStressInput is the strict argument set.
type getWeeklyStressInput struct {
	EndDate string `json:"end_date" jsonschema:"the last day of the last week, YYYY-MM-DD"`
	Weeks   *int   `json:"weeks,omitempty" jsonschema:"how many weeks to read, 1 to 52, default 4"`
}

func getWeeklyStressContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetWeeklyStress,
			Title: "Get weekly stress",
			Description: "read the account's weekly stress aggregate for the weeks " +
				"ending at end_date, newest week first. The week count is bounded by " +
				"this server at 52",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(
			dateProperty("end_date", "the last day of the last week to read"),
			Property{
				Name:        "weeks",
				Types:       []string{typeInteger},
				Description: "how many weeks to read, counting back from end_date",
				Minimum:     bound(1),
				Maximum:     bound(maxStressWeeks),
				Default:     defaultStressWeeks,
			},
		),
	}
}

// registerGetWeeklyStress registers the tool.
func registerGetWeeklyStress(registry *mcpserver.Registry, svc *service) error {
	stress, err := stressClient(svc)
	if err != nil {
		return err
	}
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getWeeklyStressInput) (
		*mcp.CallToolResult, WeeklyStressWindow, error,
	) {
		weeks, err := resolveStressWeeks(in.Weeks)
		if err != nil {
			return nil, WeeklyStressWindow{}, err
		}
		end, session, err := svc.resolveStressWeekEnd(ctx, in.EndDate)
		if err != nil {
			return nil, WeeklyStressWindow{}, err
		}
		read, err := stress.WeeklyStress(ctx, session, end, weeks)
		if err != nil {
			return nil, WeeklyStressWindow{}, fail(err)
		}
		return nil, newWeeklyStressWindow(end.String(), weeks, read), nil
	}
	return mcpserver.AddTool(registry, getWeeklyStressContract().Registration(), handler)
}

// resolveStressWeeks applies the manifest default and refuses an out-of-range count.
func resolveStressWeeks(weeks *int) (int, error) {
	value := defaultStressWeeks
	if weeks != nil {
		value = *weeks
	}
	switch {
	case value < 1:
		return 0, invalidArgument("weeks must be at least 1")
	case value > maxStressWeeks:
		return 0, invalidArgument("weeks must not exceed " + strconv.Itoa(maxStressWeeks))
	}
	return value, nil
}

// resolveStressWeekEnd validates the end date before anything is dispatched.
func (s *service) resolveStressWeekEnd(
	ctx context.Context, date string,
) (client.Date, client.Session, error) {
	day, err := parseCalendarDate("end_date", date)
	if err != nil {
		return client.Date{}, client.Session{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return client.Date{}, client.Session{}, err
	}
	return day, session, nil
}

// newWeeklyStressWindow maps the weeks onto the bounded result, newest first.
//
// Source: get_weekly_stress, which sorts the weeks by start date in reverse. A week
// with no start date sorts last rather than being dropped, because a week Garmin held
// nothing for is still an answer.
func newWeeklyStressWindow(
	end string, requested int, weeks []api.WeeklyStress,
) WeeklyStressWindow {
	out := WeeklyStressWindow{
		EndDate:        end,
		WeeksRequested: requested,
		WeeklyData:     make([]WeeklyStressEntry, 0, len(weeks)),
	}
	for _, week := range weeks {
		out.WeeklyData = append(out.WeeklyData, WeeklyStressEntry{
			WeekStart:   week.CalendarDate,
			StressValue: optionalFloat(week.Value),
		})
	}
	sort.SliceStable(out.WeeklyData, func(i, j int) bool {
		return weekStartKey(out.WeeklyData[i]) > weekStartKey(out.WeeklyData[j])
	})
	if len(out.WeeklyData) > requested {
		out.WeeklyData = out.WeeklyData[:requested]
		out.Truncated = true
	}
	out.WeeksReturned = len(out.WeeklyData)
	return out
}

// weekStartKey is the sort key of one week. An absent start date sorts last.
func weekStartKey(entry WeeklyStressEntry) string {
	if entry.WeekStart == nil {
		return ""
	}
	return *entry.WeekStart
}
