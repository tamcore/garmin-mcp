package tools_test

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// builderScript answers every builder's upload with one saved workout.
func builderScript() testkit.Script {
	return testkit.NewScript().
		With(client.PathSocialProfile, repeat(okJSON(profileBody), 4)...).
		With(client.PathWorkoutPrefix, repeat(okJSON(savedWorkoutBody), 4)...)
}

// uploadedSteps returns the built workout's steps as they were sent.
func uploadedSteps(t *testing.T, h harness) []any {
	t.Helper()

	body := h.bodyFor(t, http.MethodPost, client.PathWorkoutPrefix)
	segments, _ := body["workoutSegments"].([]any)
	if len(segments) != 1 {
		t.Fatalf("the built document has %d segments, want exactly one", len(segments))
	}
	segment, _ := segments[0].(map[string]any)
	steps, _ := segment["workoutSteps"].([]any)
	return steps
}

func TestCreateWalkRunWorkoutBuildsAWarmupARepeatAndACooldown(t *testing.T) {
	h := newWriteHarness(t, builderScript(), enabledWrites())

	h.call(t, tools.ToolCreateWalkRunWorkout, map[string]any{
		argName:        "W3 two by two",
		argRunSeconds:  120,
		argWalkSeconds: 120,
		"repeats":      6,
		argWarmupMin:   5,
		argCooldownMin: 5,
	})

	steps := uploadedSteps(t, h)
	if len(steps) != 3 {
		t.Fatalf("the built workout has %d top-level steps, want three", len(steps))
	}
	group, _ := steps[1].(map[string]any)
	if got := group["type"]; got != "RepeatGroupDTO" {
		t.Errorf("the middle step is %v, want a RepeatGroupDTO", got)
	}
	if got := group["numberOfIterations"]; got != float64(6) {
		t.Errorf("numberOfIterations = %v, want 6", got)
	}
	condition, _ := group["endCondition"].(map[string]any)
	if got := condition["conditionTypeId"]; got != float64(7) {
		t.Errorf("the repeat end condition id = %v, want 7: omitting it corrupts the count", got)
	}
}

func TestCreateWalkRunWorkoutTargetsTheNamedZoneByDefault(t *testing.T) {
	h := newWriteHarness(t, builderScript(), enabledWrites())

	h.call(t, tools.ToolCreateWalkRunWorkout, map[string]any{
		argName:        "Default zone",
		argRunSeconds:  120,
		argWalkSeconds: 60,
		"repeats":      2,
		argWarmupMin:   5,
		argCooldownMin: 5,
	})

	steps := uploadedSteps(t, h)
	group, _ := steps[1].(map[string]any)
	nested, _ := group["workoutSteps"].([]any)
	run, _ := nested[0].(map[string]any)
	if got := run["zoneNumber"]; got != float64(3) {
		t.Errorf("zoneNumber = %v, want the declared Z3 default", got)
	}
}

func TestCreateRunWorkoutPrefersAnExplicitRangeOverTheNamedZone(t *testing.T) {
	h := newWriteHarness(t, builderScript(), enabledWrites())

	h.call(t, tools.ToolCreateRunWorkout, map[string]any{
		argName:        "Exact range",
		argRunSeconds:  1800,
		argWarmupMin:   5,
		argCooldownMin: 5,
		argHRMin:       136,
		argHRMax:       148,
	})

	steps := uploadedSteps(t, h)
	run, _ := steps[1].(map[string]any)
	if _, present := run["zoneNumber"]; present {
		t.Error("the step carries a zoneNumber beside a range; Garmin discards the range")
	}
	if got := run["targetValueOne"]; got != float64(136) {
		t.Errorf("targetValueOne = %v, want 136", got)
	}
	if got := run["targetValueTwo"]; got != float64(148) {
		t.Errorf("targetValueTwo = %v, want 148", got)
	}
}

func TestCreateRunWorkoutRefusesHalfARange(t *testing.T) {
	h := newWriteHarness(t, builderScript(), enabledWrites())

	h.callError(t, tools.ToolCreateRunWorkout, map[string]any{
		argName:        "Half a range",
		argRunSeconds:  1800,
		argWarmupMin:   5,
		argCooldownMin: 5,
		argHRMin:       136,
	})

	for _, recorded := range h.recordedMethods() {
		if recorded == http.MethodPost+" "+client.PathWorkoutPrefix {
			t.Errorf("half a range still reached an upload: %v", h.recordedMethods())
		}
	}
}

func TestCreateZ2WalkWorkoutBuildsOneSteadyBlock(t *testing.T) {
	h := newWriteHarness(t, builderScript(), enabledWrites())

	h.call(t, tools.ToolCreateZ2WalkWorkout, map[string]any{
		argName:        "Steady walk",
		"duration_min": 45,
		argHRMin:       110,
		argHRMax:       130,
	})

	body := h.bodyFor(t, http.MethodPost, client.PathWorkoutPrefix)
	sport, _ := body["sportType"].(map[string]any)
	if got := sport["sportTypeKey"]; got != "walking" {
		t.Errorf("sportTypeKey = %v, want walking", got)
	}

	steps := uploadedSteps(t, h)
	if len(steps) != 1 {
		t.Fatalf("the built workout has %d steps, want one steady block", len(steps))
	}
	block, _ := steps[0].(map[string]any)
	if got := block["endConditionValue"]; got != float64(45*60) {
		t.Errorf("endConditionValue = %v, want the block in seconds", got)
	}
}

func TestCreateStrengthWorkoutBuildsRepsStepsWithRests(t *testing.T) {
	h := newWriteHarness(t, builderScript(), enabledWrites())

	h.call(t, tools.ToolCreateStrengthWorkout, map[string]any{
		argName: "Push day",
		"exercises": []any{map[string]any{
			argName:        testExerciseName,
			argSets:        3,
			argReps:        8,
			argRestSeconds: 90,
			argCategory:    testCategory,
		}},
	})

	steps := uploadedSteps(t, h)
	group, _ := steps[0].(map[string]any)
	nested, _ := group["workoutSteps"].([]any)
	if len(nested) != 2 {
		t.Fatalf("the exercise built %d nested steps, want the set and its rest", len(nested))
	}
	set, _ := nested[0].(map[string]any)
	condition, _ := set["endCondition"].(map[string]any)
	if got := condition["conditionTypeId"]; got != float64(10) {
		t.Errorf("the set end condition id = %v, want 10 (reps)", got)
	}
	if got := set[argCategory]; got != testCategory {
		t.Errorf("category = %v, want the validated category", got)
	}
}

func TestCreateStrengthWorkoutRefusesACategoryTheCatalogDoesNotKnow(t *testing.T) {
	h := newWriteHarness(t, builderScript(), enabledWrites())

	h.callError(t, tools.ToolCreateStrengthWorkout, map[string]any{
		argName: "Bad category",
		"exercises": []any{map[string]any{
			argName:        "SOMETHING",
			argSets:        3,
			argReps:        8,
			argRestSeconds: 60,
			argCategory:    "UNASSIGNED",
		}},
	})

	for _, recorded := range h.recordedMethods() {
		if recorded == http.MethodPost+" "+client.PathWorkoutPrefix {
			t.Errorf("an unknown category still reached an upload: %v", h.recordedMethods())
		}
	}
}

func TestGetExerciseTypesServesTheCompiledCatalogWithoutCallingGarmin(t *testing.T) {
	h := newHarness(t, readScript())

	out := h.call(t, tools.ToolGetExerciseTypes, nil)

	if count, _ := out["count"].(float64); count == 0 {
		t.Error("the catalog is empty")
	}
	if len(h.fake.Requests()) != 0 {
		t.Errorf("the catalog read reached Garmin: %v", h.recordedMethods())
	}
}
