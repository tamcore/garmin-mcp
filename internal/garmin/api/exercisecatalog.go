package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The strength-exercise catalog.
//
// Garmin validates a strength set against its own FIT enum: exercises[].category
// is the parent and exercises[].name is the sub-category, an unknown value is
// rejected with 400 "Invalid Sub-Category Passed", and a null name is always
// accepted under a known parent. A caller therefore needs to know which
// categories exist, and which names live under them, before it writes a set.
//
// The catalog is compiled in rather than fetched. Garmin publishes it at
// connect.garmin.com/web-data/exercises/Exercises.json, which is the web tier,
// not the API tier this package addresses, and nothing here may widen its host
// boundary to reach it.
//
// It is a documented subset, not a mirror: the categories are the FIT
// exercise_category enum, and each one carries the common names of that
// category. Garmin stays the authority — a name this catalog omits may still be
// valid, and a name it lists is still rejected if Garmin's enum disagrees.
// ValidateExercise reflects that: it refuses an unknown category, because the
// parent set is closed, and accepts an unlisted name under a known category
// rather than pretending to know Garmin's full enum.

// ExerciseType is one exercise name with the label a user reads.
type ExerciseType struct {
	// Name is Garmin's exercise key, for example "BARBELL_BENCH_PRESS".
	Name string `json:"name"`
	// DisplayName is the human-readable label derived from the key.
	DisplayName string `json:"displayName"`
}

// ExerciseCategory is one strength category and the exercises under it.
type ExerciseCategory struct {
	// Category is Garmin's category key, for example "BENCH_PRESS".
	Category string `json:"category"`
	// DisplayName is the human-readable label derived from the key.
	DisplayName string `json:"displayName"`
	// Count is how many exercises this catalog lists for the category.
	Count int `json:"count"`
	// Exercises are the listed exercises, ordered by name.
	Exercises []ExerciseType `json:"exercises"`
}

// catalogRow is one category and the exercise names this package lists for it. A
// row with no names is a valid parent this subset does not enumerate.
type catalogRow struct {
	category string
	names    []string
}

// exerciseCatalog is the compiled-in catalog. It is an array rather than a map so
// there is no package-level mutable state: nothing can insert a category at
// runtime, and the order is the source order.
var exerciseCatalog = [...]catalogRow{
	{"BENCH_PRESS", []string{"BARBELL_BENCH_PRESS", "CLOSE_GRIP_BARBELL_BENCH_PRESS",
		"DUMBBELL_BENCH_PRESS", "INCLINE_DUMBBELL_BENCH_PRESS"}},
	{"CALF_RAISE", []string{"SEATED_CALF_RAISE", "SINGLE_LEG_CALF_RAISE", "STANDING_CALF_RAISE"}},
	{"CARDIO", []string{"HIGH_KNEES", "JUMPING_JACKS", "MOUNTAIN_CLIMBER"}},
	{"CARRY", []string{"FARMERS_CARRY", "FARMERS_WALK", "SUITCASE_CARRY", "WAITER_CARRY"}},
	{"CHOP", []string{"CABLE_WOOD_CHOP"}},
	{"CORE", []string{"BIRD_DOG", "DEAD_BUG", "RUSSIAN_TWIST"}},
	{"CRUNCH", []string{"BICYCLE_CRUNCH", "CABLE_CRUNCH", "CRUNCH"}},
	{"CURL", []string{"BARBELL_BICEPS_CURL", "DUMBBELL_BICEPS_CURL", "HAMMER_CURL", "PREACHER_CURL"}},
	{"DEADLIFT", []string{"BARBELL_DEADLIFT", "ROMANIAN_DEADLIFT", "SINGLE_LEG_DEADLIFT", "SUMO_DEADLIFT"}},
	{"FLYE", []string{"CABLE_CROSSOVER", "DUMBBELL_FLYE", "INCLINE_DUMBBELL_FLYE"}},
	{"HIP_RAISE", []string{"BARBELL_HIP_THRUST", "GLUTE_BRIDGE", "SINGLE_LEG_GLUTE_BRIDGE"}},
	{"HIP_STABILITY", []string{"CLAM_SHELL", "LATERAL_BAND_WALK"}},
	{"HIP_SWING", []string{"KETTLEBELL_SWING", "SINGLE_ARM_KETTLEBELL_SWING"}},
	{"HYPEREXTENSION", []string{"BACK_EXTENSION", "HYPEREXTENSION"}},
	{"LATERAL_RAISE", []string{"BENT_OVER_LATERAL_RAISE", "FRONT_RAISE", "LATERAL_RAISE"}},
	{"LEG_CURL", []string{"GOOD_MORNING", "LEG_CURL", "SEATED_LEG_CURL"}},
	{"LEG_RAISE", []string{"HANGING_LEG_RAISE", "LYING_LEG_RAISE"}},
	{"LUNGE", []string{"REVERSE_LUNGE", "SIDE_LUNGE", "WALKING_LUNGE"}},
	{"OLYMPIC_LIFT", []string{"CLEAN", "CLEAN_AND_JERK", "POWER_CLEAN", "SNATCH"}},
	{"PLANK", []string{"PLANK", "SIDE_PLANK"}},
	{"PLYO", []string{"BOX_JUMP", "BURPEE", "JUMP_SQUAT"}},
	{"PULL_UP", []string{"CHIN_UP", "NEUTRAL_GRIP_PULL_UP", "PULL_UP", "WIDE_GRIP_PULL_UP"}},
	{"PUSH_UP", []string{"DECLINE_PUSH_UP", "DIAMOND_PUSH_UP", "PUSH_UP", "WIDE_GRIP_PUSH_UP"}},
	{"ROW", []string{"BARBELL_ROW", "DUMBBELL_ROW", "INVERTED_ROW", "SEATED_CABLE_ROW"}},
	{"RUN", []string{"RUN", "TREADMILL_RUN"}},
	{"SHOULDER_PRESS", []string{"ARNOLD_PRESS", "BARBELL_SHOULDER_PRESS", "DUMBBELL_SHOULDER_PRESS"}},
	{"SHOULDER_STABILITY", []string{"EXTERNAL_ROTATION", "FACE_PULL"}},
	{"SHRUG", []string{"BARBELL_SHRUG", "DUMBBELL_SHRUG"}},
	{"SIT_UP", []string{"SIT_UP", "V_UP", "WEIGHTED_SIT_UP"}},
	{"SQUAT", []string{"BACK_SQUAT", "FRONT_SQUAT", "GOBLET_SQUAT", "OVERHEAD_SQUAT", "SPLIT_SQUAT"}},
	{"TOTAL_BODY", []string{"BURPEE", "MAN_MAKER", "THRUSTER"}},
	{"TRICEPS_EXTENSION", []string{"OVERHEAD_TRICEPS_EXTENSION", "SKULL_CRUSHER", "TRICEPS_PRESSDOWN"}},
	{"WARM_UP", []string{"ARM_CIRCLES", "LEG_SWINGS"}},
	{"UNKNOWN", nil},
}

// ExerciseTypes returns the whole catalog, ordered by category, with the count
// of exercises each category lists. The result is freshly built, so no caller
// can mutate what another caller reads.
func ExerciseTypes() []ExerciseCategory {
	categories := make([]ExerciseCategory, 0, len(exerciseCatalog))
	for _, row := range exerciseCatalog {
		categories = append(categories, buildCategory(row.category, row.names))
	}
	sort.Slice(categories, func(i, j int) bool {
		return categories[i].Category < categories[j].Category
	})
	return categories
}

// buildCategory renders one catalog row.
func buildCategory(category string, names []string) ExerciseCategory {
	exercises := make([]ExerciseType, 0, len(names))
	for _, name := range names {
		exercises = append(exercises, ExerciseType{Name: name, DisplayName: displayLabel(name)})
	}
	return ExerciseCategory{
		Category:    category,
		DisplayName: displayLabel(category),
		Count:       len(exercises),
		Exercises:   exercises,
	}
}

// ExerciseCategories returns the recognized category keys, ordered.
func ExerciseCategories() []string {
	keys := make([]string, 0, len(exerciseCatalog))
	for _, row := range exerciseCatalog {
		keys = append(keys, row.category)
	}
	sort.Strings(keys)
	return keys
}

// LookupExerciseCategory returns one catalog row and whether the category is
// recognized.
func LookupExerciseCategory(category string) (ExerciseCategory, bool) {
	key := normalizeExerciseKey(category)
	for _, row := range exerciseCatalog {
		if row.category == key {
			return buildCategory(row.category, row.names), true
		}
	}
	return ExerciseCategory{}, false
}

// MaxExerciseKeyLen bounds an exercise or category key, so a hostile value
// cannot reach Garmin or a log line at length.
const MaxExerciseKeyLen = 64

// ValidateExercise reports whether a category and an optional exercise name may
// be written.
//
// The category must be one Garmin knows, because that set is closed and an
// unknown parent is a guaranteed 400. An empty name is valid — Garmin accepts a
// null name under a known parent — and a name this subset does not list is
// accepted after a lexical check, because the catalog is not a mirror of
// Garmin's full enum and refusing an unlisted name would refuse valid work.
func ValidateExercise(category, name string) error {
	if _, known := LookupExerciseCategory(category); !known {
		return fmt.Errorf("%w: exercise category is not one Garmin recognizes",
			client.ErrValidation)
	}
	if name == "" {
		return nil
	}
	if !isExerciseKey(normalizeExerciseKey(name)) {
		return fmt.Errorf("%w: exercise name must be upper-case letters, digits or underscores",
			client.ErrValidation)
	}
	return nil
}

// normalizeExerciseKey upper-cases and trims a key, which is the form Garmin
// stores.
func normalizeExerciseKey(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

// isExerciseKey reports whether value is a bounded upper-case enum key.
func isExerciseKey(value string) bool {
	if value == "" || len(value) > MaxExerciseKeyLen {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// displayLabel renders an enum key as a readable label: BARBELL_BENCH_PRESS
// becomes "Barbell Bench Press".
func displayLabel(key string) string {
	words := strings.Split(strings.ToLower(key), "_")
	for index, word := range words {
		if word == "" {
			continue
		}
		words[index] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}
