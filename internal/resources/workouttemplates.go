package resources

// The workout document vocabulary.
//
// Every identifier below was read from the pinned upstream's own structure
// reference, which pairs each numeric id with its key. They agree with the
// constants internal/tools builds workouts from, which is the shape the live write
// suite has uploaded to Garmin — so a template here and a workout this server
// builds describe steps the same way.
const (
	stepTypeWarmup   = 1
	stepTypeCooldown = 2
	stepTypeInterval = 3
	stepTypeRecovery = 4
	stepTypeRest     = 5

	conditionTypeLapButton  = 1
	conditionTypeTime       = 2
	conditionTypeDistance   = 3
	conditionTypeIterations = 7
	conditionTypeReps       = 10

	targetTypeNone      = 1
	targetTypeHeartRate = 4

	sportTypeRunning  = 1
	sportTypeStrength = 5

	executableStepDTO = "ExecutableStepDTO"
	repeatGroupDTO    = "RepeatGroupDTO"
)

// The document field names, named once so the templates and the reference that
// documents them cannot spell a key two ways.
const (
	fieldType              = "type"
	fieldStepOrder         = "stepOrder"
	fieldStepType          = "stepType"
	fieldDescription       = "description"
	fieldEndCondition      = "endCondition"
	fieldEndConditionValue = "endConditionValue"
	fieldTargetType        = "targetType"
	fieldWorkoutSteps      = "workoutSteps"
)

// pair is Garmin's id-and-key shape, which every vocabulary field in a workout
// document uses. The JSON key names differ per field, so each accessor names them.
func pair(idKey, keyKey string, id int, key string) map[string]any {
	return map[string]any{idKey: id, keyKey: key}
}

func stepType(id int, key string) map[string]any {
	return pair("stepTypeId", "stepTypeKey", id, key)
}

func endCondition(id int, key string) map[string]any {
	return pair("conditionTypeId", "conditionTypeKey", id, key)
}

func targetType(id int, key string) map[string]any {
	return pair("workoutTargetTypeId", "workoutTargetTypeKey", id, key)
}

func sportType(id int, key string) map[string]any {
	return pair("sportTypeId", "sportTypeKey", id, key)
}

func runningSport() map[string]any  { return sportType(sportTypeRunning, "running") }
func strengthSport() map[string]any { return sportType(sportTypeStrength, "strength_training") }

func noTarget() map[string]any { return targetType(targetTypeNone, "no.target") }

// timedStep is one step that ends after a number of seconds, with no target.
func timedStep(order int, kind map[string]any, seconds float64, description string) map[string]any {
	return map[string]any{
		fieldType:              executableStepDTO,
		fieldStepOrder:         order,
		fieldStepType:          kind,
		fieldDescription:       description,
		fieldEndCondition:      endCondition(conditionTypeTime, "time"),
		fieldEndConditionValue: seconds,
		fieldTargetType:        noTarget(),
	}
}

// distanceStep is one step that ends after a number of metres.
func distanceStep(order int, kind map[string]any, metres float64, description string) map[string]any {
	return map[string]any{
		fieldType:              executableStepDTO,
		fieldStepOrder:         order,
		fieldStepType:          kind,
		fieldDescription:       description,
		fieldEndCondition:      endCondition(conditionTypeDistance, "distance"),
		fieldEndConditionValue: metres,
		fieldTargetType:        noTarget(),
	}
}

// workout wraps the steps in the one-segment envelope Garmin expects.
func workout(name, description string, sport map[string]any, steps []any) map[string]any {
	return map[string]any{
		"workoutName":    name,
		fieldDescription: description,
		"sportType":      sport,
		"workoutSegments": []any{map[string]any{
			"segmentOrder":    1,
			"sportType":       sport,
			fieldWorkoutSteps: steps,
		}},
	}
}

// simpleRunTemplate is warm-up, steady run, cool-down, all time-bounded.
func simpleRunTemplate() map[string]any {
	return workout("Simple Run", "Easy run with warmup and cooldown", runningSport(), []any{
		timedStep(1, stepType(stepTypeWarmup, "warmup"), 600, "Easy warmup jog"),
		timedStep(2, stepType(stepTypeInterval, "interval"), 1800, "Steady run"),
		timedStep(3, stepType(stepTypeCooldown, "cooldown"), 600, "Easy cooldown jog"),
	})
}

// intervalRunTemplate is six 400 m efforts with two-minute recoveries, inside a
// repeat group. It is the template that shows how RepeatGroupDTO nests its steps.
func intervalRunTemplate() map[string]any {
	repeat := map[string]any{
		fieldType:            repeatGroupDTO,
		fieldStepOrder:       2,
		"numberOfIterations": 6,
		fieldEndCondition:    endCondition(conditionTypeIterations, "iterations"),
		fieldWorkoutSteps: []any{
			distanceStep(3, stepType(stepTypeInterval, "interval"), 400, "400m at 5k effort"),
			timedStep(4, stepType(stepTypeRecovery, "recovery"), 120, "2min easy recovery"),
		},
	}
	return workout("6x400m Intervals", "Interval session with repeat group",
		runningSport(), []any{
			timedStep(1, stepType(stepTypeWarmup, "warmup"), 600, "Easy warmup jog"),
			repeat,
			timedStep(5, stepType(stepTypeCooldown, "cooldown"), 600, "Easy cooldown jog"),
		})
}

// tempoRunTemplate targets a heart-rate zone rather than leaving the effort open.
func tempoRunTemplate() map[string]any {
	tempo := timedStep(2, stepType(stepTypeInterval, "interval"), 1200, "20min tempo at HR zone 4")
	tempo[fieldTargetType] = targetType(targetTypeHeartRate, "heart.rate.zone")
	tempo["zoneNumber"] = 4

	return workout("Tempo Run", "Tempo block held in heart rate zone 4",
		runningSport(), []any{
			timedStep(1, stepType(stepTypeWarmup, "warmup"), 600, "Easy warmup jog"),
			tempo,
			timedStep(3, stepType(stepTypeCooldown, "cooldown"), 600, "Easy cooldown jog"),
		})
}

// strengthCircuitTemplate is three rounds of work and rest. The exercise fields are
// what distinguish a strength step from a running one; the catalog get_exercise_types
// serves is where a valid category and name come from.
func strengthCircuitTemplate() map[string]any {
	work := timedStep(3, stepType(stepTypeInterval, "interval"), 600, "10min circuit work")
	work["category"] = "BENCH_PRESS"
	work["exerciseName"] = "BARBELL_BENCH_PRESS"

	repeat := map[string]any{
		fieldType:            repeatGroupDTO,
		fieldStepOrder:       2,
		"numberOfIterations": 3,
		fieldEndCondition:    endCondition(conditionTypeIterations, "iterations"),
		fieldWorkoutSteps: []any{
			work,
			timedStep(4, stepType(stepTypeRest, "rest"), 120, "2min rest between rounds"),
		},
	}
	return workout("Strength Circuit", "Three rounds of circuit work and rest",
		strengthSport(), []any{
			timedStep(1, stepType(stepTypeWarmup, "warmup"), 300, "Mobility warmup"),
			repeat,
			timedStep(5, stepType(stepTypeCooldown, "cooldown"), 300, "Stretch and cool down"),
		})
}
