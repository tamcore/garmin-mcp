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

const workoutDocumentBody = `{"workoutName":"Easy Run","workoutSegments":[],"unknownBlock":7}`

func newWorkouts(t *testing.T, h harness) *api.Workouts {
	t.Helper()

	workouts, err := api.NewWorkouts(h.rc)
	if err != nil {
		t.Fatalf("NewWorkouts() = %v", err)
	}
	return workouts
}

func mustWorkoutDocument(t *testing.T) api.WorkoutDocument {
	t.Helper()

	document, err := api.ParseWorkoutDocument([]byte(workoutDocumentBody))
	if err != nil {
		t.Fatalf("ParseWorkoutDocument() = %v", err)
	}
	return document
}

func workoutItemPath() string { return client.PathWorkoutPrefix + "/18446744" }

// TestWorkoutDocumentValidation covers the strict request model for a body this
// package deliberately carries verbatim.
func TestWorkoutDocumentValidation(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		body    string
		wantErr bool
		object  bool
	}{
		"object":         {body: `{"workoutName":"x"}`, object: true},
		"array":          {body: `[{"workoutName":"x"}]`},
		"empty":          {body: "   ", wantErr: true},
		"not json":       {body: `{"workoutName":`, wantErr: true},
		"scalar":         {body: `"a workout"`, wantErr: true},
		"leading spaces": {body: "  {\"a\":1}  ", object: true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			document, err := api.ParseWorkoutDocument([]byte(tc.body))
			if tc.wantErr {
				if !errors.Is(err, client.ErrValidation) {
					t.Fatalf("ParseWorkoutDocument() = %v, want ErrValidation", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseWorkoutDocument() = %v", err)
			}
			if document.IsObject() != tc.object {
				t.Errorf("IsObject() = %v, want %v", document.IsObject(), tc.object)
			}
		})
	}
}

// TestWorkoutReadsDecodeTolerantly covers the list and the single read, unknown
// fields included.
func TestWorkoutReadsDecodeTolerantly(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(client.PathWorkouts, testkit.JSON(http.StatusOK,
			`[{"workoutId":"18446744","workoutName":"Easy Run","surpriseField":true},`+
				`{"workoutId":18446745}]`)).
		With(workoutItemPath(), testkit.JSON(http.StatusOK,
			`{"workoutId":18446744,"workoutName":"Easy Run","workoutSegments":[{"segmentOrder":1}]}`))
	h := newHarness(t, script, client.Limits{})
	workouts := newWorkouts(t, h)

	page, err := client.NewPage(0, 20)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	listed, err := workouts.List(t.Context(), h.session, page)
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("%d workouts, want 2", len(listed))
	}
	if id, ok := listed[0].WorkoutID.Int64(); !ok || id != 18446744 {
		t.Errorf("WorkoutID = %d/%v, want the numeric string decoded", id, ok)
	}

	workout, err := workouts.Get(t.Context(), h.session, mustID(t))
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	if len(workout.Segments) == 0 {
		t.Error("Get() dropped the segment structure")
	}
	if workout.Payload().Len() == 0 {
		t.Error("Get() retained no raw payload")
	}
}

// TestWorkoutListRefusesAPageOverTheBound proves the configured page bound is
// applied before anything is dispatched.
func TestWorkoutListRefusesAPageOverTheBound(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{MaxPageSize: 5})
	page, err := client.NewPage(0, 50)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}

	if _, err := newWorkouts(t, h).List(t.Context(), h.session, page); !errors.Is(
		err, client.ErrValidation) {
		t.Fatalf("List() = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestScheduleAndUnscheduleTargetTheirOwnPaths pins the two calendar writes.
func TestScheduleAndUnscheduleTargetTheirOwnPaths(t *testing.T) {
	t.Parallel()

	schedulePath := client.PathWorkoutSchedule + "/18446744"
	script := testkit.NewScript().With(schedulePath, testkit.JSON(http.StatusOK, `{"id":5150}`))
	h := newHarness(t, script, client.Limits{})
	workouts := newWorkouts(t, h)

	if _, err := workouts.Schedule(t.Context(), h.session, mustID(t),
		mustDate(t, testCalendarDate)); err != nil {
		t.Fatalf("Schedule() = %v", err)
	}
	if _, err := workouts.Unschedule(t.Context(), h.session, mustID(t)); err != nil {
		t.Fatalf("Unschedule() = %v", err)
	}

	requests := h.server.Requests()
	if requests[0].Method != http.MethodPost {
		t.Errorf("schedule method = %q, want POST", requests[0].Method)
	}
	if !strings.Contains(string(requests[0].Body), testCalendarDate) {
		t.Errorf("schedule body = %s, want the calendar date", requests[0].Body)
	}
	if requests[1].Method != http.MethodDelete {
		t.Errorf("unschedule method = %q, want DELETE", requests[1].Method)
	}
}

// TestWorkoutDownloadStreamsIntoTheCallersSink proves the FIT download reaches
// the caller's writer and never a path this package chose.
func TestWorkoutDownloadStreamsIntoTheCallersSink(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathWorkoutFITPrefix+"/18446744",
		testkit.Behavior{
			Status: http.StatusOK, ContentType: "application/octet-stream", Body: "FIT-BYTES",
		})
	h := newHarness(t, script, client.Limits{})

	var sink strings.Builder
	result, err := newWorkouts(t, h).Download(t.Context(), h.session, mustID(t), &sink)
	if err != nil {
		t.Fatalf("Download() = %v", err)
	}
	if sink.String() != "FIT-BYTES" {
		t.Errorf("sink = %q, want the streamed body", sink.String())
	}
	if result.Bytes != int64(len("FIT-BYTES")) {
		t.Errorf("Bytes = %d, want %d", result.Bytes, len("FIT-BYTES"))
	}
}
