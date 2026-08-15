//go:build garminlive

package live

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// The workout the lifecycle builds. The figures are arbitrary and bounded, and they
// describe nothing about the account.
const (
	workoutRunSeconds  = 600
	workoutWarmupMin   = 5
	workoutCooldownMin = 5
)

// Result and document field names, spelled once so a rename shows up in one place.
const (
	keyName          = "name"
	keySportType     = "sport_type"
	keyWorkoutName   = "workoutName"
	keyWorkoutData   = "workout_data"
	keyScheduledID   = "scheduled_workout_id"
	keyScheduledList = "scheduled_workouts"
	keyRunSeconds    = "run_seconds"
	keyWarmupMin     = "warmup_min"
	keyCooldownMin   = "cooldown_min"
)

// runWorkoutArgs is the run builder's whole argument set for one name.
func runWorkoutArgs(name string) map[string]any {
	return map[string]any{
		keyName:        name,
		keyRunSeconds:  workoutRunSeconds,
		keyWarmupMin:   workoutWarmupMin,
		keyCooldownMin: workoutCooldownMin,
	}
}

// TestLiveWorkoutLifecycle drives one workout from creation to removal.
//
// The whole path is exercised against the real service: a builder composes and
// uploads a document, the library reads it back, the calendar takes it, an in-place
// update replaces its content, the calendar entry survives that update, and both the
// entry and the template are removed again.
//
// The update is the check that matters most here. update_workout is a ported
// upstream proposal whose stated purpose is that a workout keeps its identifier so
// existing schedules stay valid, and that claim is only testable where a real
// calendar exists: the entry is created before the update and re-read after it.
//
// Every object is one this suite created. Nothing already on the calendar informs a
// decision, and nothing but this suite's own entry is removed.
func TestLiveWorkoutLifecycle(t *testing.T) {
	w := liveWriteEnv(t)

	name := suiteName("workout")
	created := w.call(t, tools.ToolCreateRunWorkout, runWorkoutArgs(name))
	id := identifier(t, created, tools.ToolCreateRunWorkout, argWorkoutID)
	w.keepClean(t, kindWorkout, id)

	if !w.owned.owns(kindWorkout, id) {
		t.Fatal("the write guard did not learn the created workout from Garmin's own response, " +
			"so every later mutation of it would be refused")
	}

	sport := w.assertWorkoutReadsBack(t, id, name)
	scheduled, date := w.scheduleCreatedWorkout(t, id)
	renamed := w.updateCreatedWorkout(t, id, sport)
	w.assertScheduleSurvivedTheUpdate(t, id, scheduled, date)

	w.deleteViaTool(t, tools.ToolUnscheduleWorkout, keyScheduledID, kindSchedule, scheduled)
	w.assertNotScheduled(t, id, date)

	w.deleteViaTool(t, tools.ToolDeleteWorkout, argWorkoutID, kindWorkout, id)
	w.assertWorkoutIsGone(t, id, renamed)
}

// assertWorkoutReadsBack compares the created workout with what was sent and returns
// the sport key Garmin stored, so the update can prove it kept it.
func (w *writeEnv) assertWorkoutReadsBack(t *testing.T, id int64, name string) string {
	t.Helper()

	detail := w.call(t, tools.ToolGetWorkoutByID, map[string]any{argWorkoutID: id})
	if got := identifier(t, detail, tools.ToolGetWorkoutByID, argWorkoutID); got != id {
		t.Fatalf("%s answered for a different workout than the one created",
			tools.ToolGetWorkoutByID)
	}
	assertSuiteValue(t, tools.ToolGetWorkoutByID, keyName, name, detail)

	if segments, present := detail["segments"]; !present || segments == nil {
		t.Errorf("%s returned no segments, so the uploaded structure was not stored",
			tools.ToolGetWorkoutByID)
	}
	sport, _ := detail[keySportType].(string)
	if sport == "" {
		t.Errorf("%s returned no sport type for a workout the run builder created",
			tools.ToolGetWorkoutByID)
	}
	return sport
}

// scheduleCreatedWorkout puts the created workout on a far-future date and admits
// the resulting calendar entry to the ledger.
func (w *writeEnv) scheduleCreatedWorkout(t *testing.T, id int64) (int64, string) {
	t.Helper()

	date := time.Now().UTC().AddDate(0, 0, scheduleOffsetDays).Format(time.DateOnly)
	result := w.call(t, tools.ToolScheduleWorkout, map[string]any{
		argWorkoutID: id, argCalendar: date,
	})
	if got, _ := result[argCalendar].(string); got != date {
		t.Fatalf("%s reported a different date than the one requested", tools.ToolScheduleWorkout)
	}

	return w.adoptScheduledEntry(t, tools.ToolScheduleWorkout, id, date), date
}

// updateCreatedWorkout replaces the workout in place and returns the new name.
//
// The replacement document is the one Garmin itself stores, read back and renamed,
// which is the shape a caller actually has: update_workout replaces the whole
// workout, so a partial document would be a different test.
func (w *writeEnv) updateCreatedWorkout(t *testing.T, id int64, sport string) string {
	t.Helper()

	document := w.workoutDocument(t, id)
	renamed := suiteName("workout-updated")
	document[keyWorkoutName] = renamed

	updated := w.call(t, tools.ToolUpdateWorkout, map[string]any{
		argWorkoutID: id, keyWorkoutData: document,
	})
	if got := identifier(t, updated, tools.ToolUpdateWorkout, argWorkoutID); got != id {
		t.Fatalf("%s saved the workout under a different identifier, so every schedule "+
			"pointing at it would be orphaned", tools.ToolUpdateWorkout)
	}

	detail := w.call(t, tools.ToolGetWorkoutByID, map[string]any{argWorkoutID: id})
	assertSuiteValue(t, tools.ToolGetWorkoutByID, keyName, renamed, detail)
	if got, _ := detail[keySportType].(string); got != sport {
		t.Errorf("%s changed the sport type of a workout whose update did not touch it",
			tools.ToolUpdateWorkout)
	}
	return renamed
}

// assertScheduleSurvivedTheUpdate is the live check of the ported proposal's stated
// purpose: the calendar entry created before the update still points at the same
// workout after it.
func (w *writeEnv) assertScheduleSurvivedTheUpdate(
	t *testing.T, id, scheduled int64, date string,
) {
	t.Helper()

	found, present := w.awaitScheduledEntry(t, id, date)
	if !present {
		t.Fatalf("%s left the calendar entry of the workout it updated dangling",
			tools.ToolUpdateWorkout)
	}
	if found != scheduled {
		t.Errorf("%s replaced the calendar entry rather than keeping it, so an existing "+
			"schedule did not stay valid", tools.ToolUpdateWorkout)
	}
}

// assertNotScheduled proves the calendar entry is gone.
func (w *writeEnv) assertNotScheduled(t *testing.T, id int64, date string) {
	t.Helper()

	if !w.awaitAbsentEntry(t, id, date) {
		t.Errorf("%s reported success and the calendar still holds the entry",
			tools.ToolUnscheduleWorkout)
	}
}

// awaitAbsentEntry re-reads the calendar until the created workout's entry is gone,
// and reports whether it ever went.
//
// The gateway lags in both directions, so the absence check waits exactly as the
// presence check does. It is bounded: an entry still there when the reads run out
// fails, so nothing is hidden by waiting.
func (w *writeEnv) awaitAbsentEntry(t *testing.T, id int64, date string) bool {
	t.Helper()

	for range maxCalendarReads {
		if _, present := w.scheduledEntry(t, id, date); !present {
			return true
		}
	}
	return false
}

// assertWorkoutIsGone proves the template is gone.
//
// A deleted workout must not be readable and must not be listed. Both are checked,
// because an endpoint that answers a stale read and a library that still lists the
// template are two different failures.
func (w *writeEnv) assertWorkoutIsGone(t *testing.T, id int64, name string) {
	t.Helper()

	result := w.rawCall(t, tools.ToolGetWorkoutByID, map[string]any{argWorkoutID: id})
	if !result.IsError {
		t.Errorf("%s still answers for a workout %s removed",
			tools.ToolGetWorkoutByID, tools.ToolDeleteWorkout)
	}

	w.assertWorkoutNotListed(t, tools.ToolDeleteWorkout, name)
}

// assertWorkoutNotListed proves the library no longer carries a workout by name.
//
// The name is one this suite generated, so comparing on it discloses nothing, and it
// is the only handle a batch removal leaves: the identifiers are gone with it.
func (w *writeEnv) assertWorkoutNotListed(t *testing.T, remover, name string) {
	t.Helper()

	library := w.call(t, tools.ToolGetWorkouts, nil)
	entries, _ := library["workouts"].([]any)
	for _, entry := range entries {
		object, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if listed, _ := object[keyName].(string); listed == name {
			t.Errorf("%s still lists a workout %s removed", tools.ToolGetWorkouts, remover)
		}
	}
}

// maxCalendarReads is how many times a presence check re-reads the calendar.
//
// Garmin serves the workout calendar from a GraphQL gateway that does not always
// answer with an entry the REST tier accepted a moment earlier. Waiting for it is not
// a weaker assertion: the entry must still appear, and a wait that elapses fails.
const maxCalendarReads = 4

// awaitScheduledEntry re-reads the calendar until the created workout's entry is
// there, and reports whether it ever appeared.
func (w *writeEnv) awaitScheduledEntry(t *testing.T, id int64, date string) (int64, bool) {
	t.Helper()

	for range maxCalendarReads {
		if scheduled, found := w.scheduledEntry(t, id, date); found {
			return scheduled, true
		}
	}
	return 0, false
}

// scheduledEntry reports the calendar-entry identifier of one workout on one date,
// and whether the calendar holds one.
//
// Only entries that name the created workout are examined. Every other entry on the
// day is skipped rather than inspected, so nothing pre-existing informs a decision.
func (w *writeEnv) scheduledEntry(t *testing.T, id int64, date string) (int64, bool) {
	t.Helper()

	calendar := w.call(t, tools.ToolGetScheduledWorkouts, map[string]any{
		argStartDate: date, argEndDate: date,
	})
	entries, _ := calendar[keyScheduledList].([]any)

	for _, entry := range entries {
		object, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		workout, ok := object[argWorkoutID].(float64)
		if !ok || int64(workout) != id {
			continue
		}
		scheduled, ok := object[keyScheduledID].(float64)
		if !ok {
			t.Fatalf("%s returned an entry for the created workout with no entry identifier",
				tools.ToolGetScheduledWorkouts)
		}
		return int64(scheduled), true
	}
	return 0, false
}

// adoptScheduledEntry waits for the calendar entry of one created workout, admits it
// to the ledger and returns its identifier.
//
// The read-back and the adoption are one step on purpose. No caller supplies an entry
// identifier: the identifier is the one Garmin returned on the entry that names this
// workout, so the association between the two is read rather than assumed. An entry
// naming any other template is skipped by scheduledEntry and never reaches the ledger,
// because it would be the maintainer's.
func (w *writeEnv) adoptScheduledEntry(t *testing.T, tool string, id int64, date string) int64 {
	t.Helper()

	scheduled, found := w.awaitScheduledEntry(t, id, date)
	if !found {
		t.Fatalf("%s reported success and the calendar shows no entry for the created workout",
			tool)
	}
	if !w.owned.ownScheduled(id, scheduled) {
		t.Fatalf("%s produced a calendar entry that could not be tied to a workout this suite "+
			"created, so it is not this suite's to remove", tool)
	}
	w.keepClean(t, kindSchedule, scheduled)
	return scheduled
}

// workoutDocument reads the complete workout document Garmin stores.
//
// The tool surface returns a reduced view, so the domain client is used instead: an
// in-place update replaces the whole workout and needs the whole document.
func (w *writeEnv) workoutDocument(t *testing.T, id int64) map[string]any {
	t.Helper()

	target, err := client.NewID(id)
	if err != nil {
		t.Fatalf("the created workout identifier is unusable: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*requestTimeout)
	defer cancel()

	workout, err := w.workouts.Get(ctx, w.session, target)
	if err != nil {
		t.Fatalf("reading the created workout through the workout client: %v", err)
	}

	var document map[string]any
	if err := json.Unmarshal(workout.Payload().Bytes(), &document); err != nil {
		t.Fatalf("the stored workout is not a JSON object this suite can replace: %v", err)
	}
	if len(document) == 0 {
		t.Fatal("the stored workout document is empty, so an update would replace it with nothing")
	}
	return document
}

// assertSuiteValue compares one string field of a result with the value that was
// sent.
//
// The only value it can render is one this suite generated, and it renders it only
// as the "sent" side. The account's own data is never printed: a mismatch names the
// field and stops there.
func assertSuiteValue(t *testing.T, tool, field, sent string, result map[string]any) {
	t.Helper()

	got, ok := result[field].(string)
	if !ok {
		t.Fatalf("%s returned no %s", tool, field)
	}
	if got != sent {
		t.Errorf("%s read back a %s that differs from the one written (sent %q)",
			tool, field, sent)
	}
}

// assertSuiteValueContains is assertSuiteValue for a field Garmin may decorate.
func assertSuiteValueContains(t *testing.T, tool, field, sent string, result map[string]any) {
	t.Helper()

	got, ok := result[field].(string)
	if !ok {
		t.Fatalf("%s returned no %s", tool, field)
	}
	if !strings.Contains(got, sent) {
		t.Errorf("%s read back a %s that does not carry the one written (sent %q)",
			tool, field, sent)
	}
}
