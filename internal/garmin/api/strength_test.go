package api_test

import (
	"errors"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// planStart is the synthetic instant every set plan is timed from.
func planStart() time.Time {
	return time.Date(2026, 1, 31, 9, 0, 0, 0, time.UTC)
}

// sampleSet is one valid active set, used wherever the content does not matter.
func sampleSet(t *testing.T) api.StrengthSet {
	t.Helper()

	return api.StrengthSet{
		Kind:            api.SetActive,
		Start:           planStart(),
		DurationSeconds: 45,
		Repetitions:     10,
		WeightGrams:     20000,
		Category:        categorySquat,
		ExerciseName:    exerciseBackSquat,
	}
}

// TestSetPlanExpandsRepeatsAndRestSpacing is the builder test: a repeated set
// with rest between the repetitions becomes an alternating, absolutely-timed
// list.
func TestSetPlanExpandsRepeatsAndRestSpacing(t *testing.T) {
	t.Parallel()

	plan := api.SetPlan{
		Start: planStart(),
		Sets: []api.PlannedSet{{
			Repeat: 3, Repetitions: 10, WeightGrams: 20000, DurationSeconds: 45,
			RestSeconds: 90, Category: categorySquat, ExerciseName: exerciseBackSquat,
		}},
	}
	built, err := plan.Build()
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}
	if len(built) != 6 {
		t.Fatalf("%d sets, want three active sets and three rests", len(built))
	}

	want := []struct {
		kind   api.SetKind
		offset time.Duration
	}{
		{api.SetActive, 0},
		{api.SetRest, 45 * time.Second},
		{api.SetActive, 135 * time.Second},
		{api.SetRest, 180 * time.Second},
		{api.SetActive, 270 * time.Second},
		{api.SetRest, 315 * time.Second},
	}
	for index, expected := range want {
		got := built[index]
		if got.Kind != expected.kind {
			t.Errorf("set %d kind = %q, want %q", index+1, got.Kind, expected.kind)
		}
		if !got.Start.Equal(planStart().Add(expected.offset)) {
			t.Errorf("set %d starts at %s, want %s", index+1,
				got.Start.Format(api.SetStartTimeLayout),
				planStart().Add(expected.offset).Format(api.SetStartTimeLayout))
		}
	}
	if built[1].Repetitions != 0 || built[1].Category != "" {
		t.Error("a rest set carries repetitions or a category, which Garmin does not store")
	}
}

// TestSetPlanHonorsExplicitOffsetsAndAbsoluteStarts covers the two ways a caller
// places a set itself instead of letting it follow the previous one.
func TestSetPlanHonorsExplicitOffsetsAndAbsoluteStarts(t *testing.T) {
	t.Parallel()

	offset := float64(600)
	absolute := planStart().Add(30 * time.Minute)
	plan := api.SetPlan{
		Start: planStart(),
		Sets: []api.PlannedSet{
			{Repetitions: 5, DurationSeconds: 30, Category: categoryPushUp},
			{Repetitions: 5, DurationSeconds: 30, Category: categoryPushUp, OffsetSeconds: &offset},
			{Repetitions: 5, DurationSeconds: 30, Category: categoryPushUp, StartTime: &absolute},
		},
	}
	built, err := plan.Build()
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}
	if len(built) != 3 {
		t.Fatalf("%d sets, want 3", len(built))
	}
	if !built[0].Start.Equal(planStart()) {
		t.Errorf("first set starts at %s, want the plan start", built[0].Start)
	}
	if !built[1].Start.Equal(planStart().Add(10 * time.Minute)) {
		t.Errorf("second set starts at %s, want the plan start plus the offset", built[1].Start)
	}
	if !built[2].Start.Equal(absolute) {
		t.Errorf("third set starts at %s, want the absolute time", built[2].Start)
	}
}

// TestSetPlanRefusesWhatGarminWouldReject covers the validation the builder
// applies before anything is dispatched.
func TestSetPlanRefusesWhatGarminWouldReject(t *testing.T) {
	t.Parallel()

	cases := map[string]api.SetPlan{
		"no start": {Sets: []api.PlannedSet{{DurationSeconds: 10, Category: categorySquat}}},
		"no sets":  {Start: planStart()},
		"zero duration": {
			Start: planStart(),
			Sets:  []api.PlannedSet{{Category: categorySquat}},
		},
		"unknown category": {
			Start: planStart(),
			Sets:  []api.PlannedSet{{DurationSeconds: 10, Category: categoryUnsupported}},
		},
		"too many repetitions": {
			Start: planStart(),
			Sets: []api.PlannedSet{{
				DurationSeconds: 10, Category: categorySquat, Repetitions: api.MaxRepetitions + 1,
			}},
		},
		"weight over the bound": {
			Start: planStart(),
			Sets: []api.PlannedSet{{
				DurationSeconds: 10, Category: categorySquat, WeightGrams: api.MaxWeightGrams + 1,
			}},
		},
		"repeat over the bound": {
			Start: planStart(),
			Sets: []api.PlannedSet{{
				DurationSeconds: 10, Category: categorySquat, Repeat: api.MaxSetRepeat + 1,
			}},
		},
		"more sets than the bound": {
			Start: planStart(),
			Sets: []api.PlannedSet{
				{DurationSeconds: 1, Category: categorySquat, Repeat: api.MaxSetRepeat, RestSeconds: 1},
				{DurationSeconds: 1, Category: categorySquat, Repeat: api.MaxSetRepeat, RestSeconds: 1},
				{DurationSeconds: 1, Category: categorySquat, Repeat: api.MaxSetRepeat, RestSeconds: 1},
			},
		},
	}

	for name, plan := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := plan.Build(); !errors.Is(err, client.ErrValidation) {
				t.Errorf("Build() = %v, want ErrValidation", err)
			}
		})
	}
}

// TestExerciseCatalogIsUsableForAStrengthStep covers what get_exercise_types
// exists for: a caller reads the categories, their counts and the names under
// them, and then writes a set that validates.
func TestExerciseCatalogIsUsableForAStrengthStep(t *testing.T) {
	t.Parallel()

	catalog := api.ExerciseTypes()
	if len(catalog) < 20 {
		t.Fatalf("%d categories, want the FIT category set", len(catalog))
	}
	for index := 1; index < len(catalog); index++ {
		if catalog[index-1].Category >= catalog[index].Category {
			t.Fatalf("catalog is not ordered at %d: %q then %q", index,
				catalog[index-1].Category, catalog[index].Category)
		}
	}
	if len(api.ExerciseCategories()) != len(catalog) {
		t.Errorf("%d category keys for %d catalog rows",
			len(api.ExerciseCategories()), len(catalog))
	}

	squat, ok := api.LookupExerciseCategory("squat")
	if !ok {
		t.Fatal("LookupExerciseCategory(squat) reported no such category")
	}
	if squat.Count != len(squat.Exercises) || squat.Count == 0 {
		t.Errorf("Count = %d for %d exercises", squat.Count, len(squat.Exercises))
	}
	if squat.DisplayName != "Squat" {
		t.Errorf("DisplayName = %q, want Squat", squat.DisplayName)
	}
	for _, exercise := range squat.Exercises {
		if err := api.ValidateExercise(squat.Category, exercise.Name); err != nil {
			t.Errorf("ValidateExercise(%q, %q) = %v", squat.Category, exercise.Name, err)
		}
		if exercise.DisplayName == "" || exercise.DisplayName == exercise.Name {
			t.Errorf("exercise %q renders no display name", exercise.Name)
		}
	}
}

// TestValidateExerciseKeepsTheCategoryClosedAndTheNameOpen states the one
// asymmetry in the catalog: Garmin's parent set is closed, and its full name enum
// is deliberately not mirrored here.
func TestValidateExerciseKeepsTheCategoryClosedAndTheNameOpen(t *testing.T) {
	t.Parallel()

	if err := api.ValidateExercise(categorySquat, ""); err != nil {
		t.Errorf("an empty name under a known category = %v, want it accepted", err)
	}
	if err := api.ValidateExercise(categorySquat, "SOME_UNLISTED_SQUAT"); err != nil {
		t.Errorf("an unlisted name under a known category = %v, want it accepted", err)
	}
	if err := api.ValidateExercise("UNASSIGNED", ""); !errors.Is(err, client.ErrValidation) {
		t.Errorf("an unknown category = %v, want ErrValidation", err)
	}
	if err := api.ValidateExercise(categorySquat, "back squat; drop table"); !errors.Is(
		err, client.ErrValidation) {
		t.Errorf("a name outside the key charset = %v, want ErrValidation", err)
	}
}
