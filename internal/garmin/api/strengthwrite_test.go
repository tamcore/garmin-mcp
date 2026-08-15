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

func newStrengthWrites(t *testing.T, h harness) *api.StrengthWrites {
	t.Helper()

	writes, err := api.NewStrengthWrites(h.rc)
	if err != nil {
		t.Fatalf("NewStrengthWrites() = %v", err)
	}
	return writes
}

// savedSetsBody is the set list Garmin reports after a successful replace. It
// matches sampleSet, so the verification passes.
const savedSetsBody = `{"exerciseSets":[{"setType":"ACTIVE","repetitionCount":10,` +
	`"weight":20000.0,"exercises":[{"category":"SQUAT","name":"BACK_SQUAT"}],` +
	`"startTime":"2026-01-31T09:00:00.0","unknownBlock":{"x":1}}]}`

// TestReplaceSetsWritesThenReadsBack is the verify-after-write test for the happy
// path: the PUT carries the rendered list and the verifying GET follows it.
func TestReplaceSetsWritesThenReadsBack(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(exerciseSetsPath(),
		testkit.JSON(http.StatusOK, `{"ok":true}`),
		testkit.JSON(http.StatusOK, savedSetsBody))
	h := newHarness(t, script, client.Limits{})

	saved, err := newStrengthWrites(t, h).ReplaceSets(t.Context(), h.session, mustID(t),
		[]api.StrengthSet{sampleSet(t)})
	if err != nil {
		t.Fatalf("ReplaceSets() = %v", err)
	}
	if saved.Sets.Len() != 1 {
		t.Errorf("%d saved sets, want 1", saved.Sets.Len())
	}

	requests := h.server.Requests()
	if len(requests) != 2 {
		t.Fatalf("the fake received %d requests, want the write and the verifying read",
			len(requests))
	}
	if requests[0].Method != http.MethodPut || requests[1].Method != http.MethodGet {
		t.Errorf("methods = %q then %q, want PUT then GET",
			requests[0].Method, requests[1].Method)
	}

	var body struct {
		ActivityID int64 `json:"activityId"`
		Sets       []struct {
			SetType   string  `json:"setType"`
			StartTime string  `json:"startTime"`
			Duration  float64 `json:"duration"`
			Reps      int     `json:"repetitionCount"`
			Weight    float64 `json:"weight"`
			Exercises []struct {
				Category string  `json:"category"`
				Name     *string `json:"name"`
			} `json:"exercises"`
		} `json:"exerciseSets"`
	}
	if err := json.Unmarshal(requests[0].Body, &body); err != nil {
		t.Fatalf("Unmarshal(body) = %v", err)
	}
	if len(body.Sets) != 1 {
		t.Fatalf("%d sets written, want 1", len(body.Sets))
	}

	set := body.Sets[0]
	if set.SetType != "ACTIVE" || set.Reps != 10 || set.Weight != 20000 {
		t.Errorf("written set = %+v, want the sample set", set)
	}
	if set.StartTime != "2026-01-31T09:00:00.0" {
		t.Errorf("startTime = %q, want the UTC set layout", set.StartTime)
	}
	if len(set.Exercises) != 1 || set.Exercises[0].Category != categorySquat {
		t.Errorf("exercises = %+v, want the category carried through", set.Exercises)
	}

	// Garmin refuses the whole write with "Activity ID should not be Null in the
	// Exercises Object" when the envelope omits the activity, and it is the envelope
	// that must carry it: repeating it inside the set or inside the exercise does
	// not satisfy the check. Confirmed against the live service on 2026-08-15.
	if body.ActivityID != mustID(t).Int64() {
		t.Errorf("activityId = %d, want the path identifier: Garmin refuses an envelope "+
			"that does not name the activity", body.ActivityID)
	}
}

// TestReplaceSetsReportsAMismatchRatherThanSuccess is the point of the
// verification: Garmin accepting the PUT is not evidence that it stored what was
// sent.
func TestReplaceSetsReportsAMismatchRatherThanSuccess(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"a set went missing": `{"exerciseSets":[]}`,
		"the repetitions changed": `{"exerciseSets":[{"setType":"ACTIVE","repetitionCount":8,` +
			`"weight":20000.0,"exercises":[{"category":"SQUAT","name":"BACK_SQUAT"}]}]}`,
		"the weight changed": `{"exerciseSets":[{"setType":"ACTIVE","repetitionCount":10,` +
			`"weight":15000.0,"exercises":[{"category":"SQUAT","name":"BACK_SQUAT"}]}]}`,
		"the exercise changed": `{"exerciseSets":[{"setType":"ACTIVE","repetitionCount":10,` +
			`"weight":20000.0,"exercises":[{"category":"SQUAT","name":"FRONT_SQUAT"}]}]}`,
		"the set type changed": `{"exerciseSets":[{"setType":"REST","repetitionCount":10,` +
			`"weight":20000.0,"exercises":[{"category":"SQUAT","name":"BACK_SQUAT"}]}]}`,
		"the exercise vanished": `{"exerciseSets":[{"setType":"ACTIVE","repetitionCount":10,` +
			`"weight":20000.0,"exercises":[]}]}`,
	}

	for name, savedBody := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(exerciseSetsPath(),
				testkit.JSON(http.StatusOK, `{"ok":true}`),
				testkit.JSON(http.StatusOK, savedBody))
			h := newHarness(t, script, client.Limits{})

			_, err := newStrengthWrites(t, h).ReplaceSets(t.Context(), h.session, mustID(t),
				[]api.StrengthSet{sampleSet(t)})
			if !errors.Is(err, client.ErrMalformedPayload) {
				t.Fatalf("ReplaceSets() = %v, want the mismatch reported", err)
			}

			var apiErr *client.APIError
			if !errors.As(err, &apiErr) {
				t.Fatal("the mismatch is not an *APIError")
			}
			if apiErr.Op != client.OpSetActivityExerciseSets {
				t.Errorf("Op = %q, want the write's own label", apiErr.Op)
			}
		})
	}
}

// TestReplaceSetsRefusesAnUnusableList keeps validation ahead of dispatch.
func TestReplaceSetsRefusesAnUnusableList(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	writes := newStrengthWrites(t, h)

	if _, err := writes.ReplaceSets(t.Context(), h.session, mustID(t), nil); !errors.Is(
		err, client.ErrValidation) {
		t.Errorf("ReplaceSets() with no sets = %v, want ErrValidation", err)
	}
	bad := sampleSet(t)
	bad.Category = categoryUnsupported
	if _, err := writes.ReplaceSets(t.Context(), h.session, mustID(t),
		[]api.StrengthSet{bad}); !errors.Is(err, client.ErrValidation) {
		t.Errorf("ReplaceSets() with an unknown category = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// strengthCreateScript scripts the exchanges a verified create performs: the
// activity create, the set replace with its verifying read, and the activity read
// that confirms the identifier.
func strengthCreateScript(summaryBody string) testkit.Script {
	return testkit.NewScript().
		With(client.PathActivityPrefix, testkit.JSON(http.StatusOK, `{"activityId":18446744}`)).
		With(exerciseSetsPath(),
			testkit.JSON(http.StatusOK, `{"ok":true}`),
			testkit.JSON(http.StatusOK, savedSetsBody)).
		With(activityWritePath(), testkit.JSON(http.StatusOK, summaryBody))
}

// strengthActivity is the synthetic session both create tests write.
func strengthActivity() api.StrengthActivity {
	return api.StrengthActivity{
		Name:       "Lower body",
		StartLocal: "2026-01-31T09:00:00.000",
		TimeZone:   "UTC",
		Plan: api.SetPlan{
			Start: planStart(),
			Sets: []api.PlannedSet{{
				Repetitions: 10, WeightGrams: 20000, DurationSeconds: 45,
				Category: categorySquat, ExerciseName: exerciseBackSquat,
			}},
		},
	}
}

// TestCreateStrengthActivityVerifiesWhatItSaved covers the whole create: a
// private strength activity, its sets, and both verification reads.
func TestCreateStrengthActivityVerifiesWhatItSaved(t *testing.T) {
	t.Parallel()

	h := newHarness(t, strengthCreateScript(`{"activityId":18446744}`), client.Limits{})
	created, err := newStrengthWrites(t, h).Create(t.Context(), h.session, strengthActivity())
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if created.Activity.Int64() != 18446744 {
		t.Errorf("Activity = %d, want the created identifier", created.Activity.Int64())
	}
	if created.Sets.Sets.Len() != 1 {
		t.Errorf("%d saved sets, want 1", created.Sets.Sets.Len())
	}

	requests := h.server.Requests()
	if len(requests) != 4 {
		t.Fatalf("the fake received %d requests, want create, replace, verify and read back",
			len(requests))
	}
	if got := nestedObject(t, requests[0].Body, "activityTypeDTO")["typeKey"]; got !=
		api.StrengthActivityTypeKey {
		t.Errorf("typeKey = %v, want %q", got, api.StrengthActivityTypeKey)
	}
	if got := nestedObject(t, requests[0].Body, "accessControlRuleDTO")["typeKey"]; got !=
		"private" {
		t.Errorf("accessControlRuleDTO.typeKey = %v, want private", got)
	}
	if got := nestedObject(t, requests[0].Body, "summaryDTO")["duration"]; got != float64(45) {
		t.Errorf("duration = %v, want the set list's own 45 seconds", got)
	}
}

// TestCreateStrengthActivityReportsAnIdentifierMismatch covers the second
// verification: an activity that does not report the identifier it was created
// with is a failure, not a success.
func TestCreateStrengthActivityReportsAnIdentifierMismatch(t *testing.T) {
	t.Parallel()

	h := newHarness(t, strengthCreateScript(`{"activityId":991002}`), client.Limits{})
	_, err := newStrengthWrites(t, h).Create(t.Context(), h.session, strengthActivity())
	if !errors.Is(err, client.ErrMalformedPayload) {
		t.Fatalf("Create() = %v, want the identifier mismatch reported", err)
	}
}

// TestCreateStrengthActivityRefusesABadPlanBeforeCreatingAnything is why the plan
// is built first: validating halfway through would leave an activity behind that
// no caller asked for.
func TestCreateStrengthActivityRefusesABadPlanBeforeCreatingAnything(t *testing.T) {
	t.Parallel()

	h := newHarness(t, strengthCreateScript(`{"activityId":18446744}`), client.Limits{})
	activity := strengthActivity()
	activity.Plan.Sets[0].Category = categoryUnsupported

	if _, err := newStrengthWrites(t, h).Create(t.Context(), h.session, activity); !errors.Is(
		err, client.ErrValidation) {
		t.Fatalf("Create() = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0: nothing may be created", got)
	}
}
