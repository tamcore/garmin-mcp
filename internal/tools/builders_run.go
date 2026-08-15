package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The upstream compatibility names of the run and walk builders.
const (
	ToolCreateWalkRunWorkout = "create_walk_run_workout"
	ToolCreateRunWorkout     = "create_run_workout"
	ToolCreateZ2WalkWorkout  = "create_z2_walk_workout"
)

// secondsPerMinute converts the minutes a caller thinks in into the seconds Garmin
// stores. The conversion happens once, here, rather than in every builder.
const secondsPerMinute = 60

// builderName declares the workout-name argument every builder takes.
func builderName() Property {
	return Property{
		Name:        argNameName,
		Types:       []string{typeString},
		Description: "the workout name",
		MaxLength:   new(maxNameArgumentLen),
		Required:    true,
	}
}

// blockMinutes declares a warmup, cooldown or steady block in whole minutes.
func blockMinutes(name, description string) Property {
	return Property{
		Name:        name,
		Types:       []string{typeInteger},
		Description: description,
		Minimum:     bound(0),
		Maximum:     bound(maxBlockMinutes),
		Required:    true,
	}
}

// intervalSeconds declares one interval in whole seconds.
func intervalSeconds(name, description string) Property {
	return Property{
		Name:        name,
		Types:       []string{typeInteger},
		Description: description,
		Minimum:     bound(1),
		Maximum:     bound(maxIntervalSeconds),
		Required:    true,
	}
}

// heartRateZoneProperty declares the optional named-zone target.
func heartRateZoneProperty() Property {
	zoneKeyLen := 2
	return Property{
		Name:        "hr_zone",
		Types:       []string{typeString},
		Description: "the target heart-rate zone",
		Enum:        heartRateZoneEnum(),
		MaxLength:   &zoneKeyLen,
		Default:     defaultHeartRateZone,
	}
}

// heartRateBoundProperty declares one end of an explicit bpm target range.
func heartRateBoundProperty(name, description string, required bool) Property {
	return Property{
		Name:        name,
		Types:       []string{typeInteger},
		Description: description,
		Minimum:     bound(minHeartRate),
		Maximum:     bound(maxHeartRate),
		Required:    required,
	}
}

// createWalkRunWorkoutInput is the walk/run interval builder argument set.
type createWalkRunWorkoutInput struct {
	Name        string `json:"name" jsonschema:"the workout name"`
	RunSeconds  int    `json:"run_seconds" jsonschema:"each run interval in seconds"`
	WalkSeconds int    `json:"walk_seconds" jsonschema:"each walk interval in seconds"`
	Repeats     int    `json:"repeats" jsonschema:"how many run and walk pairs"`
	WarmupMin   int    `json:"warmup_min" jsonschema:"the warmup in minutes"`
	CooldownMin int    `json:"cooldown_min" jsonschema:"the cooldown in minutes"`
	HRZone      string `json:"hr_zone" jsonschema:"the target heart-rate zone"`
}

func createWalkRunWorkoutContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolCreateWalkRunWorkout,
			Title: "Create a walk/run workout",
			Description: "build a walk/run interval workout and upload it. It creates a new " +
				"workout every time it is called, so a repeat after a transport failure " +
				"creates a second workout",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(
			builderName(),
			intervalSeconds(argNameRunSeconds, "the duration of each run interval in seconds"),
			intervalSeconds("walk_seconds", "the duration of each walk interval in seconds"),
			Property{
				Name:        "repeats",
				Types:       []string{typeInteger},
				Description: "how many run and walk pairs the workout repeats",
				Minimum:     bound(1),
				Maximum:     bound(maxRepeats),
				Required:    true,
			},
			blockMinutes(argNameWarmupMin, "the warmup duration in minutes"),
			blockMinutes(argNameCooldownMin, "the cooldown duration in minutes"),
			heartRateZoneProperty(),
		),
	}
}

func registerCreateWalkRunWorkout(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in createWalkRunWorkoutInput) (
		*mcp.CallToolResult, SavedWorkoutResult, error,
	) {
		document, err := buildWalkRunWorkout(in)
		if err != nil {
			return nil, SavedWorkoutResult{}, err
		}
		return svc.uploadBuilt(ctx, document)
	}
	return mcpserver.AddTool(registry, createWalkRunWorkoutContract().Registration(), handler)
}

// buildWalkRunWorkout composes the walk/run document.
func buildWalkRunWorkout(in createWalkRunWorkoutInput) (api.WorkoutDocument, error) {
	name, err := parseRequiredText(argNameName, in.Name, maxNameArgumentLen)
	if err != nil {
		return api.WorkoutDocument{}, err
	}
	if err := validateWalkRunBounds(in); err != nil {
		return api.WorkoutDocument{}, err
	}
	zone, err := parseHeartRateZone(in.HRZone)
	if err != nil {
		return api.WorkoutDocument{}, err
	}

	builder := newWorkoutBuilder(name, runningSport())
	addBlock(builder, warmupStep(), in.WarmupMin)
	builder.addRepeat(in.Repeats, func(nested *workoutBuilder) {
		nested.addStep(zonedStep(intervalStep(), float64(in.RunSeconds), zone))
		nested.addStep(timedStep(recoveryStep(), float64(in.WalkSeconds)))
	})
	addBlock(builder, cooldownStep(), in.CooldownMin)
	return builder.document()
}

// validateWalkRunBounds checks every numeric argument against its declared bound.
func validateWalkRunBounds(in createWalkRunWorkoutInput) error {
	checks := []struct {
		field     string
		value     float64
		low, high float64
	}{
		{argNameRunSeconds, float64(in.RunSeconds), 1, maxIntervalSeconds},
		{"walk_seconds", float64(in.WalkSeconds), 1, maxIntervalSeconds},
		{"repeats", float64(in.Repeats), 1, maxRepeats},
		{argNameWarmupMin, float64(in.WarmupMin), 0, maxBlockMinutes},
		{argNameCooldownMin, float64(in.CooldownMin), 0, maxBlockMinutes},
	}
	for _, check := range checks {
		if err := inRange(check.field, check.value, check.low, check.high); err != nil {
			return err
		}
	}
	return nil
}

// addBlock appends a warmup or cooldown block, skipping a zero-length one rather than
// writing a step Garmin would reject.
func addBlock(builder *workoutBuilder, kind stepType, minutes int) {
	if minutes <= 0 {
		return
	}
	builder.addStep(timedStep(kind, float64(minutes*secondsPerMinute)))
}

// createRunWorkoutInput is the continuous-run builder argument set.
type createRunWorkoutInput struct {
	Name        string `json:"name" jsonschema:"the workout name"`
	RunSeconds  int    `json:"run_seconds" jsonschema:"the run duration in seconds"`
	WarmupMin   int    `json:"warmup_min" jsonschema:"the warmup in minutes"`
	CooldownMin int    `json:"cooldown_min" jsonschema:"the cooldown in minutes"`
	HRZone      string `json:"hr_zone" jsonschema:"the target heart-rate zone"`
	HRMin       *int   `json:"hr_min" jsonschema:"the lower bpm bound of an explicit target range"`
	HRMax       *int   `json:"hr_max" jsonschema:"the upper bpm bound of an explicit target range"`
}

func createRunWorkoutContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolCreateRunWorkout,
			Title: "Create a continuous run workout",
			Description: "build a single uninterrupted run with a warmup and a cooldown, and " +
				"upload it. It targets a named heart-rate zone unless hr_min and hr_max are " +
				"both given, which targets that exact bpm range instead. It creates a new " +
				"workout every time it is called",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(
			builderName(),
			intervalSeconds(argNameRunSeconds, "the duration of the run in seconds"),
			blockMinutes(argNameWarmupMin, "the warmup walk duration in minutes"),
			blockMinutes(argNameCooldownMin, "the cooldown walk duration in minutes"),
			heartRateZoneProperty(),
			heartRateBoundProperty(argNameHRMin,
				"the lower bpm bound; it must be given with hr_max", false),
			heartRateBoundProperty(argNameHRMax,
				"the upper bpm bound; it must be given with hr_min", false),
		),
	}
}

func registerCreateRunWorkout(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in createRunWorkoutInput) (
		*mcp.CallToolResult, SavedWorkoutResult, error,
	) {
		document, err := buildRunWorkout(in)
		if err != nil {
			return nil, SavedWorkoutResult{}, err
		}
		return svc.uploadBuilt(ctx, document)
	}
	return mcpserver.AddTool(registry, createRunWorkoutContract().Registration(), handler)
}

// buildRunWorkout composes the continuous-run document.
func buildRunWorkout(in createRunWorkoutInput) (api.WorkoutDocument, error) {
	name, err := parseRequiredText(argNameName, in.Name, maxNameArgumentLen)
	if err != nil {
		return api.WorkoutDocument{}, err
	}
	if err := validateRunBounds(in); err != nil {
		return api.WorkoutDocument{}, err
	}
	run, err := runTargetStep(in)
	if err != nil {
		return api.WorkoutDocument{}, err
	}

	builder := newWorkoutBuilder(name, runningSport())
	addBlock(builder, warmupStep(), in.WarmupMin)
	builder.addStep(run)
	addBlock(builder, cooldownStep(), in.CooldownMin)
	return builder.document()
}

// validateRunBounds checks the continuous-run durations against their bounds.
func validateRunBounds(in createRunWorkoutInput) error {
	if err := inRange(argNameRunSeconds, float64(in.RunSeconds), 1, maxIntervalSeconds); err != nil {
		return err
	}
	if err := inRange(argNameWarmupMin, float64(in.WarmupMin), 0, maxBlockMinutes); err != nil {
		return err
	}
	return inRange(argNameCooldownMin, float64(in.CooldownMin), 0, maxBlockMinutes)
}

// runTargetStep picks the explicit bpm range when both bounds are given, and the
// named zone otherwise. Garmin discards a range that arrives beside a zone, so only
// one of the two is ever written.
func runTargetStep(in createRunWorkoutInput) (executableStep, error) {
	low, high, explicit, err := parseHeartRateRange(in.HRMin, in.HRMax)
	if err != nil {
		return executableStep{}, err
	}
	if explicit {
		return rangedStep(intervalStep(), float64(in.RunSeconds), low, high), nil
	}
	zone, err := parseHeartRateZone(in.HRZone)
	if err != nil {
		return executableStep{}, err
	}
	return zonedStep(intervalStep(), float64(in.RunSeconds), zone), nil
}

// createZ2WalkWorkoutInput is the steady-walk builder argument set.
type createZ2WalkWorkoutInput struct {
	Name        string `json:"name" jsonschema:"the workout name"`
	DurationMin int    `json:"duration_min" jsonschema:"the walking block in minutes"`
	HRMin       int    `json:"hr_min" jsonschema:"the lower bpm bound of the target range"`
	HRMax       int    `json:"hr_max" jsonschema:"the upper bpm bound of the target range"`
}

func createZ2WalkWorkoutContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolCreateZ2WalkWorkout,
			Title: "Create a steady walk workout",
			Description: "build a single steady walking block targeting an explicit bpm " +
				"range, and upload it. It creates a new workout every time it is called",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(
			builderName(),
			blockMinutes("duration_min", "the walking block duration in minutes"),
			heartRateBoundProperty(argNameHRMin, "the lower bpm bound of the target range", true),
			heartRateBoundProperty(argNameHRMax, "the upper bpm bound of the target range", true),
		),
	}
}

func registerCreateZ2WalkWorkout(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in createZ2WalkWorkoutInput) (
		*mcp.CallToolResult, SavedWorkoutResult, error,
	) {
		document, err := buildZ2WalkWorkout(in)
		if err != nil {
			return nil, SavedWorkoutResult{}, err
		}
		return svc.uploadBuilt(ctx, document)
	}
	return mcpserver.AddTool(registry, createZ2WalkWorkoutContract().Registration(), handler)
}

// buildZ2WalkWorkout composes the steady-walk document.
func buildZ2WalkWorkout(in createZ2WalkWorkoutInput) (api.WorkoutDocument, error) {
	name, err := parseRequiredText(argNameName, in.Name, maxNameArgumentLen)
	if err != nil {
		return api.WorkoutDocument{}, err
	}
	if err := inRange("duration_min", float64(in.DurationMin), 1, maxBlockMinutes); err != nil {
		return api.WorkoutDocument{}, err
	}
	low, high, _, err := parseHeartRateRange(&in.HRMin, &in.HRMax)
	if err != nil {
		return api.WorkoutDocument{}, err
	}

	builder := newWorkoutBuilder(name, walkingSport())
	builder.addStep(rangedStep(
		intervalStep(), float64(in.DurationMin*secondsPerMinute), low, high))
	return builder.document()
}

// uploadBuilt uploads a document a builder composed and reports what Garmin saved.
func (s *service) uploadBuilt(ctx context.Context, document api.WorkoutDocument) (
	*mcp.CallToolResult, SavedWorkoutResult, error,
) {
	session, err := s.session(ctx)
	if err != nil {
		return nil, SavedWorkoutResult{}, err
	}
	saved, err := s.workouts.Upload(ctx, session, document)
	if err != nil {
		return nil, SavedWorkoutResult{}, fail(err)
	}
	result, err := newSavedWorkoutResult(saved)
	return nil, result, err
}
