package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The upstream compatibility names of the calendar reads.
const (
	ToolGetScheduledWorkouts    = "get_scheduled_workouts"
	ToolGetTrainingPlanWorkouts = "get_training_plan_workouts"
)

// DefaultMaxScheduledWorkouts bounds a returned calendar listing. The date window is
// bounded too, but a training plan can put several entries on one day, so the entry
// count needs a ceiling of its own.
const DefaultMaxScheduledWorkouts = 500

// A ScheduledWorkoutEntry is one calendar entry.
//
// It is health material: it says what training a person planned and whether they did
// it. The field set is upstream's curated shape, so a client that already reads
// garmin_mcp finds the same keys.
type ScheduledWorkoutEntry struct {
	Date               *string `json:"date,omitempty" jsonschema:"the calendar day, YYYY-MM-DD"`
	ScheduledWorkoutID *int64  `json:"scheduled_workout_id,omitempty" jsonschema:"the calendar-entry id"`
	WorkoutID          *int64  `json:"workout_id,omitempty" jsonschema:"the workout template id, if any"`
	WorkoutUUID        *string `json:"workout_uuid,omitempty" jsonschema:"the identifier adaptive plans use"`
	TrainingPlanID     *int64  `json:"training_plan_id,omitempty" jsonschema:"the plan this entry belongs to"`
	FBTAdaptivePlanID  *int64  `json:"fbt_adaptive_plan_id,omitempty" jsonschema:"the adaptive plan this entry belongs to"`
	TPType             *string `json:"tp_type,omitempty" jsonschema:"Garmin's plan type code"`
	TrainingPlan       *string `json:"training_plan,omitempty" jsonschema:"the plan name"`
	Name               *string `json:"name,omitempty" jsonschema:"the entry name"`
	Sport              *string `json:"sport,omitempty" jsonschema:"the sport key, for example running"`
	WorkoutType        *string `json:"workout_type,omitempty" jsonschema:"Garmin's intent label"`

	Completed bool `json:"completed" jsonschema:"whether an activity was recorded against this entry"`
	IsRestDay bool `json:"is_rest_day" jsonschema:"whether Garmin marked the day as rest"`
	IsRaceDay bool `json:"is_race_day" jsonschema:"whether Garmin marked the day as a race"`

	EstimatedDurationSeconds *int     `json:"estimated_duration_seconds,omitempty" jsonschema:"the planned duration"`
	EstimatedDistanceMeters  *float64 `json:"estimated_distance_meters,omitempty" jsonschema:"the planned distance"`
	ActivityID               *int64   `json:"activity_id,omitempty" jsonschema:"the recorded activity"`
}

// A ScheduledWorkoutList is the bounded calendar listing.
type ScheduledWorkoutList struct {
	ScheduledWorkouts []ScheduledWorkoutEntry `json:"scheduled_workouts" jsonschema:"the calendar entries"`
	Count             int                     `json:"count" jsonschema:"how many entries this result carries"`
	StartDate         string                  `json:"start_date" jsonschema:"the inclusive first day that was read"`
	EndDate           string                  `json:"end_date" jsonschema:"the inclusive last day that was read"`
	Truncated         bool                    `json:"truncated" jsonschema:"whether the listing was cut"`
}

// LogValue reports the listing size, never an entry.
func (l ScheduledWorkoutList) LogValue() slog.Value {
	return shape("scheduledWorkoutList",
		slog.Int("entries", len(l.ScheduledWorkouts)),
		slog.Bool("truncated", l.Truncated),
	)
}

// scheduledWorkoutsInput is the calendar-read argument set.
type scheduledWorkoutsInput struct {
	StartDate string `json:"start_date" jsonschema:"the inclusive first day, YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"the inclusive last day, YYYY-MM-DD"`
}

func getScheduledWorkoutsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetScheduledWorkouts,
			Title: "Get the scheduled workouts",
			Description: "read the Garmin Connect workout calendar between two dates, " +
				"inclusive, with each entry's date, identifiers and completion status. The " +
				"scheduled_workout_id it returns is the calendar-entry id unschedule_workout " +
				"takes, which is not the workout's own id",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(
			dateProperty("start_date", "inclusive first day of the window"),
			dateProperty("end_date", "inclusive last day of the window"),
		),
	}
}

func registerGetScheduledWorkouts(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in scheduledWorkoutsInput) (
		*mcp.CallToolResult, ScheduledWorkoutList, error,
	) {
		span, err := parseWindow(in.StartDate, in.EndDate, svc.limits)
		if err != nil {
			return nil, ScheduledWorkoutList{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, ScheduledWorkoutList{}, err
		}

		entries, err := svc.calendar.ScheduledWorkouts(ctx, session, span)
		if err != nil {
			return nil, ScheduledWorkoutList{}, fail(err)
		}

		curated, truncated := newScheduledWorkoutEntries(entries)
		return nil, ScheduledWorkoutList{
			ScheduledWorkouts: curated,
			Count:             len(curated),
			StartDate:         span.Start().String(),
			EndDate:           span.End().String(),
			Truncated:         truncated,
		}, nil
	}
	return mcpserver.AddTool(registry, getScheduledWorkoutsContract().Registration(), handler)
}

// newScheduledWorkoutEntries curates the entries and reports whether the bound cut
// the listing.
func newScheduledWorkoutEntries(entries []api.ScheduledWorkout) ([]ScheduledWorkoutEntry, bool) {
	truncated := false
	if len(entries) > DefaultMaxScheduledWorkouts {
		entries = entries[:DefaultMaxScheduledWorkouts]
		truncated = true
	}

	out := make([]ScheduledWorkoutEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, newScheduledWorkoutEntry(entry))
	}
	return out, truncated
}

func newScheduledWorkoutEntry(entry api.ScheduledWorkout) ScheduledWorkoutEntry {
	return ScheduledWorkoutEntry{
		Date:                     entry.ScheduleDate,
		ScheduledWorkoutID:       optionalInt64(entry.ScheduledWorkoutID),
		WorkoutID:                optionalInt64(entry.WorkoutID),
		WorkoutUUID:              entry.WorkoutUUID,
		TrainingPlanID:           optionalInt64(entry.TrainingPlanID),
		FBTAdaptivePlanID:        optionalInt64(entry.FBTAdaptivePlanID),
		TPType:                   entry.TPType,
		TrainingPlan:             entry.TPPlanName,
		Name:                     entry.WorkoutName,
		Sport:                    entry.WorkoutType,
		WorkoutType:              entry.WorkoutPhrase,
		Completed:                entry.Completed(),
		IsRestDay:                isFlagged(entry.IsRestDay),
		IsRaceDay:                isFlagged(entry.Race),
		EstimatedDurationSeconds: optionalInt(entry.EstimatedDurationSec),
		EstimatedDistanceMeters:  optionalFloat(entry.EstimatedDistanceM),
		ActivityID:               optionalInt64(entry.AssociatedActivityID),
	}
}

// isFlagged renders an optional Garmin flag as a plain boolean. An absent flag is
// false, which is what upstream's own curation does.
func isFlagged(flag *bool) bool { return flag != nil && *flag }

// A TrainingPlanEntry is one active plan.
type TrainingPlanEntry struct {
	Name           *string `json:"name,omitempty" jsonschema:"the plan name"`
	TrainingPlanID *int64  `json:"training_plan_id,omitempty" jsonschema:"the plan identifier"`
	Classification *string `json:"classification,omitempty" jsonschema:"Garmin's plan family, for example ADAPTIVE"`
	TrainingType   *string `json:"training_type,omitempty" jsonschema:"the plan's training type"`
}

// A TrainingPlanWindow is what the Garmin Coach read reports.
type TrainingPlanWindow struct {
	Date      string                  `json:"date" jsonschema:"the reference day that was read"`
	Plans     []TrainingPlanEntry     `json:"plans" jsonschema:"the active plans Garmin reported"`
	Workouts  []ScheduledWorkoutEntry `json:"workouts" jsonschema:"the plans' scheduled entries, rest days included"`
	Count     int                     `json:"count" jsonschema:"how many entries this result carries"`
	Truncated bool                    `json:"truncated" jsonschema:"whether the listing was cut at this server's bound"`
}

// LogValue reports the window size, never a plan or an entry.
func (w TrainingPlanWindow) LogValue() slog.Value {
	return shape("trainingPlanWindow",
		slog.Int("plans", len(w.Plans)),
		slog.Int("workouts", len(w.Workouts)),
		slog.Bool("truncated", w.Truncated),
	)
}

// trainingPlanInput is the Garmin Coach argument set.
type trainingPlanInput struct {
	CalendarDate string `json:"calendar_date" jsonschema:"the reference day, YYYY-MM-DD"`
}

func getTrainingPlanWorkoutsContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetTrainingPlanWorkouts,
			Title: "Get the training plan workouts",
			Description: "read the active Garmin Coach or training-plan workouts around one " +
				"date. Garmin generates that window itself and it is typically the current " +
				"week, so a later date can return nothing while a plan is still active. The " +
				"count includes rest days, and adaptive plans identify a workout by UUID " +
				"rather than by numeric id",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(calendarDateProperty(argNameCalendarDate,
			"the reference day whose plan window to read, in YYYY-MM-DD form")),
	}
}

func registerGetTrainingPlanWorkouts(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in trainingPlanInput) (
		*mcp.CallToolResult, TrainingPlanWindow, error,
	) {
		date, err := parseCalendarDate(argNameCalendarDate, in.CalendarDate)
		if err != nil {
			return nil, TrainingPlanWindow{}, err
		}
		session, err := svc.session(ctx)
		if err != nil {
			return nil, TrainingPlanWindow{}, err
		}

		plans, err := svc.calendar.TrainingPlanWorkouts(ctx, session, date)
		if err != nil {
			return nil, TrainingPlanWindow{}, fail(err)
		}
		return nil, newTrainingPlanWindow(date.String(), plans), nil
	}
	return mcpserver.AddTool(registry, getTrainingPlanWorkoutsContract().Registration(), handler)
}

// newTrainingPlanWindow flattens the plans and their entries into one bounded result.
func newTrainingPlanWindow(date string, plans []api.TrainingPlanSchedule) TrainingPlanWindow {
	summaries := make([]TrainingPlanEntry, 0, len(plans))
	var entries []api.ScheduledWorkout
	for _, plan := range plans {
		summaries = append(summaries, TrainingPlanEntry{
			Name:           plan.PlanName,
			TrainingPlanID: optionalInt64(plan.TrainingPlanID),
			Classification: plan.Classification,
			TrainingType:   plan.TrainingType(),
		})
		entries = append(entries, plan.Workouts.Items()...)
	}

	workouts, truncated := newScheduledWorkoutEntries(entries)
	return TrainingPlanWindow{
		Date:      date,
		Plans:     summaries,
		Workouts:  workouts,
		Count:     len(workouts),
		Truncated: truncated,
	}
}
