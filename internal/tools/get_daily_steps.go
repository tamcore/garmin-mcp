package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetDailySteps is the upstream compatibility name of the daily step aggregate
// tool.
const ToolGetDailySteps = "get_daily_steps"

// maxDailyStepDays bounds the day records one call returns. It is above the widest
// window the request layer allows, so it cuts nothing a permitted window produces.
const maxDailyStepDays = 512

// DailyStepsWindow is the account's daily step aggregate over an inclusive window.
//
// It is health data — never log it, never cache it. A step count and a distance are
// both readings.
//
// The list is **not** aligned to the requested window: Garmin returns a record per
// day it holds, so a day the watch was not worn is simply missing, and the order is
// not promised. Each day carries its own date, which is the only thing a caller may
// key on.
type DailyStepsWindow struct {
	StartDate string `json:"start_date" jsonschema:"the inclusive first day, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"the inclusive last day, YYYY-MM-DD"`

	// Days are the per-day records Garmin held, in the order it sent them.
	Days []DailyStepDay `json:"days" jsonschema:"the per-day records Garmin held, keyed by date"`

	// Count is how many day records this result carries. It is not the length of
	// the requested window.
	Count int `json:"count" jsonschema:"how many day records this result carries"`

	// Truncated reports that the list was cut at this server's bound.
	Truncated bool `json:"truncated" jsonschema:"whether the list was cut at this server's bound"`
}

// DailyStepDay is one calendar day of the aggregate.
//
// This is not the get_steps_data record: that tool returns fifteen-minute buckets.
// Every field is optional, because four fields present across three days of one
// account is not a guarantee.
type DailyStepDay struct {
	// Date identifies the record. A caller must match on it rather than assume the
	// list lines up with the requested window.
	Date *string `json:"date,omitempty" jsonschema:"the calendar day of this record, YYYY-MM-DD"`

	TotalSteps *int `json:"total_steps,omitempty" jsonschema:"steps taken on the day"`

	// TotalDistance is unitless on purpose: neither the document nor upstream
	// states a unit. It is presumably metres, and this server will not label it so
	// until a source says as much.
	TotalDistance *float64 `json:"total_distance,omitempty" jsonschema:"distance covered, unit unstated by Garmin"`

	StepGoal *int `json:"step_goal,omitempty" jsonschema:"the day's step goal"`
}

// LogValue reports the shape of the window, never a step count.
func (w DailyStepsWindow) LogValue() slog.Value {
	return shape("dailyStepsWindow",
		slog.Int("days", w.Count),
		slog.Bool("truncated", w.Truncated),
	)
}

// getDailyStepsInput is the strict argument set: an inclusive date window.
type getDailyStepsInput struct {
	StartDate string `json:"start_date" jsonschema:"the inclusive first day, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"the inclusive last day, YYYY-MM-DD"`
}

func getDailyStepsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetDailySteps,
			Title: "Get daily steps",
			Description: "read the account's daily step aggregate over an inclusive date " +
				"window, oldest first. Garmin answers at most 28 days per request, so a " +
				"wider window is read in parts and returned as one list. The window is " +
				"bounded by this server; narrow it if the call is refused",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(
			dateProperty("start_date", "the inclusive first day of the window"),
			dateProperty("end_date", "the inclusive last day of the window"),
		),
	}
}

// registerGetDailySteps registers the tool.
func registerGetDailySteps(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in getDailyStepsInput) (
		*mcp.CallToolResult, DailyStepsWindow, error,
	) {
		window, err := svc.dailySteps(ctx, in)
		return nil, window, err
	}
	return mcpserver.AddTool(registry, getDailyStepsContract().Registration(), handler)
}

// dailySteps validates the window, reads it and bounds the list.
func (s *service) dailySteps(ctx context.Context, in getDailyStepsInput) (DailyStepsWindow, error) {
	span, err := parseWindow(in.StartDate, in.EndDate, s.limits)
	if err != nil {
		return DailyStepsWindow{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return DailyStepsWindow{}, err
	}

	entries, err := s.wellness.Daily().DailySteps(ctx, session, span)
	if err != nil {
		return DailyStepsWindow{}, fail(err)
	}

	truncated := len(entries) > maxDailyStepDays
	if truncated {
		entries = entries[:maxDailyStepDays]
	}
	days := make([]DailyStepDay, 0, len(entries))
	for _, entry := range entries {
		days = append(days, DailyStepDay{
			Date:          entry.CalendarDate,
			TotalSteps:    optionalInt(entry.TotalSteps),
			TotalDistance: optionalFloat(entry.TotalDistance),
			StepGoal:      optionalInt(entry.StepGoal),
		})
	}
	return DailyStepsWindow{
		StartDate: span.Start().String(),
		EndDate:   span.End().String(),
		Days:      days,
		Count:     len(days),
		Truncated: truncated,
	}, nil
}
