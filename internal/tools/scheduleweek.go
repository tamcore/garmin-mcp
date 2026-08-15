package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolScheduleWeek is the upstream compatibility name of the week-at-a-time schedule.
const ToolScheduleWeek = "schedule_week"

// Per-item outcome labels.
const (
	// scheduleStatusScheduled means Garmin accepted a new calendar entry.
	scheduleStatusScheduled = "scheduled"
	// scheduleStatusAlreadyScheduled means the pre-check found the entry already
	// there, so nothing was sent.
	scheduleStatusAlreadyScheduled = "already_scheduled"
	// scheduleStatusFailed means Garmin refused the entry.
	scheduleStatusFailed = "failed"

	// duplicateCheckChecked means the pre-check answered.
	duplicateCheckChecked = "checked"
	// duplicateCheckFailed means it could not, and the schedule went out anyway.
	duplicateCheckFailed = "failed"
)

// A WeekScheduleOutcome is one day's result.
//
// duplicate_check is reported rather than hidden, because it is the difference
// between "this is not already scheduled" and "nobody could tell": on failed the
// entry was sent without knowing, so a repeat of the same call can duplicate it.
type WeekScheduleOutcome struct {
	WorkoutID      int64  `json:"workout_id" jsonschema:"the workout this item named"`
	CalendarDate   string `json:"calendar_date" jsonschema:"the date this item named"`
	Status         string `json:"status" jsonschema:"scheduled, already_scheduled or failed"`
	DuplicateCheck string `json:"duplicate_check" jsonschema:"checked, or failed when the calendar was unreadable"`
	HTTPStatus     int    `json:"http_status,omitempty" jsonschema:"the HTTP status Garmin answered with"`
	Advice         string `json:"advice,omitempty" jsonschema:"why this item was not scheduled"`
}

// LogValue reports the outcome status, never the workout or the day.
func (o WeekScheduleOutcome) LogValue() slog.Value {
	return shape("weekScheduleOutcome", slog.String("status", o.Status))
}

// A WeekScheduleResult is what the week schedule reports.
type WeekScheduleResult struct {
	Scheduled        []WeekScheduleOutcome `json:"scheduled" jsonschema:"one outcome per requested item, in order"`
	Requested        int                   `json:"requested" jsonschema:"how many items were requested"`
	Applied          int                   `json:"applied" jsonschema:"how many new calendar entries Garmin accepted"`
	AlreadyScheduled int                   `json:"already_scheduled" jsonschema:"how many were already scheduled"`
	Failed           int                   `json:"failed" jsonschema:"how many items Garmin refused"`
}

// LogValue reports the counts, never the schedule.
func (r WeekScheduleResult) LogValue() slog.Value {
	return shape("weekScheduleResult",
		slog.Int("requested", r.Requested),
		slog.Int("applied", r.Applied),
		slog.Int("alreadyScheduled", r.AlreadyScheduled),
		slog.Int("failed", r.Failed),
	)
}

// scheduleWeekInput is the week-schedule argument set.
type scheduleWeekInput struct {
	Week []scheduleEntry `json:"week" jsonschema:"the workouts and the dates to schedule them on"`
}

func scheduleWeekContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolScheduleWeek,
			Title: "Schedule a week of workouts",
			Description: "add a week's workouts to the Garmin Connect calendar in one call. " +
				"Each item is checked against the calendar first and skipped when it is " +
				"already there, but that check fails open: when the calendar cannot be read, " +
				"the entry is still sent and the item reports duplicate_check failed. " +
				"Repeating the call can therefore create duplicate entries, so on a failed or " +
				"ambiguous result read the calendar with get_scheduled_workouts and remove " +
				"any duplicate with unschedule_workout",
			Tier:     policy.TierWrite,
			Category: categoryHealth,
			// Non-idempotent, matching the manifest: a pre-check that fails open is
			// duplicate avoidance, not a guarantee.
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(Property{
			Name:        "week",
			Types:       []string{typeArray},
			Description: "the week's schedule, each an object with workout_id and calendar_date",
			Items:       scheduleEntrySchema(),
			MinItems:    new(1),
			MaxItems:    new(DefaultMaxBatchItems),
			Required:    true,
		}),
	}
}

func registerScheduleWeek(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleWeekInput) (
		*mcp.CallToolResult, WeekScheduleResult, error,
	) {
		requests, err := parseScheduleEntries(in.Week, svc.bounds.MaxBatchItems)
		if err != nil {
			return nil, WeekScheduleResult{}, err
		}
		if len(requests) == 0 {
			return nil, WeekScheduleResult{}, invalidArgument("week must name at least one workout")
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, WeekScheduleResult{}, err
		}
		return nil, svc.scheduleWeek(ctx, session, requests), nil
	}
	return mcpserver.AddTool(registry, scheduleWeekContract().Registration(), handler)
}

// scheduleWeek loops the pre-check and the single-entry schedule.
//
// One item's failure does not abandon the rest, which is what a week-at-a-time tool
// is for: a caller sees which days landed and which did not.
func (s *service) scheduleWeek(
	ctx context.Context, session client.Session, requests []scheduleRequest,
) WeekScheduleResult {
	outcomes := make([]WeekScheduleOutcome, 0, len(requests))
	for _, request := range requests {
		outcomes = append(outcomes, s.scheduleOne(ctx, session, request))
	}
	return newWeekScheduleResult(outcomes)
}

// scheduleOne pre-checks one entry and schedules it unless it is already there.
//
// The pre-check fails open on purpose, matching upstream: a check that cannot answer
// must not block a legitimate schedule. It is reported, so the caller knows the
// difference. The check's own failure is never returned as the item's failure.
func (s *service) scheduleOne(
	ctx context.Context, session client.Session, request scheduleRequest,
) WeekScheduleOutcome {
	outcome := WeekScheduleOutcome{
		WorkoutID:      request.id.Int64(),
		CalendarDate:   request.date.String(),
		DuplicateCheck: duplicateCheckChecked,
	}

	scheduled, err := s.calendar.IsScheduled(ctx, session, request.id, request.date)
	switch {
	case err != nil:
		outcome.DuplicateCheck = duplicateCheckFailed
	case scheduled:
		outcome.Status = scheduleStatusAlreadyScheduled
		return outcome
	}

	result, err := s.workouts.Schedule(ctx, session, request.id, request.date)
	if err != nil {
		outcome.Status = scheduleStatusFailed
		outcome.Advice = fail(err).Error()
		return outcome
	}
	outcome.Status = scheduleStatusScheduled
	outcome.HTTPStatus = result.Status
	return outcome
}

// newWeekScheduleResult tallies the outcomes into the reported result.
func newWeekScheduleResult(outcomes []WeekScheduleOutcome) WeekScheduleResult {
	result := WeekScheduleResult{Scheduled: outcomes, Requested: len(outcomes)}
	for _, outcome := range outcomes {
		switch outcome.Status {
		case scheduleStatusScheduled:
			result.Applied++
		case scheduleStatusAlreadyScheduled:
			result.AlreadyScheduled++
		default:
			result.Failed++
		}
	}
	return result
}
