//go:build garminlive

package live

import (
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/tools"
)

// This file drives the batch write tools: the multi-document upload, the two batch
// schedules, the week schedule and the two batch removals. They are kept apart from
// the single-item write surface because their whole contract is the per-item one —
// each requested item is applied, reported and read back on its own — and that is
// what every helper below exists to prove.

// batchWorkouts is how many workouts the batch checks create. Two is the smallest
// number that can show per-item reporting rather than a single outcome.
const batchWorkouts = 2

// Result and argument keys the batch tools use.
const (
	keySaved            = "saved"
	keyOutcomes         = "outcomes"
	keyApplied          = "applied"
	keyRequested        = "requested"
	keyFailures         = "failures"
	keyAlreadyScheduled = "already_scheduled"
	keyDuplicateCheck   = "duplicate_check"
	keyWeek             = "week"
	keySchedules        = "schedules"
	keyScheduledOutcome = "scheduled"
	keyWorkouts         = "workouts"
	keyGearUUID         = "gear_uuid"
	keyGear             = "gear"
	keyFormat           = "format"
	keyBytes            = "bytes"
	keyWorkoutID        = "workoutId"
)

// checkedDuplicate is the value schedule_week reports when its pre-check answered.
const checkedDuplicate = "checked"

// TestLiveWorkoutBatchToolsApplyEachItemSeparately drives the four batch tools and
// the week schedule.
//
// A batch tool's contract is that one item is applied and reported on its own, which
// only a real service can confirm. Every workout and every calendar entry here is one
// this suite created.
func TestLiveWorkoutBatchToolsApplyEachItemSeparately(t *testing.T) {
	w := liveWriteEnv(t)

	template := w.batchDocument(t)
	names := make([]string, 0, batchWorkouts)
	documents := make([]map[string]any, 0, batchWorkouts)
	for range batchWorkouts {
		name := suiteName("batch")
		names = append(names, name)
		documents = append(documents, merged(template, keyWorkoutName, name))
	}

	uploaded := w.call(t, tools.ToolUploadWorkouts, map[string]any{keyWorkouts: documents})
	ids := w.savedWorkoutIDs(t, uploaded, len(documents))

	dates := w.scheduleBatch(t, ids)
	entries := w.adoptScheduledBatch(t, ids, dates)
	w.assertWeekScheduleSkipsWhatIsAlreadyThere(t, ids, dates)

	w.unscheduleBatch(t, ids, entries, dates)
	w.deleteWorkoutBatch(t, ids)

	for _, name := range names {
		w.assertWorkoutNotListed(t, tools.ToolDeleteWorkouts, name)
	}
}

// batchDocument produces one Garmin workout document for the batch upload.
//
// It is Garmin's own stored document for a workout the run builder created, with the
// identifier stripped so an upload creates rather than replaces. This file therefore
// authors no Garmin workout schema of its own: the shape comes from the service.
func (w *writeEnv) batchDocument(t *testing.T) map[string]any {
	t.Helper()

	created := w.call(t, tools.ToolCreateRunWorkout, runWorkoutArgs(suiteName("template")))
	id := identifier(t, created, tools.ToolCreateRunWorkout, argWorkoutID)
	w.keepClean(t, kindWorkout, id)

	document := w.workoutDocument(t, id)
	delete(document, keyWorkoutID)
	w.deleteViaTool(t, tools.ToolDeleteWorkout, argWorkoutID, kindWorkout, id)
	return document
}

// savedWorkoutIDs reads the identifiers out of a batch upload and puts each one
// under cleanup.
func (w *writeEnv) savedWorkoutIDs(t *testing.T, result map[string]any, want int) []int64 {
	t.Helper()

	if failures, _ := result[keyFailures].([]any); len(failures) != 0 {
		t.Fatalf("%s refused %d of %d documents", tools.ToolUploadWorkouts, len(failures), want)
	}
	saved, _ := result[keySaved].([]any)
	if len(saved) != want {
		t.Fatalf("%s saved %d of %d documents", tools.ToolUploadWorkouts, len(saved), want)
	}

	ids := make([]int64, 0, want)
	for index, entry := range saved {
		object, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("%s returned an item at position %d that is not an object",
				tools.ToolUploadWorkouts, index)
		}
		id := identifier(t, object, tools.ToolUploadWorkouts, argWorkoutID)
		w.keepClean(t, kindWorkout, id)
		ids = append(ids, id)
	}
	return ids
}

// scheduleBatch schedules every created workout on a distinct far-future date.
func (w *writeEnv) scheduleBatch(t *testing.T, ids []int64) []string {
	t.Helper()

	dates := make([]string, 0, len(ids))
	schedules := make([]map[string]any, 0, len(ids))
	for index, id := range ids {
		date := time.Now().UTC().AddDate(0, 0, scheduleOffsetDays+index).Format(time.DateOnly)
		dates = append(dates, date)
		schedules = append(schedules, map[string]any{argWorkoutID: id, argCalendar: date})
	}

	result := w.call(t, tools.ToolScheduleWorkouts, map[string]any{keySchedules: schedules})
	if applied, _ := result[keyApplied].(float64); int(applied) != len(ids) {
		t.Fatalf("%s applied %v of %d entries", tools.ToolScheduleWorkouts,
			result[keyApplied], len(ids))
	}
	return dates
}

// adoptScheduledBatch finds each created workout's calendar entry and admits it.
func (w *writeEnv) adoptScheduledBatch(t *testing.T, ids []int64, dates []string) []int64 {
	t.Helper()

	entries := make([]int64, 0, len(ids))
	for index, id := range ids {
		entries = append(entries,
			w.adoptScheduledEntry(t, tools.ToolScheduleWorkouts, id, dates[index]))
	}
	return entries
}

// assertWeekScheduleSkipsWhatIsAlreadyThere drives schedule_week over entries that
// already exist.
//
// That pre-check is the one behaviour separating schedule_week from
// schedule_workouts, and it fails open, so it can only be judged against a real
// calendar. Driving it over the entries the previous step created proves it read one,
// and it deliberately creates nothing, so no duplicate is left behind.
func (w *writeEnv) assertWeekScheduleSkipsWhatIsAlreadyThere(
	t *testing.T, ids []int64, dates []string,
) {
	t.Helper()

	week := make([]map[string]any, 0, len(ids))
	for index, id := range ids {
		week = append(week, map[string]any{argWorkoutID: id, argCalendar: dates[index]})
	}

	result := w.call(t, tools.ToolScheduleWeek, map[string]any{keyWeek: week})
	if requested, _ := result[keyRequested].(float64); int(requested) != len(ids) {
		t.Fatalf("%s reported %v requested items and %d were sent",
			tools.ToolScheduleWeek, result[keyRequested], len(ids))
	}
	assertEveryOutcomeChecked(t, result, len(ids))

	if applied, _ := result[keyApplied].(float64); applied != 0 {
		t.Errorf("%s created %v duplicate calendar entries for entries that already existed",
			tools.ToolScheduleWeek, result[keyApplied])
	}
	if already, _ := result[keyAlreadyScheduled].(float64); int(already) != len(ids) {
		t.Errorf("%s recognised %v of %d entries as already scheduled, so its pre-check did "+
			"not see the calendar it wrote", tools.ToolScheduleWeek,
			result[keyAlreadyScheduled], len(ids))
	}
}

// assertEveryOutcomeChecked requires the duplicate pre-check to have answered for
// every item. The check fails open, so an item reporting that it failed would make
// the skip count meaningless rather than wrong.
//
// The count is asserted first, and that is the point of this helper: iterating an
// outcome list without requiring one entry per requested item lets an empty list —
// a tool that reported aggregate counters and no per-item result at all — pass while
// asserting nothing. A vacuous pass is a defect here, not a green light.
func assertEveryOutcomeChecked(t *testing.T, result map[string]any, want int) {
	t.Helper()

	outcomes, _ := result[keyScheduledOutcome].([]any)
	if len(outcomes) != want {
		t.Fatalf("%s reported %d outcomes for %d requested items, so no per-item result was "+
			"proven", tools.ToolScheduleWeek, len(outcomes), want)
	}
	for index, entry := range outcomes {
		object, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("%s returned an outcome at position %d that is not an object",
				tools.ToolScheduleWeek, index)
		}
		if check, _ := object[keyDuplicateCheck].(string); check != checkedDuplicate {
			t.Errorf("%s could not read the calendar for the item at position %d, so its "+
				"skip count proves nothing", tools.ToolScheduleWeek, index)
		}
	}
}

// unscheduleBatch removes every calendar entry in one call and proves each one is
// gone before it leaves the ledger.
//
// Releasing on the reported counter alone would be the one mistake this ledger exists
// to prevent: a silent no-op removal would leave real calendar entries on the account
// while the end-of-suite leak report declared the run clean. The entry is therefore
// read back, and only an entry Garmin no longer serves is released.
func (w *writeEnv) unscheduleBatch(t *testing.T, ids, entries []int64, dates []string) {
	t.Helper()

	asked := w.confirmations.Load()
	result := w.call(t, tools.ToolUnscheduleWorkouts,
		map[string]any{"scheduled_workout_ids": entries})
	assertBatchApplied(t, tools.ToolUnscheduleWorkouts, result, len(entries))

	if w.confirmations.Load() == asked {
		t.Errorf("%s ran without asking for confirmation", tools.ToolUnscheduleWorkouts)
	}
	for index, entry := range entries {
		if !w.awaitAbsentEntry(t, ids[index], dates[index]) {
			t.Errorf("%s reported success and the calendar still holds one of the entries",
				tools.ToolUnscheduleWorkouts)
			continue
		}
		w.owned.release(kindSchedule, entry)
	}
}

// deleteWorkoutBatch removes every created workout in one call and proves each one is
// gone before it leaves the ledger.
//
// As with the calendar entries, the reported counter is not the evidence: each
// template is re-read, and only one the library no longer answers for is released.
func (w *writeEnv) deleteWorkoutBatch(t *testing.T, ids []int64) {
	t.Helper()

	asked := w.confirmations.Load()
	result := w.call(t, tools.ToolDeleteWorkouts, map[string]any{"workout_ids": ids})
	assertBatchApplied(t, tools.ToolDeleteWorkouts, result, len(ids))

	if w.confirmations.Load() == asked {
		t.Errorf("%s ran without asking for confirmation", tools.ToolDeleteWorkouts)
	}
	for _, id := range ids {
		if !w.rawCall(t, tools.ToolGetWorkoutByID, map[string]any{argWorkoutID: id}).IsError {
			t.Errorf("%s reported success and %s still answers for one of the workouts",
				tools.ToolDeleteWorkouts, tools.ToolGetWorkoutByID)
			continue
		}
		w.owned.release(kindWorkout, id)
	}
}

// assertBatchApplied requires every item of a batch removal to have been applied and
// reported on its own.
func assertBatchApplied(t *testing.T, tool string, result map[string]any, want int) {
	t.Helper()

	if applied, _ := result[keyApplied].(float64); int(applied) != want {
		t.Fatalf("%s applied %v of %d items", tool, result[keyApplied], want)
	}
	outcomes, _ := result[keyOutcomes].([]any)
	if len(outcomes) != want {
		t.Errorf("%s reported %d outcomes for %d items, so items are not reported separately",
			tool, len(outcomes), want)
	}
}
