package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolCreateRunIntervalWorkout is the upstream compatibility name of the interval
// run builder.
const ToolCreateRunIntervalWorkout = "create_run_interval_workout"

// defaultIntervalHeartRateZone is the work-interval zone when no range is given.
// Source: workout_builders.py:641.
const defaultIntervalHeartRateZone = "Z4"

// Argument names of the interval builder.
const (
	argNameRepSeconds      = "rep_seconds"
	argNameRecoverySeconds = "recovery_seconds"
	argPrefixRecovery      = "recovery_"
)

// createRunIntervalWorkoutInput is the interval run builder argument set.
type createRunIntervalWorkoutInput struct {
	Name            string `json:"name" jsonschema:"the workout name"`
	Repeats         int    `json:"repeats" jsonschema:"how many work intervals"`
	RepSeconds      int    `json:"rep_seconds" jsonschema:"each work interval in seconds"`
	RecoverySeconds int    `json:"recovery_seconds" jsonschema:"each recovery jog in seconds, 0 for none"`
	WarmupMin       int    `json:"warmup_min" jsonschema:"the warmup in minutes"`
	CooldownMin     int    `json:"cooldown_min" jsonschema:"the cooldown in minutes"`
	HRZone          string `json:"hr_zone" jsonschema:"the work-interval heart-rate zone"`
	HRMin           *int   `json:"hr_min" jsonschema:"the lower bpm bound of the work intervals"`
	HRMax           *int   `json:"hr_max" jsonschema:"the upper bpm bound of the work intervals"`
	RecoveryHRMin   *int   `json:"recovery_hr_min" jsonschema:"the lower bpm bound of the recovery jogs"`
	RecoveryHRMax   *int   `json:"recovery_hr_max" jsonschema:"the upper bpm bound of the recovery jogs"`
}

func createRunIntervalWorkoutContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolCreateRunIntervalWorkout,
			Title: "Create an interval run workout",
			Description: "build warmup, then repeats of a work interval and a recovery jog, " +
				"then cooldown, and upload it. The work intervals target a named heart-rate " +
				"zone unless hr_min and hr_max are both given, which targets that exact bpm " +
				"range instead. The recovery jogs carry no target unless recovery_hr_min and " +
				"recovery_hr_max are both given. It creates a new workout every time it is called",
			Tier:        policy.TierWrite,
			Category:    categoryHealth,
			Annotations: writeAnnotations(false),
		},
		Schema: NewSchema(
			builderName(),
			repeatsProperty("how many work intervals the workout repeats"),
			intervalSeconds(argNameRepSeconds, "the duration of each work interval in seconds"),
			Property{
				Name:        argNameRecoverySeconds,
				Types:       []string{typeInteger},
				Description: "the duration of each recovery jog in seconds; 0 omits the jog",
				Minimum:     bound(0),
				Maximum:     bound(maxIntervalSeconds),
				Required:    true,
			},
			blockMinutes(argNameWarmupMin, "the warmup duration in minutes"),
			blockMinutes(argNameCooldownMin, "the cooldown duration in minutes"),
			heartRateZoneProperty(defaultIntervalHeartRateZone),
			heartRateBoundProperty(argNameHRMin,
				"the lower bpm bound of the work intervals; it must be given with hr_max", false),
			heartRateBoundProperty(argNameHRMax,
				"the upper bpm bound of the work intervals; it must be given with hr_min", false),
			heartRateBoundProperty(argPrefixRecovery+argNameHRMin,
				"the lower bpm bound of the recovery jogs; it must be given with recovery_hr_max", false),
			heartRateBoundProperty(argPrefixRecovery+argNameHRMax,
				"the upper bpm bound of the recovery jogs; it must be given with recovery_hr_min", false),
		),
	}
}

func registerCreateRunIntervalWorkout(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in createRunIntervalWorkoutInput) (
		*mcp.CallToolResult, SavedWorkoutResult, error,
	) {
		document, err := buildRunIntervalWorkout(in)
		if err != nil {
			return nil, SavedWorkoutResult{}, err
		}
		return svc.uploadBuilt(ctx, document)
	}
	return mcpserver.AddTool(registry, createRunIntervalWorkoutContract().Registration(), handler)
}

// buildRunIntervalWorkout composes warmup, the repeat group and cooldown.
// Source: build_run_interval_json (workout_builders.py:135-283).
func buildRunIntervalWorkout(in createRunIntervalWorkoutInput) (api.WorkoutDocument, error) {
	name, err := parseRequiredText(argNameName, in.Name, maxNameArgumentLen)
	if err != nil {
		return api.WorkoutDocument{}, err
	}
	if err := validateRunIntervalBounds(in); err != nil {
		return api.WorkoutDocument{}, err
	}
	work, err := heartRateStep(float64(in.RepSeconds), in.HRZone, defaultIntervalHeartRateZone,
		in.HRMin, in.HRMax)
	if err != nil {
		return api.WorkoutDocument{}, err
	}
	low, high, recoveryRange, err := parseHeartRateRange(argPrefixRecovery, in.RecoveryHRMin, in.RecoveryHRMax)
	if err != nil {
		return api.WorkoutDocument{}, err
	}

	builder := newWorkoutBuilder(name, runningSport())
	addBlock(builder, warmupStep(), in.WarmupMin)
	builder.addRepeat(in.Repeats, func(nested *workoutBuilder) {
		nested.addStep(work)
		if in.RecoverySeconds > 0 {
			seconds := float64(in.RecoverySeconds)
			if recoveryRange {
				nested.addStep(rangedStep(recoveryStep(), seconds, low, high))
			} else {
				nested.addStep(timedStep(recoveryStep(), seconds))
			}
		}
	})
	addBlock(builder, cooldownStep(), in.CooldownMin)
	return builder.document()
}

// validateRunIntervalBounds checks every numeric argument against its declared bound.
func validateRunIntervalBounds(in createRunIntervalWorkoutInput) error {
	return checkBounds([]boundCheck{
		{argNameRepeats, float64(in.Repeats), 1, maxRepeats},
		{argNameRepSeconds, float64(in.RepSeconds), 1, maxIntervalSeconds},
		{argNameRecoverySeconds, float64(in.RecoverySeconds), 0, maxIntervalSeconds},
		{argNameWarmupMin, float64(in.WarmupMin), 0, maxBlockMinutes},
		{argNameCooldownMin, float64(in.CooldownMin), 0, maxBlockMinutes},
	})
}
