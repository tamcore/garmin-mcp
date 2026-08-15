package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Calendar is the workout calendar, which Garmin serves from its GraphQL tier
// rather than from the REST tier the rest of this package reads.
//
// Source: get_scheduled_workouts, _get_garmin_coach_workouts and
// _is_already_scheduled in garmin_mcp at commit 3610be6, all three of which call
// python-garminconnect 0.3.10's query_garmin_graphql. See the package comment on
// internal/garmin/client/graphql.go for the request shape and where it came from.
//
// Nothing here writes. The calendar write is Workouts.Schedule, and it stays on the
// REST tier, exactly as upstream leaves it.
type Calendar struct {
	req requester
}

// NewCalendar returns a calendar client over the request layer.
func NewCalendar(rc *client.Client) (*Calendar, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &Calendar{req: req}, nil
}

// ScheduledWorkout is one entry of the workout calendar.
//
// It is health material: it says what training a person planned and whether they
// completed it, so it is never logged. Every field is optional, an unknown field
// never fails the response, and the numeric fields use the union decoder because
// Garmin sends some of them as strings.
//
// The field names are Garmin's own, taken from the response
// _curate_scheduled_workout reads upstream.
type ScheduledWorkout struct {
	// ScheduleDate is the calendar day, YYYY-MM-DD.
	ScheduleDate *string `json:"scheduleDate"`
	// ScheduledWorkoutID is the calendar-entry id, which is what unschedules the
	// entry. It is not the workout's own id.
	ScheduledWorkoutID client.Number `json:"scheduledWorkoutId"`
	// WorkoutID is the workout template's numeric id, when the plan family uses one.
	WorkoutID client.Number `json:"workoutId"`
	// WorkoutUUID is the identifier adaptive Garmin Coach plans use instead.
	WorkoutUUID *string `json:"workoutUuid"`
	// TrainingPlanID and FBTAdaptivePlanID name the plan the entry belongs to.
	TrainingPlanID    client.Number `json:"trainingPlanId"`
	FBTAdaptivePlanID client.Number `json:"fbtAdaptivePlanId"`
	// TPType and TPPlanName describe that plan.
	TPType     *string `json:"tpType"`
	TPPlanName *string `json:"tpPlanName"`
	// WorkoutName is the entry's name and WorkoutType its sport.
	WorkoutName *string `json:"workoutName"`
	WorkoutType *string `json:"workoutType"`
	// WorkoutPhrase is Garmin's intent label, for example ANAEROBIC_SPEED.
	WorkoutPhrase *string `json:"workoutPhrase"`
	// IsRestDay and Race are the two day flags Garmin sets.
	IsRestDay *bool `json:"isRestDay"`
	Race      *bool `json:"race"`
	// EstimatedDurationSec and EstimatedDistanceM are the planned targets.
	EstimatedDurationSec client.Number `json:"estimatedDurationInSecs"`
	EstimatedDistanceM   client.Number `json:"estimatedDistanceInMeters"`
	// AssociatedActivityID is the recorded activity, present once the workout was
	// done.
	AssociatedActivityID client.Number `json:"associatedActivityId"`
}

// Completed reports whether the entry was carried out.
//
// Garmin sends no completion flag: upstream infers it from the presence of an
// associated activity, and so does this.
func (s ScheduledWorkout) Completed() bool { return s.AssociatedActivityID.IsSet() }

// trainingPlanDetails is the nested detail object of one plan.
type trainingPlanDetails struct {
	TrainingType *string `json:"trainingType"`
}

// TrainingPlanSchedule is one active plan and the workouts it scheduled in the
// window Garmin generated. It is health material for the same reason
// ScheduledWorkout is.
type TrainingPlanSchedule struct {
	// PlanName is the plan's display name.
	PlanName *string `json:"planName"`
	// TrainingPlanID is the plan identifier.
	TrainingPlanID client.Number `json:"trainingPlanId"`
	// Classification is Garmin's plan family, for example ADAPTIVE.
	Classification *string `json:"trainingPlanClassification"`
	// Workouts are the scheduled entries of this plan. Garmin sends a single object
	// instead of an array for some plan families, which the union decoder absorbs.
	Workouts client.List[ScheduledWorkout] `json:"workoutScheduleSummaries"`

	// details is unexported: the nested object is Garmin's and it drifts, so it is
	// reached through TrainingType rather than published as a field.
	details *trainingPlanDetails
}

// planFields is the decoding twin of TrainingPlanSchedule. It exists so
// UnmarshalJSON can read the nested detail object without recursing into itself.
type planFields struct {
	PlanName       *string                       `json:"planName"`
	TrainingPlanID client.Number                 `json:"trainingPlanId"`
	Classification *string                       `json:"trainingPlanClassification"`
	Workouts       client.List[ScheduledWorkout] `json:"workoutScheduleSummaries"`
	Details        *trainingPlanDetails          `json:"trainingPlanDetailsDTO"`
}

// UnmarshalJSON decodes the plan, keeping the nested detail object off the exported
// surface. Decoding stays tolerant: an unknown field is ignored, as everywhere else.
func (t *TrainingPlanSchedule) UnmarshalJSON(data []byte) error {
	var fields planFields
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&fields); err != nil {
		return err
	}
	*t = TrainingPlanSchedule{
		PlanName:       fields.PlanName,
		TrainingPlanID: fields.TrainingPlanID,
		Classification: fields.Classification,
		Workouts:       fields.Workouts,
		details:        fields.Details,
	}
	return nil
}

// TrainingType reports the plan's training type, and nil when Garmin sent none.
func (t TrainingPlanSchedule) TrainingType() *string {
	if t.details == nil {
		return nil
	}
	return t.details.TrainingType
}

// trainingPlanWindow is the object trainingPlanScalar returns.
type trainingPlanWindow struct {
	Plans []TrainingPlanSchedule `json:"trainingPlanWorkoutScheduleDTOS"`
}

// ScheduledWorkouts reads the workout calendar between two dates, inclusive.
//
// The window is validated against the configured date-range bound before anything is
// dispatched, so a caller cannot ask for a decade of calendar in one call.
func (c *Calendar) ScheduledWorkouts(
	ctx context.Context, session client.Session, span client.DateRange,
) ([]ScheduledWorkout, error) {
	req := scheduleSummariesQuery(span)
	if err := c.req.limits().ValidateDateRange(span); err != nil {
		return nil, invalidQuery(req, err)
	}

	var entries []ScheduledWorkout
	if err := c.req.query(ctx, session, req, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// TrainingPlanWorkouts reads the active Garmin Coach or training-plan window around
// one date.
//
// Garmin generates that window itself and it is typically the current week, so a
// future date can legitimately return no plans while a plan is active. That is an
// empty result, not a failure.
func (c *Calendar) TrainingPlanWorkouts(
	ctx context.Context, session client.Session, date client.Date,
) ([]TrainingPlanSchedule, error) {
	req := trainingPlanQuery(date)
	if date.IsZero() {
		return nil, invalidQuery(req, fmt.Errorf(
			"%w: a calendar date is required for this endpoint", client.ErrValidation))
	}

	var window trainingPlanWindow
	if err := c.req.query(ctx, session, req, &window); err != nil {
		return nil, err
	}
	return window.Plans, nil
}

// IsScheduled reports whether workout id already has a calendar entry on date.
//
// It is the duplicate pre-check upstream's scheduling tools run, narrowed to that one
// day, and it matches on both the workout and the date: matching on either alone
// would skip a legitimate second entry.
//
// It reports its own failures rather than swallowing them. Upstream's helper ends in
// a bare "except Exception: return False", so a failed check reads as "not scheduled"
// and the duplicate POST goes out anyway. That fail-open behavior is a caller's
// decision to make, not this package's, and it is why scheduling stays classified as
// non-idempotent wherever the pre-check is used.
func (c *Calendar) IsScheduled(
	ctx context.Context, session client.Session, id client.ID, date client.Date,
) (bool, error) {
	if id.IsZero() {
		return false, invalidQuery(scheduleSummariesQuery(client.DateRange{}), fmt.Errorf(
			"%w: a positive identifier is required for this endpoint", client.ErrValidation))
	}
	span, err := client.NewDateRange(date, date)
	if err != nil {
		return false, invalidQuery(scheduleSummariesQuery(client.DateRange{}), err)
	}

	entries, err := c.ScheduledWorkouts(ctx, session, span)
	if err != nil {
		return false, err
	}
	return matchesSchedule(entries, id, date), nil
}

// matchesSchedule reports whether any entry names both this workout and this date.
func matchesSchedule(entries []ScheduledWorkout, id client.ID, date client.Date) bool {
	for _, entry := range entries {
		scheduled, ok := entry.WorkoutID.Int64()
		if !ok || scheduled != id.Int64() {
			continue
		}
		if entry.ScheduleDate != nil && *entry.ScheduleDate == date.String() {
			return true
		}
	}
	return false
}

// scheduleSummariesQuery builds the calendar query. An unset range renders no
// arguments, which the request layer refuses before dispatch.
func scheduleSummariesQuery(span client.DateRange) client.GraphQLRequest {
	req := client.GraphQLRequest{
		Op:       client.OpGetScheduledWorkouts,
		Endpoint: client.EndpointGraphQL,
		Field:    client.GraphQLFieldWorkoutScheduleSummaries,
	}
	if span.IsZero() {
		return req
	}
	req.Arguments = []client.GraphQLArgument{
		{Name: client.GraphQLArgStartDate, Value: span.Start().String()},
		{Name: client.GraphQLArgEndDate, Value: span.End().String()},
	}
	return req
}

// trainingPlanQuery builds the Garmin Coach query. The lang and firstDayOfWeek
// arguments are the constants upstream hard-codes; neither names a user.
func trainingPlanQuery(date client.Date) client.GraphQLRequest {
	req := client.GraphQLRequest{
		Op:       client.OpGetTrainingPlanWorkouts,
		Endpoint: client.EndpointGraphQL,
		Field:    client.GraphQLFieldTrainingPlan,
	}
	if date.IsZero() {
		return req
	}
	req.Arguments = []client.GraphQLArgument{
		{Name: client.GraphQLArgCalendarDate, Value: date.String()},
		{Name: client.GraphQLArgLang, Value: client.GraphQLLangDefault},
		{Name: client.GraphQLArgFirstDayOfWeek, Value: client.GraphQLFirstDayOfWeekDefault},
	}
	return req
}
