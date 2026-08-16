package resources

import "strconv"

// structureReference documents the workout vocabulary the templates use.
//
// It is built from the same constants the templates are built from, rather than
// from a second hand-written table. A reference that drifts from the documents it
// describes is worse than none: a caller would read valid-looking values here and
// have Garmin reject the workout.
func structureReference() map[string]any {
	return map[string]any{
		fieldDescription: "Garmin workout document structure. A workout carries one " +
			"segment, and the segment carries ordered steps. A step is either an " +
			"ExecutableStepDTO or a RepeatGroupDTO that nests further steps.",
		"step_types": map[string]any{
			executableStepDTO: "one performed step: a warmup, an interval, a " +
				"recovery, a rest or a cooldown",
			repeatGroupDTO: "a group repeated numberOfIterations times, whose " +
				"workoutSteps are the steps of one round",
		},
		"stepType_values":     keyedValues("stepTypeKey", stepTypeKeys()),
		"endCondition_values": keyedValues("conditionTypeKey", endConditionKeys()),
		"targetType_values":   keyedValues("workoutTargetTypeKey", targetTypeKeys()),
		"sportType_values":    keyedValues("sportTypeKey", sportTypeKeys()),
		fieldEndConditionValue: "the number the endCondition counts: seconds for " +
			"time, metres for distance, repetitions for reps",
		"zoneNumber": "required when targetType is heart.rate.zone: the zone to " +
			"hold, 1 to 5",
		"strength_training_fields": map[string]any{
			"category": "the exercise category, for example BENCH_PRESS. Valid " +
				"values come from get_exercise_types",
			"exerciseName": "the exercise within the category, for example " +
				"BARBELL_BENCH_PRESS",
			"weightValue": "the load, as a number, for example 24.0",
			"weightUnit": "the unit of weightValue, as an object rather than a " +
				"name: {\"unitId\": 8, \"unitKey\": \"kilogram\", \"factor\": 1000.0}",
		},
	}
}

// stepTypeKeys is the step vocabulary, keyed by Garmin's numeric id.
func stepTypeKeys() map[int]string {
	return map[int]string{
		stepTypeWarmup:   "warmup",
		stepTypeCooldown: "cooldown",
		stepTypeInterval: "interval",
		stepTypeRecovery: "recovery",
		stepTypeRest:     "rest",
	}
}

// endConditionKeys is the end-condition vocabulary. lap.button and reps are
// documented although no template here uses them, because the reference describes
// what Garmin accepts rather than what these four documents happen to show.
func endConditionKeys() map[int]string {
	return map[int]string{
		conditionTypeLapButton:  "lap.button",
		conditionTypeTime:       "time",
		conditionTypeDistance:   "distance",
		conditionTypeIterations: "iterations",
		conditionTypeReps:       "reps",
	}
}

func targetTypeKeys() map[int]string {
	return map[int]string{
		targetTypeNone:      "no.target",
		targetTypeHeartRate: "heart.rate.zone",
	}
}

func sportTypeKeys() map[int]string {
	return map[int]string{
		sportTypeRunning:  "running",
		sportTypeStrength: "strength_training",
	}
}

// keyedValues renders an id-to-key table the way upstream's reference does: the
// numeric id as a JSON object key, mapping to the named key field.
func keyedValues(keyName string, values map[int]string) map[string]any {
	out := make(map[string]any, len(values))
	for id, key := range values {
		out[strconv.Itoa(id)] = map[string]any{keyName: key}
	}
	return out
}
