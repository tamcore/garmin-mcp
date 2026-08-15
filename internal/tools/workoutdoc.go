package tools

import (
	"encoding/json"
	"strconv"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
)

// Garmin's workout vocabulary, as numeric ids.
//
// Garmin treats the numeric id as authoritative and rejects a document whose id and
// key disagree, so the two are never written apart: every pair below comes from one
// of the constructors in this file.
const (
	stepTypeWarmup   = 1
	stepTypeCooldown = 2
	stepTypeInterval = 3
	stepTypeRecovery = 4
	stepTypeRest     = 5

	conditionTypeTime       = 2
	conditionTypeIterations = 7
	conditionTypeReps       = 10

	targetTypeNone      = 1
	targetTypeHeartRate = 4

	sportTypeRunning  = 1
	sportTypeStrength = 5
	sportTypeWalking  = 11

	executableStepDTO   = "ExecutableStepDTO"
	repeatGroupDTO      = "RepeatGroupDTO"
	heartRateZoneCount  = 5
	heartRateZonePrefix = "Z"
)

// stepType is Garmin's step-kind pair.
type stepType struct {
	ID  int    `json:"stepTypeId"`
	Key string `json:"stepTypeKey"`
}

// endCondition is Garmin's end-condition pair.
type endCondition struct {
	ID  int    `json:"conditionTypeId"`
	Key string `json:"conditionTypeKey"`
}

// targetType is Garmin's step-target pair.
type targetType struct {
	ID  int    `json:"workoutTargetTypeId"`
	Key string `json:"workoutTargetTypeKey"`
}

// sportType is Garmin's workout sport pair, whose ids differ from the activity API's.
type sportType struct {
	ID  int    `json:"sportTypeId"`
	Key string `json:"sportTypeKey"`
}

func runningSport() sportType  { return sportType{ID: sportTypeRunning, Key: "running"} }
func walkingSport() sportType  { return sportType{ID: sportTypeWalking, Key: "walking"} }
func strengthSport() sportType { return sportType{ID: sportTypeStrength, Key: "strength_training"} }

func timeCondition() endCondition { return endCondition{ID: conditionTypeTime, Key: "time"} }

func repsCondition() endCondition { return endCondition{ID: conditionTypeReps, Key: argNameReps} }

func iterationsCondition() endCondition {
	return endCondition{ID: conditionTypeIterations, Key: "iterations"}
}

func noTarget() targetType { return targetType{ID: targetTypeNone, Key: "no.target"} }

func heartRateTarget() targetType {
	return targetType{ID: targetTypeHeartRate, Key: "heart.rate.zone"}
}

// warmupStep, cooldownStep, intervalStep, recoveryStep and restStep name the five
// step kinds a builder uses.
func warmupStep() stepType   { return stepType{ID: stepTypeWarmup, Key: "warmup"} }
func cooldownStep() stepType { return stepType{ID: stepTypeCooldown, Key: "cooldown"} }
func intervalStep() stepType { return stepType{ID: stepTypeInterval, Key: "interval"} }
func recoveryStep() stepType { return stepType{ID: stepTypeRecovery, Key: "recovery"} }
func restStep() stepType     { return stepType{ID: stepTypeRest, Key: "rest"} }

// An executableStep is one performed step of a built workout.
//
// A heart-rate target is either a named zone or an explicit bpm range, never both:
// Garmin silently discards the range when a zone is also present, so the builder
// sets one or the other and this type keeps both optional.
type executableStep struct {
	Type              string       `json:"type"`
	StepOrder         int          `json:"stepOrder"`
	StepType          stepType     `json:"stepType"`
	EndCondition      endCondition `json:"endCondition"`
	EndConditionValue float64      `json:"endConditionValue"`
	TargetType        targetType   `json:"targetType"`
	ZoneNumber        *int         `json:"zoneNumber,omitempty"`
	TargetValueOne    *float64     `json:"targetValueOne,omitempty"`
	TargetValueTwo    *float64     `json:"targetValueTwo,omitempty"`
	Description       string       `json:"description,omitempty"`
	Category          string       `json:"category,omitempty"`
	ExerciseName      string       `json:"exerciseName,omitempty"`
}

// A repeatGroup repeats its steps a fixed number of times.
//
// The iteration end condition is always written with its numeric id: omitting the id
// makes Garmin silently corrupt the repeat count.
type repeatGroup struct {
	Type               string       `json:"type"`
	StepOrder          int          `json:"stepOrder"`
	NumberOfIterations int          `json:"numberOfIterations"`
	EndCondition       endCondition `json:"endCondition"`
	WorkoutSteps       []any        `json:"workoutSteps"`
}

// A workoutBuilder composes a Garmin workout document step by step.
//
// It is the whole builder surface: a caller adds steps in order and the builder
// assigns the step order, so two steps can never claim the same position.
type workoutBuilder struct {
	name  string
	sport sportType
	steps []any
	order int
}

// newWorkoutBuilder starts a document for one sport.
func newWorkoutBuilder(name string, sport sportType) *workoutBuilder {
	return &workoutBuilder{name: name, sport: sport}
}

// next returns the next step order.
func (b *workoutBuilder) next() int {
	b.order++
	return b.order
}

// addStep appends one performed step.
func (b *workoutBuilder) addStep(step executableStep) {
	step.Type = executableStepDTO
	step.StepOrder = b.next()
	b.steps = append(b.steps, step)
}

// addRepeat appends a repeat group built by fill, which receives a nested builder
// sharing this builder's step ordering.
func (b *workoutBuilder) addRepeat(iterations int, fill func(*workoutBuilder)) {
	group := repeatGroup{
		Type:               repeatGroupDTO,
		StepOrder:          b.next(),
		NumberOfIterations: iterations,
		EndCondition:       iterationsCondition(),
	}
	nested := &workoutBuilder{order: b.order}
	fill(nested)
	b.order = nested.order
	group.WorkoutSteps = nested.steps
	b.steps = append(b.steps, group)
}

// document renders the built workout and validates it through the API layer, so the
// same bounds a caller-supplied document faces apply to a built one.
func (b *workoutBuilder) document() (api.WorkoutDocument, error) {
	body, err := json.Marshal(map[string]any{
		"workoutName": b.name,
		"sportType":   b.sport,
		"workoutSegments": []any{map[string]any{
			"segmentOrder": 1,
			"sportType":    b.sport,
			"workoutSteps": b.steps,
		}},
	})
	if err != nil {
		return api.WorkoutDocument{}, fail(err)
	}
	document, err := api.ParseWorkoutDocument(body)
	if err != nil {
		return api.WorkoutDocument{}, fail(err)
	}
	return document, nil
}

// timedStep builds one time-bounded step with no target.
func timedStep(kind stepType, seconds float64) executableStep {
	return executableStep{
		StepType:          kind,
		EndCondition:      timeCondition(),
		EndConditionValue: seconds,
		TargetType:        noTarget(),
	}
}

// zonedStep builds one time-bounded step targeting a named heart-rate zone.
func zonedStep(kind stepType, seconds float64, zone int) executableStep {
	step := timedStep(kind, seconds)
	step.TargetType = heartRateTarget()
	step.ZoneNumber = &zone
	return step
}

// rangedStep builds one time-bounded step targeting an explicit bpm range.
func rangedStep(kind stepType, seconds, low, high float64) executableStep {
	step := timedStep(kind, seconds)
	step.TargetType = heartRateTarget()
	step.TargetValueOne = &low
	step.TargetValueTwo = &high
	return step
}

// parseHeartRateZone validates a named zone argument, Z1 to Z5.
func parseHeartRateZone(value string) (int, error) {
	zone := optionalTextArg(value, defaultHeartRateZone)
	if len(zone) != 2 || zone[:1] != heartRateZonePrefix {
		return 0, invalidArgument("hr_zone must be Z1, Z2, Z3, Z4 or Z5")
	}
	number, err := strconv.Atoi(zone[1:])
	if err != nil || number < 1 || number > heartRateZoneCount {
		return 0, invalidArgument("hr_zone must be Z1, Z2, Z3, Z4 or Z5")
	}
	return number, nil
}

// heartRateZoneEnum renders the named zones a builder accepts.
func heartRateZoneEnum() []any {
	out := make([]any, 0, heartRateZoneCount)
	for zone := 1; zone <= heartRateZoneCount; zone++ {
		out = append(out, heartRateZonePrefix+strconv.Itoa(zone))
	}
	return out
}

// parseHeartRateRange validates the optional explicit bpm range. Both bounds are
// given together or neither is, because half a range is not a target.
func parseHeartRateRange(low, high *int) (float64, float64, bool, error) {
	switch {
	case low == nil && high == nil:
		return 0, 0, false, nil
	case low == nil || high == nil:
		return 0, 0, false, invalidArgument("hr_min and hr_max must be given together")
	}
	if err := inRange(argNameHRMin, float64(*low), minHeartRate, maxHeartRate); err != nil {
		return 0, 0, false, err
	}
	if err := inRange(argNameHRMax, float64(*high), minHeartRate, maxHeartRate); err != nil {
		return 0, 0, false, err
	}
	if *low >= *high {
		return 0, 0, false, invalidArgument("hr_min must be below hr_max")
	}
	return float64(*low), float64(*high), true, nil
}
