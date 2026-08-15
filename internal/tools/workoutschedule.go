package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The upstream compatibility names of the calendar writes.
const (
	ToolScheduleWorkout    = "schedule_workout"
	ToolScheduleWorkouts   = "schedule_workouts"
	ToolUnscheduleWorkout  = "unschedule_workout"
	ToolUnscheduleWorkouts = "unschedule_workouts"
)

// A ScheduleResult reports one calendar entry that was created.
type ScheduleResult struct {
	WorkoutID    int64  `json:"workout_id" jsonschema:"the workout that was scheduled"`
	CalendarDate string `json:"calendar_date" jsonschema:"the date it was scheduled on"`
	Status       int    `json:"status" jsonschema:"the HTTP status Garmin answered with"`
}

// LogValue reports that a workout was scheduled, never which one or when.
func (r ScheduleResult) LogValue() slog.Value {
	return shape("scheduleResult", slog.Int("status", r.Status))
}

// scheduleWorkoutInput is the single-schedule argument set.
type scheduleWorkoutInput struct {
	WorkoutID    int64  `json:"workout_id" jsonschema:"the workout to schedule"`
	CalendarDate string `json:"calendar_date" jsonschema:"the date to schedule it on"`
}

// scheduleDateProperty declares the calendar date a schedule targets.
func scheduleDateProperty() Property {
	return calendarDateProperty(argNameCalendarDate,
		"the date to schedule the workout on, in YYYY-MM-DD form")
}

func scheduleWorkoutContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolScheduleWorkout,
			Title: "Schedule a workout",
			Description: "add one workout from the library to the Garmin Connect calendar on " +
				"one date. Calling it twice for the same workout and date creates two " +
				"calendar entries, so on a failed or ambiguous result read the calendar " +
				"before calling again and remove any duplicate with unschedule_workout",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(workoutIDIntegerProperty(), scheduleDateProperty()),
	}
}

func registerScheduleWorkout(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleWorkoutInput) (
		*mcp.CallToolResult, ScheduleResult, error,
	) {
		date, err := parseCalendarDate(argNameCalendarDate, in.CalendarDate)
		if err != nil {
			return nil, ScheduleResult{}, err
		}
		id, session, err := svc.resolveWorkoutRead(ctx, in.WorkoutID)
		if err != nil {
			return nil, ScheduleResult{}, err
		}

		result, err := svc.workouts.Schedule(ctx, session, id, date)
		if err != nil {
			return nil, ScheduleResult{}, fail(err)
		}
		return nil, ScheduleResult{
			WorkoutID: id.Int64(), CalendarDate: date.String(), Status: result.Status,
		}, nil
	}
	return mcpserver.AddTool(registry, scheduleWorkoutContract().Registration(), handler)
}

// scheduleEntry is one item of the batch schedule.
//
// Upstream's batch also accepts an inline workout document, uploading it first and
// scheduling what came back. That form is not accepted here: it makes one call both
// a create and a calendar write, so a partial failure leaves a caller unable to say
// which half happened. Upload first, then schedule the identifier that came back.
type scheduleEntry struct {
	WorkoutID    int64  `json:"workout_id" jsonschema:"the workout to schedule"`
	CalendarDate string `json:"calendar_date" jsonschema:"the date to schedule it on"`
}

// scheduleWorkoutsInput is the batch-schedule argument set.
type scheduleWorkoutsInput struct {
	Schedules []scheduleEntry `json:"schedules" jsonschema:"the workouts and the dates for them"`
}

func scheduleWorkoutsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolScheduleWorkouts,
			Title: "Schedule several workouts",
			Description: "add several existing workouts to the calendar in one call. Each " +
				"entry is scheduled on its own, so one rejection does not abandon the rest. " +
				"Repeating the call creates duplicate calendar entries",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(Property{
			Name:        "schedules",
			Types:       []string{typeArray},
			Description: "the schedules, each an object with workout_id and calendar_date",
			Items:       scheduleEntrySchema(),
			MinItems:    new(1),
			MaxItems:    new(DefaultMaxBatchItems),
			Required:    true,
		}),
	}
}

// scheduleEntrySchema declares one batch entry.
func scheduleEntrySchema() map[string]any {
	return map[string]any{
		keyType: typeObject,
		keyProperties: map[string]any{
			argNameWorkoutID: map[string]any{
				keyType: typeInteger, keyMinimum: 1,
				keyDescription: "the workout to schedule",
			},
			argNameCalendarDate: map[string]any{
				keyType: typeString, keyFormat: formatDate, "pattern": patternCalendarDate,
				keyDescription: "the date to schedule it on",
			},
		},
		keyRequired:             []any{argNameWorkoutID, argNameCalendarDate},
		keyAdditionalProperties: false,
	}
}

func registerScheduleWorkouts(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleWorkoutsInput) (
		*mcp.CallToolResult, BatchResult, error,
	) {
		requests, err := parseScheduleEntries(in.Schedules, svc.bounds.MaxBatchItems)
		if err != nil {
			return nil, BatchResult{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, BatchResult{}, err
		}
		return nil, svc.scheduleEach(ctx, session, requests), nil
	}
	return mcpserver.AddTool(registry, scheduleWorkoutsContract().Registration(), handler)
}

// scheduleRequest is one validated batch entry.
type scheduleRequest struct {
	id   client.ID
	date client.Date
}

// parseScheduleEntries validates the whole batch before any of it is dispatched, so a
// malformed entry cannot leave half a calendar written.
func parseScheduleEntries(entries []scheduleEntry, limit int) ([]scheduleRequest, error) {
	if err := boundedCount("schedules", len(entries), limit); err != nil {
		return nil, err
	}
	out := make([]scheduleRequest, 0, len(entries))
	for _, entry := range entries {
		id, err := parseIdentifier(argNameWorkoutID, entry.WorkoutID)
		if err != nil {
			return nil, err
		}
		date, err := parseCalendarDate(argNameCalendarDate, entry.CalendarDate)
		if err != nil {
			return nil, err
		}
		out = append(out, scheduleRequest{id: id, date: date})
	}
	return out, nil
}

// scheduleEach loops the single-entry schedule.
func (s *service) scheduleEach(
	ctx context.Context, session client.Session, requests []scheduleRequest,
) BatchResult {
	outcomes := make([]BatchOutcome, 0, len(requests))
	for _, request := range requests {
		result, err := s.workouts.Schedule(ctx, session, request.id, request.date)
		if err != nil {
			outcomes = append(outcomes, failedOutcome(request.id.Int64(), err))
			continue
		}
		outcomes = append(outcomes, BatchOutcome{
			ID: request.id.Int64(), Applied: true, Status: result.Status,
		})
	}
	return newBatchResult(outcomes)
}

// unscheduleWorkoutInput is the single-unschedule argument set.
type unscheduleWorkoutInput struct {
	ScheduledWorkoutID int64 `json:"scheduled_workout_id" jsonschema:"the calendar-entry id"`
}

func unscheduleWorkoutContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolUnscheduleWorkout,
			Title: "Unschedule a workout",
			Description: "remove one entry from the Garmin Connect calendar. The workout " +
				"template itself stays in the library and can be scheduled again. The " +
				"identifier is the calendar-entry id, not the workout's own id",
			Tier:        policy.TierDestructive,
			Category:    categoryHealth,
			Annotations: destructiveAnnotations(),
		},
		Schema: NewSchema(scheduledWorkoutIDProperty()),
	}
}

func registerUnscheduleWorkout(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in unscheduleWorkoutInput) (
		*mcp.CallToolResult, DeletionResult, error,
	) {
		id, err := parseIdentifier("scheduled_workout_id", in.ScheduledWorkoutID)
		if err != nil {
			return nil, DeletionResult{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, DeletionResult{}, err
		}

		result, err := svc.workouts.Unschedule(ctx, session, id)
		if err != nil {
			return nil, DeletionResult{}, fail(err)
		}
		return nil, DeletionResult{ID: id.Int64(), Deleted: true, Status: result.Status}, nil
	}
	return mcpserver.AddTool(registry, unscheduleWorkoutContract().Registration(), handler)
}

// unscheduleWorkoutsInput is the batch-unschedule argument set.
type unscheduleWorkoutsInput struct {
	ScheduledWorkoutIDs []int64 `json:"scheduled_workout_ids" jsonschema:"the calendar-entry ids"`
}

func unscheduleWorkoutsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolUnscheduleWorkouts,
			Title: "Unschedule several workouts",
			Description: "remove several entries from the Garmin Connect calendar in one " +
				"call. The workout templates stay in the library. Each identifier is a " +
				"calendar-entry id, not a workout id",
			Tier:        policy.TierDestructive,
			Category:    categoryHealth,
			Annotations: destructiveAnnotations(),
		},
		Schema: NewSchema(identifierListProperty("scheduled_workout_ids",
			"the calendar-entry identifiers to remove")),
	}
}

func registerUnscheduleWorkouts(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in unscheduleWorkoutsInput) (
		*mcp.CallToolResult, BatchResult, error,
	) {
		ids, err := parseIdentifierList(
			"scheduled_workout_ids", in.ScheduledWorkoutIDs, svc.bounds.MaxBatchItems)
		if err != nil {
			return nil, BatchResult{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, BatchResult{}, err
		}
		return nil, svc.removeEach(ctx, session, ids, svc.workouts.Unschedule), nil
	}
	return mcpserver.AddTool(registry, unscheduleWorkoutsContract().Registration(), handler)
}
