package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// This file covers the workout writes: the in-place update and everything that decides
// whether the answer Garmin gave describes the workout the request addressed. They sit
// apart from the read and validation tests because that identity check is the whole
// subject, and because workoutsave.go is the file they exercise.

// TestUpdateKeepsTheWorkoutIdentityAndPrefersTheServerAnswer is the
// update_workout behavior: the PUT targets the existing workout, the body's
// workoutId is forced to match it so existing schedules stay valid, and the name
// and id reported back are the server's, not the caller's.
func TestUpdateKeepsTheWorkoutIdentityAndPrefersTheServerAnswer(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(workoutItemPath(), testkit.JSON(http.StatusOK,
		`{"workoutId":18446744,"workoutName":"Easy Run (normalized)"}`))
	h := newHarness(t, script, client.Limits{})

	document, err := api.ParseWorkoutDocument([]byte(`{"workoutName":"easy run","workoutId":1}`))
	if err != nil {
		t.Fatalf("ParseWorkoutDocument() = %v", err)
	}
	saved, err := newWorkouts(t, h).Update(t.Context(), h.session, mustID(t), document)
	if err != nil {
		t.Fatalf("Update() = %v", err)
	}

	request := h.server.Requests()[0]
	if request.Method != http.MethodPut {
		t.Errorf("method = %q, want PUT so the workout keeps its id", request.Method)
	}
	var body map[string]any
	if err := json.Unmarshal(request.Body, &body); err != nil {
		t.Fatalf("Unmarshal(body) = %v", err)
	}
	if body["workoutId"] != float64(18446744) {
		t.Errorf("body workoutId = %v, want the path identifier to win", body["workoutId"])
	}
	if body["workoutName"] != "easy run" {
		t.Errorf("body workoutName = %v, want the caller's document carried through",
			body["workoutName"])
	}

	id, err := saved.ID()
	if err != nil {
		t.Fatalf("ID() = %v", err)
	}
	if id.Int64() != 18446744 {
		t.Errorf("ID() = %d, want the server-returned identifier", id.Int64())
	}
	if name, ok := saved.Name(); !ok || name != "Easy Run (normalized)" {
		t.Errorf("Name() = %q/%v, want the server-returned name", name, ok)
	}
}

// TestUpdateReadsTheWorkoutBackWhenGarminAnswersWithNoContent covers what the real
// service actually does.
//
// Garmin answers an in-place workout update with 204 and an empty body, so there is
// no identifier and no name in the answer at all. Reporting that as a malformed
// payload made update_workout fail on every real update while the update itself had
// succeeded. The identifier and the name must still be the server's rather than the
// caller's, so they are read back rather than echoed. Confirmed against the live
// service on 2026-08-15.
func TestUpdateReadsTheWorkoutBackWhenGarminAnswersWithNoContent(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(workoutItemPath(),
		testkit.JSON(http.StatusNoContent, ""),
		testkit.JSON(http.StatusOK,
			`{"workoutId":18446744,"workoutName":"Easy Run (normalized)"}`))
	h := newHarness(t, script, client.Limits{})

	document, err := api.ParseWorkoutDocument([]byte(`{"workoutName":"easy run"}`))
	if err != nil {
		t.Fatalf("ParseWorkoutDocument() = %v", err)
	}
	saved, err := newWorkouts(t, h).Update(t.Context(), h.session, mustID(t), document)
	if err != nil {
		t.Fatalf("Update() = %v, want the empty answer to be handled rather than refused", err)
	}

	requests := h.server.Requests()
	if len(requests) != 2 {
		t.Fatalf("%d requests, want the update and the read-back", len(requests))
	}
	if requests[0].Method != http.MethodPut || requests[1].Method != http.MethodGet {
		t.Errorf("methods = %q then %q, want PUT then GET",
			requests[0].Method, requests[1].Method)
	}

	id, err := saved.ID()
	if err != nil {
		t.Fatalf("ID() = %v", err)
	}
	if id.Int64() != 18446744 {
		t.Errorf("ID() = %d, want the identifier the read-back reported", id.Int64())
	}
	if name, ok := saved.Name(); !ok || name != "Easy Run (normalized)" {
		t.Errorf("Name() = %q/%v, want the name Garmin stored", name, ok)
	}
}

// TestUpdateRefusesAnAnswerNamingADifferentWorkout is the identity check on the
// answer, and it covers both shapes the answer arrives in.
//
// The result of an update is what a caller schedules, deletes or updates next. An
// identifier that is not the one the request addressed therefore aims every one of
// those at a workout the caller never named — someone else's, if Garmin ever answers a
// write from a cache or drifts. Neither the direct answer nor the read-back that
// stands in for a 204 may be believed on that point.
func TestUpdateRefusesAnAnswerNamingADifferentWorkout(t *testing.T) {
	t.Parallel()

	const other = `{"workoutId":99999999,"workoutName":"someone else's workout"}`
	cases := map[string][]testkit.Behavior{
		"the update answers with another workout": {testkit.JSON(http.StatusOK, other)},
		"the read-back answers with another workout": {
			testkit.JSON(http.StatusNoContent, ""),
			testkit.JSON(http.StatusOK, other),
		},
	}
	for name, responses := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, testkit.NewScript().With(workoutItemPath(), responses...),
				client.Limits{})
			document, err := api.ParseWorkoutDocument([]byte(`{"workoutName":"easy run"}`))
			if err != nil {
				t.Fatalf("ParseWorkoutDocument() = %v", err)
			}

			saved, err := newWorkouts(t, h).Update(t.Context(), h.session, mustID(t), document)
			if !errors.Is(err, client.ErrMalformedPayload) {
				t.Fatalf("Update() = %v, want ErrMalformedPayload for a foreign identifier", err)
			}
			if id, idErr := saved.ID(); idErr == nil {
				t.Errorf("Update() reported workout %d as saved, want no result at all",
					id.Int64())
			}
		})
	}
}

// TestUpdateRefusesAnIdentifierOnlyAFloatWouldCallEqual is the same identity check
// against the two answers a float64 comparison cannot refuse.
//
// The identifier used to be compared through Number.Int64, which truncates the float64
// the payload was parsed into. That accepted an answer naming 18446744.9 for a request
// addressing 18446744 — a different object under any reading — and, above 2^53, made two
// identifiers one apart compare equal, so a drifted answer naming the neighbouring
// workout was reported as the workout the caller had updated. Both shapes are refused
// here, on both the direct answer and the read-back that stands in for a 204.
func TestUpdateRefusesAnIdentifierOnlyAFloatWouldCallEqual(t *testing.T) {
	t.Parallel()

	// 2^53 is the last integer a float64 holds alone; 2^53+1 shares its representation.
	// A request addressing the first and an answer naming the second are two different
	// workouts that a float comparison calls one.
	const atFloatLimit int64 = 9007199254740992
	const neighbour = `{"workoutId":9007199254740993,"workoutName":"the next workout"}`
	const fractional = `{"workoutId":18446744.9,"workoutName":"not this workout"}`

	cases := map[string]struct {
		id        int64
		path      string
		responses []testkit.Behavior
	}{
		"the answer names a fractional identifier": {
			id: testActivityID, path: workoutItemPath(),
			responses: []testkit.Behavior{testkit.JSON(http.StatusOK, fractional)},
		},
		"the read-back names a fractional identifier": {
			id: testActivityID, path: workoutItemPath(),
			responses: []testkit.Behavior{
				testkit.JSON(http.StatusNoContent, ""),
				testkit.JSON(http.StatusOK, fractional),
			},
		},
		"the answer names the neighbour of an identifier at 2^53": {
			id:   atFloatLimit,
			path: client.PathWorkoutPrefix + "/9007199254740992",
			responses: []testkit.Behavior{
				testkit.JSON(http.StatusOK, neighbour),
			},
		},
		"the read-back names the neighbour of an identifier at 2^53": {
			id:   atFloatLimit,
			path: client.PathWorkoutPrefix + "/9007199254740992",
			responses: []testkit.Behavior{
				testkit.JSON(http.StatusNoContent, ""),
				testkit.JSON(http.StatusOK, neighbour),
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, testkit.NewScript().With(tc.path, tc.responses...), client.Limits{})
			document, err := api.ParseWorkoutDocument([]byte(`{"workoutName":"easy run"}`))
			if err != nil {
				t.Fatalf("ParseWorkoutDocument() = %v", err)
			}
			target, err := client.NewID(tc.id)
			if err != nil {
				t.Fatalf("NewID(%d) = %v", tc.id, err)
			}

			saved, err := newWorkouts(t, h).Update(t.Context(), h.session, target, document)
			if !errors.Is(err, client.ErrMalformedPayload) {
				t.Fatalf("Update() = %v, want ErrMalformedPayload: the answer names a "+
					"different workout and only a float comparison would call it the same one",
					err)
			}
			if _, idErr := saved.ID(); idErr == nil {
				t.Error("Update() reported a saved workout, want no result at all")
			}
		})
	}
}

// TestUpdateRefusesAReadBackThatNamesNoWorkout covers the other half of the 204 path:
// an answer that names nothing cannot be reported as the workout that was updated
// either, because the identifier would then be the caller's own echoed back.
func TestUpdateRefusesAReadBackThatNamesNoWorkout(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript().With(workoutItemPath(),
		testkit.JSON(http.StatusNoContent, ""),
		testkit.JSON(http.StatusOK, `{"workoutName":"easy run"}`)), client.Limits{})
	document, err := api.ParseWorkoutDocument([]byte(`{"workoutName":"easy run"}`))
	if err != nil {
		t.Fatalf("ParseWorkoutDocument() = %v", err)
	}

	if _, err := newWorkouts(t, h).Update(
		t.Context(), h.session, mustID(t), document); !errors.Is(
		err, client.ErrMalformedPayload) {
		t.Fatalf("Update() = %v, want ErrMalformedPayload for an unnamed read-back", err)
	}
}

// TestUpdateRefusesAnArrayDocument keeps the upload validators in force for the
// in-place update, plus the one rule only the update has: a replacement must be a
// single complete object.
func TestUpdateRefusesAnArrayDocument(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	document, err := api.ParseWorkoutDocument([]byte(`[{"workoutName":"x"}]`))
	if err != nil {
		t.Fatalf("ParseWorkoutDocument() = %v", err)
	}

	if _, err := newWorkouts(t, h).Update(t.Context(), h.session, mustID(t), document); !errors.Is(
		err, client.ErrValidation) {
		t.Fatalf("Update() = %v, want ErrValidation", err)
	}
	if _, err := newWorkouts(t, h).Upload(t.Context(), h.session,
		api.WorkoutDocument{}); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("Upload() with no document = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}
