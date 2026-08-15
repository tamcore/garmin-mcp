package tools_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// Synthetic calendar fixtures. Every identifier, name and date is invented.
const (
	weekStart = "2026-01-05"
	weekEnd   = "2026-01-11"

	argWeek = "week"

	calendarEntriesBody = `{"data":{"workoutScheduleSummariesScalar":[` +
		`{"scheduleDate":"2026-01-05","scheduledWorkoutId":9001,"workoutId":9101,` +
		`"workoutName":"Easy Run","workoutType":"running","estimatedDurationInSecs":1800,` +
		`"associatedActivityId":7001},` +
		`{"scheduleDate":"2026-01-07","scheduledWorkoutId":9002,"workoutId":9102,` +
		`"workoutName":"Rest","isRestDay":true,"tpPlanName":"Synthetic 5K Plan"}]}}`

	emptyCalendarBody = `{"data":{"workoutScheduleSummariesScalar":[]}}`

	coachPlanBody = `{"data":{"trainingPlanScalar":{"trainingPlanWorkoutScheduleDTOS":[` +
		`{"planName":"Synthetic 5K Plan","trainingPlanId":5001,` +
		`"trainingPlanClassification":"ADAPTIVE",` +
		`"trainingPlanDetailsDTO":{"trainingType":"RUNNING"},` +
		`"workoutScheduleSummaries":[` +
		`{"scheduleDate":"2026-01-05","workoutUuid":"11111111-2222-3333-4444-555555555555",` +
		`"workoutName":"Base Run","workoutPhrase":"LONG_WORKOUT"}]}]}}}`
)

// calendarScript serves the gateway with the same body for every query.
func calendarScript(body string) testkit.Script {
	return testkit.NewScript().With(client.PathGraphQL,
		repeat(testkit.JSON(http.StatusOK, body), 8)...)
}

func TestGetScheduledWorkoutsReturnsTheCuratedCalendar(t *testing.T) {
	t.Parallel()

	h := newHarness(t, calendarScript(calendarEntriesBody))
	out := h.call(t, tools.ToolGetScheduledWorkouts, map[string]any{
		argStartDate: weekStart, argEndDate: weekEnd,
	})

	entries, ok := out["scheduled_workouts"].([]any)
	if !ok || len(entries) != 2 {
		t.Fatalf("scheduled_workouts = %v, want two entries", out["scheduled_workouts"])
	}
	if got := out["count"]; got != float64(2) {
		t.Errorf("count = %v, want 2", got)
	}

	first, _ := entries[0].(map[string]any)
	if got := first["scheduled_workout_id"]; got != float64(9001) {
		t.Errorf("scheduled_workout_id = %v, want 9001", got)
	}
	if got := first["workout_id"]; got != float64(9101) {
		t.Errorf("workout_id = %v, want 9101", got)
	}
	if got := first["completed"]; got != true {
		t.Errorf("completed = %v, want true", got)
	}
	if got := first["date"]; got != weekStart {
		t.Errorf("date = %v, want %q", got, weekStart)
	}

	second, _ := entries[1].(map[string]any)
	if got := second["is_rest_day"]; got != true {
		t.Errorf("is_rest_day = %v, want true", got)
	}
	if got := second["completed"]; got != false {
		t.Errorf("completed = %v, want false", got)
	}
}

func TestGetScheduledWorkoutsRefusesAnUnusableWindow(t *testing.T) {
	t.Parallel()

	h := newHarness(t, calendarScript(calendarEntriesBody))
	cases := map[string]map[string]any{
		"reversed":     {argStartDate: weekEnd, argEndDate: weekStart},
		"not a date":   {argStartDate: "not-a-date", argEndDate: weekEnd},
		"too long":     {argStartDate: strings.Repeat("9", 40), argEndDate: weekEnd},
		"decade apart": {argStartDate: "2010-01-01", argEndDate: weekEnd},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if text := h.callError(t, tools.ToolGetScheduledWorkouts, args); text == "" {
				t.Error("the refusal carried no advice")
			}
		})
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("dispatched %d requests, want 0", got)
	}
}

func TestGetTrainingPlanWorkoutsReturnsThePlanWindow(t *testing.T) {
	t.Parallel()

	h := newHarness(t, calendarScript(coachPlanBody))
	out := h.call(t, tools.ToolGetTrainingPlanWorkouts, map[string]any{
		argCalendarDate: weekStart,
	})

	plans, ok := out["plans"].([]any)
	if !ok || len(plans) != 1 {
		t.Fatalf("plans = %v, want one plan", out["plans"])
	}
	plan, _ := plans[0].(map[string]any)
	if got := plan["name"]; got != "Synthetic 5K Plan" {
		t.Errorf("plan name = %v", got)
	}
	if got := plan["training_type"]; got != "RUNNING" {
		t.Errorf("training_type = %v", got)
	}

	workouts, ok := out["workouts"].([]any)
	if !ok || len(workouts) != 1 {
		t.Fatalf("workouts = %v, want one workout", out["workouts"])
	}
	workout, _ := workouts[0].(map[string]any)
	if got := workout["workout_uuid"]; got != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("workout_uuid = %v", got)
	}
	if got := workout["workout_type"]; got != "LONG_WORKOUT" {
		t.Errorf("workout_type = %v", got)
	}
	if got := out["count"]; got != float64(1) {
		t.Errorf("count = %v, want 1", got)
	}
}

func TestGetTrainingPlanWorkoutsRefusesAMalformedDate(t *testing.T) {
	t.Parallel()

	h := newHarness(t, calendarScript(coachPlanBody))
	if text := h.callError(t, tools.ToolGetTrainingPlanWorkouts,
		map[string]any{argCalendarDate: "2026-13-45"}); text == "" {
		t.Error("the refusal carried no advice")
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("dispatched %d requests, want 0", got)
	}
}

// scheduleWeekScript serves the calendar to every pre-check and accepts every
// schedule POST.
func scheduleWeekScript(calendar string) testkit.Script {
	return testkit.NewScript().
		With(client.PathGraphQL, repeat(testkit.JSON(http.StatusOK, calendar), 8)...).
		With(client.PathWorkoutSchedule+"/9101", testkit.JSON(http.StatusOK, `{}`)).
		With(client.PathWorkoutSchedule+"/9102", testkit.JSON(http.StatusOK, `{}`))
}

func TestScheduleWeekSchedulesEachEntry(t *testing.T) {
	t.Parallel()

	h := newWriteHarness(t, scheduleWeekScript(emptyCalendarBody), enabledWrites())
	out := h.call(t, tools.ToolScheduleWeek, map[string]any{
		argWeek: []any{
			map[string]any{argCalendarDate: weekStart, argWorkoutID: 9101},
			map[string]any{argCalendarDate: weekEnd, argWorkoutID: 9102},
		},
	})

	scheduled, ok := out["scheduled"].([]any)
	if !ok || len(scheduled) != 2 {
		t.Fatalf("scheduled = %v, want two outcomes", out["scheduled"])
	}
	for index, entry := range scheduled {
		outcome, _ := entry.(map[string]any)
		if got := outcome["status"]; got != "scheduled" {
			t.Errorf("outcome %d status = %v, want scheduled", index, got)
		}
	}
	if got := out["applied"]; got != float64(2) {
		t.Errorf("applied = %v, want 2", got)
	}
}

// TestScheduleWeekSkipsAnEntryThatIsAlreadyOnTheCalendar covers the pre-check that
// exists to avoid a duplicate calendar entry.
func TestScheduleWeekSkipsAnEntryThatIsAlreadyOnTheCalendar(t *testing.T) {
	t.Parallel()

	h := newWriteHarness(t, scheduleWeekScript(calendarEntriesBody), enabledWrites())
	out := h.call(t, tools.ToolScheduleWeek, map[string]any{
		argWeek: []any{map[string]any{argCalendarDate: weekStart, argWorkoutID: 9101}},
	})

	scheduled, _ := out["scheduled"].([]any)
	if len(scheduled) != 1 {
		t.Fatalf("scheduled = %v, want one outcome", out["scheduled"])
	}
	outcome, _ := scheduled[0].(map[string]any)
	if got := outcome["status"]; got != "already_scheduled" {
		t.Errorf("status = %v, want already_scheduled", got)
	}
	for _, request := range h.fake.Requests() {
		if strings.HasPrefix(request.Path, client.PathWorkoutSchedule) {
			t.Errorf("a schedule POST reached %s despite the pre-check", request.Path)
		}
	}
}

// TestScheduleWeekFailsOpenWhenThePreCheckFails is the honest half of the
// classification: a pre-check that cannot answer must not block a legitimate
// schedule, which is exactly why the tool stays non-idempotent.
func TestScheduleWeekFailsOpenWhenThePreCheckFails(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(client.PathGraphQL,
			repeat(testkit.JSON(http.StatusInternalServerError, `{"error":"synthetic"}`), 8)...).
		With(client.PathWorkoutSchedule+"/9101", testkit.JSON(http.StatusOK, `{}`))
	h := newWriteHarness(t, script, enabledWrites())

	out := h.call(t, tools.ToolScheduleWeek, map[string]any{
		argWeek: []any{map[string]any{argCalendarDate: weekStart, argWorkoutID: 9101}},
	})
	scheduled, _ := out["scheduled"].([]any)
	if len(scheduled) != 1 {
		t.Fatalf("scheduled = %v, want one outcome", out["scheduled"])
	}
	outcome, _ := scheduled[0].(map[string]any)
	if got := outcome["status"]; got != "scheduled" {
		t.Errorf("status = %v, want scheduled: the pre-check must fail open", got)
	}
	if got := outcome["duplicate_check"]; got != "failed" {
		t.Errorf("duplicate_check = %v, want failed", got)
	}
}

func TestScheduleWeekRefusesAMalformedBatch(t *testing.T) {
	t.Parallel()

	h := newWriteHarness(t, scheduleWeekScript(emptyCalendarBody), enabledWrites())
	cases := map[string]any{
		"empty":            []any{},
		"bad date":         []any{map[string]any{argCalendarDate: "nope", argWorkoutID: 9101}},
		"bad workout":      []any{map[string]any{argCalendarDate: weekStart, argWorkoutID: -1}},
		"missing workout":  []any{map[string]any{argCalendarDate: weekStart}},
		"missing calendar": []any{map[string]any{argWorkoutID: 9101}},
	}

	for name, week := range cases {
		t.Run(name, func(t *testing.T) {
			if text := h.callError(t, tools.ToolScheduleWeek,
				map[string]any{argWeek: week}); text == "" {
				t.Error("the refusal carried no advice")
			}
		})
	}
	for _, request := range h.fake.Requests() {
		if strings.HasPrefix(request.Path, client.PathWorkoutSchedule) {
			t.Errorf("a malformed batch reached %s", request.Path)
		}
	}
}

// TestScheduleWeekIsRefusedWithoutTheWriteScope keeps the tier gate honest.
func TestScheduleWeekIsRefusedWithoutTheWriteScope(t *testing.T) {
	t.Parallel()

	h := newHarness(t, scheduleWeekScript(emptyCalendarBody))
	if text := h.callError(t, tools.ToolScheduleWeek, map[string]any{
		argWeek: []any{map[string]any{argCalendarDate: weekStart, argWorkoutID: 9101}},
	}); text == "" {
		t.Error("the refusal carried no advice")
	}
}
