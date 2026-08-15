package tools_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// Synthetic workout fixtures. Every value here is invented.
const (
	testWorkoutID   = "550001"
	workoutListBody = `[{"workoutId":550001,"workoutName":"Easy run","updateDate":"2026-01-30",` +
		`"sportType":{"sportTypeKey":"running"},"estimatedDurationInSecs":2400},` +
		`{"workoutId":550002,"workoutName":"Intervals",` +
		`"sportType":{"sportTypeKey":"running"}}]`
	workoutDetailBody = `{"workoutId":550001,"workoutName":"Easy run",` +
		`"sportType":{"sportTypeKey":"running"},"workoutSegments":[{"segmentOrder":1}]}`
	savedWorkoutBody = `{"workoutId":550009,"workoutName":"Saved by Garmin"}`

	// updatedWorkoutBody is what an in-place update answers with. It names the
	// workout the request addressed, which an upload answer does not: an update that
	// came back naming another workout is a failure the api layer refuses, so a
	// fixture reusing the upload body would be scripting drift rather than success.
	updatedWorkoutBody = `{"workoutId":` + testWorkoutID + `,"workoutName":"Saved by Garmin"}`

	workoutFITBody = "FITBYTES"
)

func workoutReadScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, repeat(okJSON(profileBody), 4)...).
		With(client.PathWorkouts, okJSON(workoutListBody)).
		With(workoutPath(testWorkoutID), okJSON(workoutDetailBody)).
		With(client.PathWorkoutFITPrefix+"/"+testWorkoutID, testkit.Behavior{
			Status: http.StatusOK, ContentType: "application/octet-stream",
			Body: workoutFITBody,
		})
}

func TestGetWorkoutsReturnsBoundedSummaries(t *testing.T) {
	h := newHarness(t, workoutReadScript())

	out := h.call(t, tools.ToolGetWorkouts, nil)

	if got := out["count"]; got != float64(2) {
		t.Errorf("count = %v, want 2", got)
	}
	if truncated, _ := out["truncated"].(bool); truncated {
		t.Error("truncated = true, but the library is shorter than the bound")
	}
	entries, _ := out["workouts"].([]any)
	first, _ := entries[0].(map[string]any)
	if got := first["sport_type"]; got != typeRunning {
		t.Errorf("sport_type = %v, want running", got)
	}
}

func TestGetWorkoutByIDReturnsTheSegmentsUnreshaped(t *testing.T) {
	h := newHarness(t, workoutReadScript())

	out := h.call(t, tools.ToolGetWorkoutByID, map[string]any{argWorkoutID: testWorkoutID})

	if got := out["workout_id"]; got != float64(550001) {
		t.Errorf("workout_id = %v, want 550001", got)
	}
	if _, ok := out["segments"]; !ok {
		t.Error("the result carries no segments")
	}
}

func TestGetWorkoutByIDRefusesTheUUIDFormItCannotServe(t *testing.T) {
	h := newHarness(t, workoutReadScript())

	h.callError(t, tools.ToolGetWorkoutByID, map[string]any{
		argWorkoutID: "3fa85f64-5717-4562-b3fc-2c963f66afa6",
	})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("a UUID still reached Garmin: %v", h.recordedMethods())
	}
}

func TestDownloadWorkoutReturnsAnEmbeddedResourceAndNamesNoPath(t *testing.T) {
	h := newHarness(t, workoutReadScript())

	result := h.rawCall(t, tools.ToolDownloadWorkout, map[string]any{argWorkoutID: 550001})
	if result.IsError {
		t.Fatalf("download_workout failed: %s", resultText(result))
	}
	if len(result.Content) == 0 {
		t.Fatal("the result carries no content block")
	}

	out := structured(t, tools.ToolDownloadWorkout, result)
	if got := out["bytes"]; got != float64(len(workoutFITBody)) {
		t.Errorf("bytes = %v, want the transferred size", got)
	}
	uri, _ := out["uri"].(string)
	if !strings.HasPrefix(uri, "garmin://workout/") {
		t.Errorf("uri = %q, want a garmin:// resource URI and never a filesystem path", uri)
	}
}

func TestDownloadWorkoutRefusesAFileOverTheBound(t *testing.T) {
	script := workoutReadScript().With(client.PathWorkoutFITPrefix+"/"+testWorkoutID,
		testkit.Behavior{Status: http.StatusOK, Body: strings.Repeat("x", 4096)})
	h := newHarnessWith(t, script, tools.Bounds{MaxDownloadBytes: 16}, client.Limits{})

	message := h.callError(t, tools.ToolDownloadWorkout, map[string]any{argWorkoutID: 550001})

	if !strings.Contains(message, "bound") {
		t.Errorf("the refusal %q does not mention the bound", message)
	}
}

func workoutWriteScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, repeat(okJSON(profileBody), 6)...).
		With(client.PathWorkoutPrefix, repeat(okJSON(savedWorkoutBody), 4)...).
		With(workoutPath(testWorkoutID), repeat(okJSON(updatedWorkoutBody), 2)...).
		With(client.PathWorkoutSchedule+"/"+testWorkoutID, repeat(okJSON(`{}`), 3)...)
}

func TestUploadWorkoutReportsWhatGarminSavedRatherThanWhatWasSent(t *testing.T) {
	h := newWriteHarness(t, workoutWriteScript(), enabledWrites())

	out := h.call(t, tools.ToolUploadWorkout, map[string]any{
		argWorkoutData: map[string]any{workoutNameKey: "Sent name"},
	})

	if got := out["workout_id"]; got != float64(550009) {
		t.Errorf("workout_id = %v, want the identifier Garmin assigned", got)
	}
	if got := out["name"]; got != savedWorkoutName {
		t.Errorf("name = %v, want the name Garmin saved", got)
	}
}

func TestUpdateWorkoutKeepsTheIdentifierSoSchedulesStayValid(t *testing.T) {
	h := newWriteHarness(t, workoutWriteScript(), enabledWrites())

	h.call(t, tools.ToolUpdateWorkout, map[string]any{
		argWorkoutID:   550001,
		argWorkoutData: map[string]any{workoutNameKey: "Renamed", "workoutId": 999999},
	})

	body := h.bodyFor(t, http.MethodPut, workoutPath(testWorkoutID))
	if got := body["workoutId"]; got != float64(550001) {
		t.Errorf("the update sent workoutId = %v, want the identifier in the path", got)
	}
}

func TestUploadWorkoutsReportsEveryItemSeparately(t *testing.T) {
	h := newWriteHarness(t, workoutWriteScript(), enabledWrites())

	out := h.call(t, tools.ToolUploadWorkouts, map[string]any{
		"workouts": []any{
			map[string]any{workoutNameKey: "One"},
			map[string]any{workoutNameKey: "Two"},
		},
	})

	if got := out["requested"]; got != float64(2) {
		t.Errorf("requested = %v, want 2", got)
	}
	if saved, _ := out["saved"].([]any); len(saved) != 2 {
		t.Errorf("saved %d workouts, want 2", len(saved))
	}
}

func TestUploadWorkoutsRefusesAnEmptyBatch(t *testing.T) {
	h := newWriteHarness(t, workoutWriteScript(), enabledWrites())

	h.callError(t, tools.ToolUploadWorkouts, map[string]any{"workouts": []any{}})

	if len(h.fake.Requests()) != 0 {
		t.Errorf("an empty batch still reached Garmin: %v", h.recordedMethods())
	}
}

func TestScheduleWorkoutWritesTheCalendarEntry(t *testing.T) {
	h := newWriteHarness(t, workoutWriteScript(), enabledWrites())

	out := h.call(t, tools.ToolScheduleWorkout, map[string]any{
		argWorkoutID:    550001,
		argCalendarDate: testCalendarDate,
	})

	if got := out["calendar_date"]; got != testCalendarDate {
		t.Errorf("calendar_date = %v, want %v", got, testCalendarDate)
	}
}

func TestScheduleWorkoutsValidatesTheWholeBatchBeforeWritingAny(t *testing.T) {
	h := newWriteHarness(t, workoutWriteScript(), enabledWrites())

	h.callError(t, tools.ToolScheduleWorkouts, map[string]any{
		"schedules": []any{
			map[string]any{argWorkoutID: 550001, argCalendarDate: testCalendarDate},
			map[string]any{argWorkoutID: 550001, argCalendarDate: "not-a-date"},
		},
	})

	for _, recorded := range h.recordedMethods() {
		if strings.HasPrefix(recorded, http.MethodPost) {
			t.Errorf("a malformed batch still wrote a calendar entry: %v", h.recordedMethods())
		}
	}
}

// TestScheduleWorkoutsWritesEveryValidatedEntry covers the accepted path of the
// batch: each entry is dispatched on its own and reported on its own, which is
// what lets a caller tell a partial success from a total one.
func TestScheduleWorkoutsWritesEveryValidatedEntry(t *testing.T) {
	h := newWriteHarness(t, workoutWriteScript(), enabledWrites())

	out := h.call(t, tools.ToolScheduleWorkouts, map[string]any{
		"schedules": []any{
			map[string]any{argWorkoutID: 550001, argCalendarDate: testCalendarDate},
			map[string]any{argWorkoutID: 550001, argCalendarDate: "2026-02-01"},
		},
	})

	if got := out["requested"]; got != float64(2) {
		t.Errorf("requested = %v, want 2", got)
	}
	if got := out["applied"]; got != float64(2) {
		t.Errorf("applied = %v, want 2", got)
	}
	if outcomes, _ := out["outcomes"].([]any); len(outcomes) != 2 {
		t.Errorf("reported %d outcomes, want one per entry", len(outcomes))
	}
}

func workoutDeleteScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, repeat(okJSON(profileBody), 4)...).
		With(workoutPath(testWorkoutID), repeat(testkit.JSON(http.StatusNoContent, ""), 2)...).
		With(workoutPath("550002"), testkit.JSON(http.StatusNotFound, `{}`)).
		With(client.PathWorkoutSchedule+"/"+testWorkoutID, testkit.JSON(http.StatusNoContent, ""))
}

func TestDeleteWorkoutsReportsAPartialFailureItemByItem(t *testing.T) {
	h := newWriteHarness(t, workoutDeleteScript(), enabledWrites())

	out := h.call(t, tools.ToolDeleteWorkouts, map[string]any{
		"workout_ids": []any{550001, 550002},
	})

	if got := out["requested"]; got != float64(2) {
		t.Errorf("requested = %v, want 2", got)
	}
	if got := out["applied"]; got != float64(1) {
		t.Errorf("applied = %v, want 1: the second identifier is unknown to Garmin", got)
	}
}

// TestDeleteWorkoutRemovesTheTemplateItself separates the two removals: deleting
// a workout takes the library template every calendar entry points at, which is
// not what unscheduling does.
func TestDeleteWorkoutRemovesTheTemplateItself(t *testing.T) {
	h := newWriteHarness(t, workoutDeleteScript(), enabledWrites())

	out := h.call(t, tools.ToolDeleteWorkout, map[string]any{argWorkoutID: 550001})

	if deleted, _ := out["deleted"].(bool); !deleted {
		t.Errorf("deleted = %v, want true", out["deleted"])
	}
	if got := out["id"]; got != float64(550001) {
		t.Errorf("id = %v, want the workout that was named", got)
	}
	if !slicesContain(h.recordedMethods(), http.MethodDelete+" "+workoutPath(testWorkoutID)) {
		t.Errorf("the template was not deleted: %v", h.recordedMethods())
	}
}

// TestUnscheduleWorkoutsRemovesEveryCalendarEntry covers the batch form, which
// loops the single removal and reports each entry separately.
func TestUnscheduleWorkoutsRemovesEveryCalendarEntry(t *testing.T) {
	h := newWriteHarness(t, calendarBatchScript(), enabledWrites())

	out := h.call(t, tools.ToolUnscheduleWorkouts, map[string]any{
		"scheduled_workout_ids": []any{550001, 550002},
	})

	if got := out["requested"]; got != float64(2) {
		t.Errorf("requested = %v, want 2", got)
	}
	if got := out["applied"]; got != float64(2) {
		t.Errorf("applied = %v, want 2", got)
	}
}

// calendarBatchScript answers two calendar removals.
func calendarBatchScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, repeat(okJSON(profileBody), 2)...).
		With(client.PathWorkoutSchedule+"/"+testWorkoutID,
			testkit.JSON(http.StatusNoContent, "")).
		With(client.PathWorkoutSchedule+"/550002", testkit.JSON(http.StatusNoContent, ""))
}

// slicesContain reports whether values holds want.
func slicesContain(values []string, want string) bool {
	return slices.Contains(values, want)
}

func TestUnscheduleWorkoutRemovesTheCalendarEntryOnly(t *testing.T) {
	h := newWriteHarness(t, workoutDeleteScript(), enabledWrites())

	out := h.call(t, tools.ToolUnscheduleWorkout, map[string]any{
		"scheduled_workout_id": 550001,
	})

	if deleted, _ := out["deleted"].(bool); !deleted {
		t.Errorf("deleted = %v, want true", out["deleted"])
	}
}
