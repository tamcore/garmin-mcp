package api

// The compiled-in strength catalog: the fallback whenever the published catalog
// at [ExerciseCatalogURL] cannot be read. See exercisecatalog.go for what the
// two catalogs are and how they relate.

// catalogRow is one category and the exercise names this package lists for it. A
// row with no names is a valid parent this subset does not enumerate.
type catalogRow struct {
	category string
	names    []string
}

// builtinCatalogRows is the compiled-in catalog.
//
// It is a function returning a fresh slice, not a package variable: a variable is
// assignable, and one assignment would change what every principal in the process
// validates against. A constant that cannot be a const is a function here.
func builtinCatalogRows() []catalogRow {
	return []catalogRow{
		{"BENCH_PRESS", []string{"BARBELL_BENCH_PRESS", "CLOSE_GRIP_BARBELL_BENCH_PRESS",
			"DUMBBELL_BENCH_PRESS", "INCLINE_DUMBBELL_BENCH_PRESS"}},
		{"CALF_RAISE", []string{"SEATED_CALF_RAISE", "SINGLE_LEG_CALF_RAISE", "STANDING_CALF_RAISE"}},
		{"CARDIO", []string{"HIGH_KNEES", "JUMPING_JACKS", "MOUNTAIN_CLIMBER"}},
		{"CARRY", []string{"FARMERS_CARRY", "FARMERS_WALK", "SUITCASE_CARRY", "WAITER_CARRY"}},
		{"CHOP", []string{"CABLE_WOOD_CHOP"}},
		{"CORE", []string{"BIRD_DOG", "DEAD_BUG", "RUSSIAN_TWIST"}},
		{"CRUNCH", []string{"BICYCLE_CRUNCH", "CABLE_CRUNCH", "CRUNCH"}},
		{"CURL", []string{"BARBELL_BICEPS_CURL", "DUMBBELL_BICEPS_CURL", "HAMMER_CURL", "PREACHER_CURL"}},
		{"DEADLIFT", []string{"BARBELL_DEADLIFT", "ROMANIAN_DEADLIFT", "SINGLE_LEG_DEADLIFT",
			"SUMO_DEADLIFT"}},
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
}

// BuiltinExerciseCatalog returns the compiled-in subset. It is built on every
// call, from the rows above, so no caller can be handed a snapshot another caller
// changed.
func BuiltinExerciseCatalog() *ExerciseCatalog {
	return newExerciseCatalog(CatalogSourceBuiltin, builtinRows())
}

// builtinRows renders the compiled-in rows as catalog rows.
func builtinRows() map[string][]ExerciseType {
	source := builtinCatalogRows()
	rows := make(map[string][]ExerciseType, len(source))
	for _, row := range source {
		exercises := make([]ExerciseType, 0, len(row.names))
		for _, name := range row.names {
			exercises = append(exercises, ExerciseType{Name: name, DisplayName: displayLabel(name)})
		}
		rows[row.category] = exercises
	}
	return rows
}
