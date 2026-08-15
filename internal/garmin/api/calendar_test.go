package api_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// Synthetic calendar fixtures. Every identifier, name and date is invented.
const (
	calendarStart = "2026-01-05"
	calendarEnd   = "2026-01-11"

	scheduledCalendarBody = `{"data":{"workoutScheduleSummariesScalar":[` +
		`{"scheduleDate":"2026-01-05","scheduledWorkoutId":9001,"workoutId":42,` +
		`"workoutName":"Easy Run","workoutType":"running",` +
		`"estimatedDurationInSecs":1800,"associatedActivityId":7001,"unknownBlock":7},` +
		`{"scheduleDate":"2026-01-07","scheduledWorkoutId":9002,"workoutId":77,` +
		`"workoutName":"Rest","isRestDay":true}]}}`

	trainingPlanBody = `{"data":{"trainingPlanScalar":{"trainingPlanWorkoutScheduleDTOS":[` +
		`{"planName":"Synthetic 5K Plan","trainingPlanId":5001,` +
		`"trainingPlanClassification":"ADAPTIVE",` +
		`"trainingPlanDetailsDTO":{"trainingType":"RUNNING"},` +
		`"workoutScheduleSummaries":[` +
		`{"scheduleDate":"2026-01-05","workoutUuid":"11111111-2222-3333-4444-555555555555",` +
		`"workoutName":"Base Run","workoutPhrase":"LONG_WORKOUT"}]}]}}}`
)

func newCalendar(t *testing.T, h harness) *api.Calendar {
	t.Helper()

	calendar, err := api.NewCalendar(h.rc)
	if err != nil {
		t.Fatalf("NewCalendar() = %v", err)
	}
	return calendar
}

// graphQLScript serves body for the gateway path.
func graphQLScript(body string) testkit.Script {
	return testkit.NewScript().With(client.PathGraphQL, testkit.JSON(http.StatusOK, body))
}

func mustRange(t *testing.T, start, end string) client.DateRange {
	t.Helper()

	span, err := client.NewDateRange(mustDate(t, start), mustDate(t, end))
	if err != nil {
		t.Fatalf("NewDateRange() = %v", err)
	}
	return span
}

// TestScheduledWorkoutsReadsTheGraphQLCalendar covers the whole read: the document
// that goes out and the entries that come back.
func TestScheduledWorkoutsReadsTheGraphQLCalendar(t *testing.T) {
	t.Parallel()

	h := newHarness(t, graphQLScript(scheduledCalendarBody), client.Limits{})
	entries, err := newCalendar(t, h).ScheduledWorkouts(
		t.Context(), h.session, mustRange(t, calendarStart, calendarEnd))
	if err != nil {
		t.Fatalf("ScheduledWorkouts() = %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("read %d entries, want 2", len(entries))
	}
	if id, ok := entries[0].WorkoutID.Int64(); !ok || id != 42 {
		t.Errorf("first workout id = %d (set %t), want 42", id, ok)
	}
	if !entries[0].Completed() {
		t.Error("an entry with an associated activity must report completed")
	}
	if entries[1].Completed() {
		t.Error("an entry with no associated activity must not report completed")
	}

	requests := h.server.Requests()
	if len(requests) != 1 {
		t.Fatalf("dispatched %d requests, want 1", len(requests))
	}
	if requests[0].Method != http.MethodPost || requests[0].Path != client.PathGraphQL {
		t.Errorf("dispatched %s %s, want POST %s",
			requests[0].Method, requests[0].Path, client.PathGraphQL)
	}
	want := `workoutScheduleSummariesScalar(startDate:\"2026-01-05\", endDate:\"2026-01-11\")`
	if !strings.Contains(string(requests[0].Body), want) {
		t.Errorf("request body = %s, want it to carry %s", requests[0].Body, want)
	}
}

// TestScheduledWorkoutsRefusesAnUnusableWindow keeps a decade of calendar, and an
// unset range, off the wire.
func TestScheduledWorkoutsRefusesAnUnusableWindow(t *testing.T) {
	t.Parallel()

	cases := map[string]client.DateRange{
		"unset":    {},
		"too wide": mustRange(t, "2020-01-01", "2026-01-01"),
	}

	for name, span := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, graphQLScript(scheduledCalendarBody), client.Limits{})
			_, err := newCalendar(t, h).ScheduledWorkouts(t.Context(), h.session, span)
			if !errors.Is(err, client.ErrValidation) {
				t.Fatalf("ScheduledWorkouts() = %v, want ErrValidation", err)
			}
			if got := len(h.server.Requests()); got != 0 {
				t.Errorf("dispatched %d requests, want 0", got)
			}
		})
	}
}

// TestTrainingPlanWorkoutsReadsThePlanWindow covers the Garmin Coach read.
func TestTrainingPlanWorkoutsReadsThePlanWindow(t *testing.T) {
	t.Parallel()

	h := newHarness(t, graphQLScript(trainingPlanBody), client.Limits{})
	plans, err := newCalendar(t, h).TrainingPlanWorkouts(
		t.Context(), h.session, mustDate(t, calendarStart))
	if err != nil {
		t.Fatalf("TrainingPlanWorkouts() = %v", err)
	}

	if len(plans) != 1 {
		t.Fatalf("read %d plans, want 1", len(plans))
	}
	if plans[0].PlanName == nil || *plans[0].PlanName != "Synthetic 5K Plan" {
		t.Errorf("plan name = %v", plans[0].PlanName)
	}
	if got := plans[0].Workouts.Len(); got != 1 {
		t.Fatalf("plan carries %d workouts, want 1", got)
	}
	if got := plans[0].TrainingType(); got == nil || *got != "RUNNING" {
		t.Errorf("training type = %v", got)
	}

	requests := h.server.Requests()
	want := `trainingPlanScalar(calendarDate:\"2026-01-05\", lang:\"en-US\", ` +
		`firstDayOfWeek:\"monday\")`
	if len(requests) != 1 || !strings.Contains(string(requests[0].Body), want) {
		t.Errorf("request body = %s, want it to carry %s", requests[0].Body, want)
	}
}

// TestTrainingPlanWorkoutsRefusesAnUnsetDate keeps an unusable call off the wire.
func TestTrainingPlanWorkoutsRefusesAnUnsetDate(t *testing.T) {
	t.Parallel()

	h := newHarness(t, graphQLScript(trainingPlanBody), client.Limits{})
	_, err := newCalendar(t, h).TrainingPlanWorkouts(t.Context(), h.session, client.Date{})
	if !errors.Is(err, client.ErrValidation) {
		t.Fatalf("TrainingPlanWorkouts() = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("dispatched %d requests, want 0", got)
	}
}

// TestIsScheduledMatchesBothWorkoutAndDate is the duplicate pre-check. It must match
// on both fields: matching on the workout alone would silently skip a legitimate
// second date, and matching on the date alone would skip a different workout.
func TestIsScheduledMatchesBothWorkoutAndDate(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		id   int64
		date string
		want bool
	}{
		"same workout and date":    {id: 42, date: calendarStart, want: true},
		"same workout, other date": {id: 42, date: "2026-01-07"},
		"other workout, same date": {id: 77, date: calendarStart},
		"neither":                  {id: 1234, date: "2026-01-09"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, graphQLScript(scheduledCalendarBody), client.Limits{})
			id, err := client.NewID(tc.id)
			if err != nil {
				t.Fatalf("NewID() = %v", err)
			}

			got, err := newCalendar(t, h).IsScheduled(
				t.Context(), h.session, id, mustDate(t, tc.date))
			if err != nil {
				t.Fatalf("IsScheduled() = %v", err)
			}
			if got != tc.want {
				t.Errorf("IsScheduled() = %t, want %t", got, tc.want)
			}
		})
	}
}

// TestIsScheduledRefusesAnUnsetIdentifier keeps an unusable pre-check off the wire.
func TestIsScheduledRefusesAnUnsetIdentifier(t *testing.T) {
	t.Parallel()

	h := newHarness(t, graphQLScript(scheduledCalendarBody), client.Limits{})
	_, err := newCalendar(t, h).IsScheduled(
		t.Context(), h.session, client.ID{}, mustDate(t, calendarStart))
	if !errors.Is(err, client.ErrValidation) {
		t.Fatalf("IsScheduled() = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("dispatched %d requests, want 0", got)
	}
}

// TestCalendarReportsAReportedGraphQLError proves the errors array reaches the domain
// caller as a failure rather than as an empty calendar.
func TestCalendarReportsAReportedGraphQLError(t *testing.T) {
	t.Parallel()

	body := `{"data":{"workoutScheduleSummariesScalar":[]},` +
		`"errors":[{"message":"synthetic gateway refusal"}]}`
	h := newHarness(t, graphQLScript(body), client.Limits{})

	_, err := newCalendar(t, h).ScheduledWorkouts(
		t.Context(), h.session, mustRange(t, calendarStart, calendarEnd))
	if !errors.Is(err, client.ErrGraphQLErrors) {
		t.Fatalf("ScheduledWorkouts() = %v, want ErrGraphQLErrors", err)
	}
	if strings.Contains(err.Error(), "synthetic gateway refusal") {
		t.Errorf("the rendered failure quotes the upstream message: %q", err.Error())
	}
}

// TestCalendarRefusesAnUnusableSession keeps an unprincipled read off the wire.
func TestCalendarRefusesAnUnusableSession(t *testing.T) {
	t.Parallel()

	h := newHarness(t, graphQLScript(scheduledCalendarBody), client.Limits{})
	calendar := newCalendar(t, h)

	if _, err := calendar.ScheduledWorkouts(t.Context(), client.Session{},
		mustRange(t, calendarStart, calendarEnd)); !errors.Is(err, client.ErrMissingPrincipal) {
		t.Errorf("ScheduledWorkouts() = %v, want ErrMissingPrincipal", err)
	}
	if _, err := calendar.TrainingPlanWorkouts(t.Context(), client.Session{},
		mustDate(t, calendarStart)); !errors.Is(err, client.ErrMissingPrincipal) {
		t.Errorf("TrainingPlanWorkouts() = %v, want ErrMissingPrincipal", err)
	}
}

// TestNewCalendarNeedsARequestLayer covers the constructor guard.
func TestNewCalendarNeedsARequestLayer(t *testing.T) {
	t.Parallel()

	if _, err := api.NewCalendar(nil); !errors.Is(err, client.ErrNotConfigured) {
		t.Fatalf("NewCalendar(nil) = %v, want ErrNotConfigured", err)
	}
}
