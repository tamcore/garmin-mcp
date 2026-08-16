package tools_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// argExercises is the exercise-list argument of the strength builder.
const argExercises = "exercises"

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
		argExercises: []any{map[string]any{
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
		argExercises: []any{map[string]any{
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
	if source, _ := out["source"].(string); source != "built_in_subset" {
		t.Errorf("source = %q, want the compiled-in fallback to name itself", source)
	}
	if len(h.fake.Requests()) != 0 {
		t.Errorf("the catalog read reached Garmin: %v", h.recordedMethods())
	}
}

// The category and exercise only the synthetic published document carries.
const (
	webOnlyCategory = "BANDED_EXERCISES"
	webOnlyExercise = "AB_TWIST"
)

// fetchedCatalog builds a snapshot from a synthetic published document.
//
// The document is generated here, from this project's own compiled-in rows plus
// synthetic filler — Garmin's document is not a fixture. The compiled-in rows are
// present because api.ParseExerciseCatalog refuses a document it cannot recognize
// as Garmin's taxonomy, which is the check that keeps a fabricated catalog from
// reaching a write.
func fetchedCatalog(t *testing.T) *api.ExerciseCatalog {
	t.Helper()

	type entry struct {
		Primary   []string `json:"primaryMuscles"`
		Secondary []string `json:"secondaryMuscles"`
	}
	categories := map[string]map[string]map[string]entry{
		webOnlyCategory: {argExercises: {webOnlyExercise: {
			Primary: []string{"ABS"}, Secondary: []string{"OBLIQUES"},
		}}},
	}
	for _, category := range api.BuiltinExerciseCatalog().Types() {
		exercises := map[string]entry{}
		for _, exercise := range category.Exercises {
			exercises[exercise.Name] = entry{Primary: []string{"CHEST"}}
		}
		if len(exercises) == 0 {
			continue
		}
		categories[category.Category] = map[string]map[string]entry{argExercises: exercises}
	}
	for index := range 40 {
		exercises := map[string]entry{}
		for position := range 3 {
			exercises[fmt.Sprintf("SYNTHETIC_MOVEMENT_%d_%d", index, position)] = entry{
				Primary: []string{"CHEST"},
			}
		}
		categories[fmt.Sprintf("SYNTHETIC_CATEGORY_%d", index)] = map[string]map[string]entry{
			argExercises: exercises,
		}
	}

	raw, err := json.Marshal(map[string]any{"categories": categories})
	if err != nil {
		t.Fatalf("encode the synthetic document: %v", err)
	}
	catalog, err := api.ParseExerciseCatalog(raw)
	if err != nil {
		t.Fatalf("api.ParseExerciseCatalog() = %v", err)
	}
	return catalog
}

// TestGetExerciseTypesServesTheFetchedCatalogAndNamesIt is what a caller needs to
// tell a full catalog from the fallback: the source, the counts, and the muscle
// groups the compiled-in subset never had.
func TestGetExerciseTypesServesTheFetchedCatalogAndNamesIt(t *testing.T) {
	h := newCatalogHarness(t, readScript(), fetchedCatalog(t))

	out := h.call(t, tools.ToolGetExerciseTypes, nil)

	if source, _ := out["source"].(string); source != "garmin_web_catalog" {
		t.Errorf("source = %q, want the fetched catalog to name itself", source)
	}
	if count, _ := out["exercise_count"].(float64); count <= builtinExerciseCount {
		t.Errorf("exercise_count = %v, want more than the compiled-in subset carries", count)
	}
	if len(h.fake.Requests()) != 0 {
		t.Errorf("the catalog read reached Garmin: %v", h.recordedMethods())
	}

	categories, _ := out["categories"].([]any)
	muscles, found := 0, false
	for _, raw := range categories {
		category, _ := raw.(map[string]any)
		if category["category"] != webOnlyCategory {
			continue
		}
		found = true
		exercises, _ := category["exercises"].([]any)
		for _, item := range exercises {
			exercise, _ := item.(map[string]any)
			primary, _ := exercise["primary_muscles"].([]any)
			muscles += len(primary)
		}
	}
	if !found {
		t.Fatalf("the fetched category %q is absent from the result", webOnlyCategory)
	}
	if muscles == 0 {
		t.Error("no muscle group reached the result")
	}
}

// builtinExerciseCount is how many exercises the compiled-in subset carries. A
// fetched result has to beat it, or the fetch bought nothing.
const builtinExerciseCount = 98

// TestStrengthWritesAcceptACategoryOnlyTheFetchedCatalogKnows is the other half
// of the fetch: the categories it adds validate, so a caller is not refused work
// Garmin accepts.
func TestStrengthWritesAcceptACategoryOnlyTheFetchedCatalogKnows(t *testing.T) {
	h := newWriteHarnessWithCatalog(t, builderScript(), enabledWrites(), fetchedCatalog(t))

	h.call(t, tools.ToolCreateStrengthWorkout, map[string]any{
		argName: "Banded day",
		argExercises: []any{map[string]any{
			argName:        webOnlyExercise,
			argSets:        3,
			argReps:        10,
			argRestSeconds: 60,
			argCategory:    webOnlyCategory,
		}},
	})

	steps := uploadedSteps(t, h)
	group, _ := steps[0].(map[string]any)
	nested, _ := group["workoutSteps"].([]any)
	set, _ := nested[0].(map[string]any)
	if got := set[argCategory]; got != webOnlyCategory {
		t.Errorf("category = %v, want the fetched category", got)
	}
}
