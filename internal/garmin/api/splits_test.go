package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const (
	testActivityID = int64(18446744)
	splitEntry     = `{"type":"INTERVAL_ACTIVE","distance":400.0,"duration":"95",` +
		`"startTimeGMT":"2026-01-31T06:12:00.0","maxElevation":null}`
	exerciseSetsBody = `{"exerciseSets":[{"setType":"ACTIVE","repetitionCount":"12","weight":20000.0,` +
		`"exercises":[{"category":"BENCH_PRESS","name":null,"probability":98}],` +
		`"startTime":"2026-01-31T10:00:00.0"}],"unknownBlock":1}`
)

func splitsPath() string {
	return client.PathActivityPrefix + "/18446744/" + client.SegmentTypedSplits
}

func exerciseSetsPath() string {
	return client.PathActivityPrefix + "/18446744/" + client.SegmentExerciseSets
}

func newActivityDetails(t *testing.T, h harness) *api.ActivityDetails {
	t.Helper()

	details, err := api.NewActivityDetails(h.rc)
	if err != nil {
		t.Fatalf("NewActivityDetails() = %v", err)
	}
	return details
}

// TestTypedSplitsDecodesEveryUpstreamShape is the union-decoder test: the same
// endpoint answers with a keyed object, a differently keyed object, a bare array and
// a single object depending on the activity type, and upstream tolerates all of them.
func TestTypedSplitsDecodesEveryUpstreamShape(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		body string
		want int
	}{
		"splits key":    {`{"splits":[` + splitEntry + `,` + splitEntry + `]}`, 2},
		"lapDTOs key":   {`{"lapDTOs":[` + splitEntry + `]}`, 1},
		"bare array":    {`[` + splitEntry + `]`, 1},
		"single object": {splitEntry, 1},
		"empty object":  {`{}`, 0},
		"null":          {`null`, 0},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(splitsPath(), testkit.JSON(http.StatusOK, tc.body))
			h := newHarness(t, script, client.Limits{})

			got, err := newActivityDetails(t, h).TypedSplits(t.Context(), h.session, mustID(t))
			if err != nil {
				t.Fatalf("TypedSplits() = %v", err)
			}
			splits := got.Splits()
			if len(splits) != tc.want {
				t.Fatalf("%d splits decoded, want %d", len(splits), tc.want)
			}
			if tc.want == 0 {
				return
			}
			if kind, ok := splits[0].Type.Value(); !ok || kind != "INTERVAL_ACTIVE" {
				t.Errorf("Type = %q/%v, want INTERVAL_ACTIVE", kind, ok)
			}
			if duration, ok := splits[0].Duration.Float64(); !ok || duration != 95 {
				t.Errorf("Duration = %v/%v, want 95 from the string form", duration, ok)
			}
			if splits[0].MaxElevation.IsSet() {
				t.Error("MaxElevation must report absent for an explicit null")
			}
		})
	}
}

func TestTypedSplitsRejectsAnUnusableActivityID(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	_, err := newActivityDetails(t, h).TypedSplits(t.Context(), h.session, client.ID{})
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("TypedSplits() with a zero id = %v, want a validation error", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestTypedSplitsReportsAMalformedShape(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(splitsPath(), testkit.JSON(http.StatusOK, `{"splits":"not-a-list"}`))
	h := newHarness(t, script, client.Limits{})

	_, err := newActivityDetails(t, h).TypedSplits(t.Context(), h.session, mustID(t))
	if !errors.Is(err, client.ErrMalformedPayload) {
		t.Errorf("TypedSplits() = %v, want ErrMalformedPayload", err)
	}
}

func TestExerciseSetsDecodesNestedUnionShapes(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(exerciseSetsPath(), testkit.JSON(http.StatusOK, exerciseSetsBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newActivityDetails(t, h).ExerciseSets(t.Context(), h.session, mustID(t))
	if err != nil {
		t.Fatalf("ExerciseSets() = %v", err)
	}

	sets := got.Sets.Items()
	if len(sets) != 1 {
		t.Fatalf("%d sets decoded, want 1", len(sets))
	}
	if kind, ok := sets[0].SetType.Value(); !ok || kind != "ACTIVE" {
		t.Errorf("SetType = %q/%v, want ACTIVE", kind, ok)
	}
	if reps, ok := sets[0].RepetitionCount.Int64(); !ok || reps != 12 {
		t.Errorf("RepetitionCount = %v/%v, want 12 from the string form", reps, ok)
	}

	exercises := sets[0].Exercises.Items()
	if len(exercises) != 1 {
		t.Fatalf("%d exercises decoded, want 1", len(exercises))
	}
	if category, ok := exercises[0].Category.Value(); !ok || category != "BENCH_PRESS" {
		t.Errorf("Category = %q/%v, want BENCH_PRESS", category, ok)
	}
	if exercises[0].Name.IsSet() {
		t.Error("Name must report absent for an explicit null")
	}
	if got.Payload().Endpoint() != client.EndpointActivityExerciseSet {
		t.Errorf("payload endpoint = %q, want the exercise-set label", got.Payload().Endpoint())
	}
}

func TestActivityDetailsPathsCarryTheIdentifierAsASegment(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(splitsPath(), testkit.JSON(http.StatusOK, `{"splits":[]}`)).
		With(exerciseSetsPath(), testkit.JSON(http.StatusOK, `{"exerciseSets":[]}`))
	h := newHarness(t, script, client.Limits{})
	details := newActivityDetails(t, h)

	if _, err := details.TypedSplits(t.Context(), h.session, mustID(t)); err != nil {
		t.Fatalf("TypedSplits() = %v", err)
	}
	if _, err := details.ExerciseSets(t.Context(), h.session, mustID(t)); err != nil {
		t.Fatalf("ExerciseSets() = %v", err)
	}

	requests := h.server.Requests()
	if len(requests) != 2 {
		t.Fatalf("the fake received %d requests, want 2", len(requests))
	}
	if requests[0].Path != splitsPath() {
		t.Errorf("splits path = %q, want %q", requests[0].Path, splitsPath())
	}
	if requests[1].Path != exerciseSetsPath() {
		t.Errorf("exercise-set path = %q, want %q", requests[1].Path, exerciseSetsPath())
	}
}
