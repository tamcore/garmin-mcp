package tools_test

import (
	"maps"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// intervalArgs is a three-rep session with a jog between reps, plus overrides.
func intervalArgs(overrides map[string]any) map[string]any {
	args := map[string]any{
		argName:            "Threshold 3x6",
		argRepeats:         3,
		"rep_seconds":      360,
		"recovery_seconds": 180,
		argWarmupMin:       12,
		argCooldownMin:     8,
	}
	maps.Copy(args, overrides)
	return args
}

// repeatChildren returns the steps inside the built repeat group.
func repeatChildren(t *testing.T, steps []any) []any {
	t.Helper()

	if len(steps) != 3 {
		t.Fatalf("the built workout has %d top-level steps, want warmup, repeat, cooldown", len(steps))
	}
	group, _ := steps[1].(map[string]any)
	if group["type"] != "RepeatGroupDTO" || group["numberOfIterations"] != float64(3) {
		t.Fatalf("the middle step = %v, want a three-iteration RepeatGroupDTO", group)
	}
	children, _ := group["workoutSteps"].([]any)
	return children
}

func TestCreateRunIntervalWorkoutBuildsWorkAndJogInsideTheRepeat(t *testing.T) {
	h := newWriteHarness(t, builderScript(), enabledWrites())

	h.call(t, tools.ToolCreateRunIntervalWorkout, intervalArgs(nil))

	steps := uploadedSteps(t, h)
	children := repeatChildren(t, steps)
	if len(children) != 2 {
		t.Fatalf("the repeat holds %d steps, want work and recovery", len(children))
	}
	work, _ := children[0].(map[string]any)
	if got := work["zoneNumber"]; got != float64(4) {
		t.Errorf("zoneNumber = %v, want the declared Z4 default", got)
	}
	jog, _ := children[1].(map[string]any)
	stepType, _ := jog["stepType"].(map[string]any)
	target, _ := jog["targetType"].(map[string]any)
	if stepType["stepTypeKey"] != "recovery" || target["workoutTargetTypeKey"] != "no.target" {
		t.Errorf("the jog = %v, want an untargeted recovery step", jog)
	}
	if jog["endConditionValue"] != float64(180) {
		t.Errorf("the jog lasts %v, want 180 seconds", jog["endConditionValue"])
	}
	cooldown, _ := steps[2].(map[string]any)
	if cooldown["stepOrder"] != float64(5) {
		t.Errorf("the cooldown stepOrder = %v, want 5 after the repeat's two children", cooldown["stepOrder"])
	}
}

func TestCreateRunIntervalWorkoutTargetsExplicitRanges(t *testing.T) {
	h := newWriteHarness(t, builderScript(), enabledWrites())

	h.call(t, tools.ToolCreateRunIntervalWorkout, intervalArgs(map[string]any{
		argHRMin: 172, argHRMax: 180, "recovery_hr_min": 120, "recovery_hr_max": 140,
	}))

	children := repeatChildren(t, uploadedSteps(t, h))
	for index, want := range [][2]float64{{172, 180}, {120, 140}} {
		step, _ := children[index].(map[string]any)
		if _, present := step["zoneNumber"]; present {
			t.Errorf("step %d carries a zoneNumber beside a range", index)
		}
		if step["targetValueOne"] != want[0] || step["targetValueTwo"] != want[1] {
			t.Errorf("step %d range = %v-%v, want %v", index,
				step["targetValueOne"], step["targetValueTwo"], want)
		}
	}
}

func TestCreateRunIntervalWorkoutOmitsTheJogWhenRecoveryIsZero(t *testing.T) {
	h := newWriteHarness(t, builderScript(), enabledWrites())

	h.call(t, tools.ToolCreateRunIntervalWorkout, intervalArgs(map[string]any{"recovery_seconds": 0}))

	if children := repeatChildren(t, uploadedSteps(t, h)); len(children) != 1 {
		t.Errorf("the repeat holds %d steps, want only the work interval", len(children))
	}
}

func TestCreateRunIntervalWorkoutRefusesHalfARecoveryRange(t *testing.T) {
	h := newWriteHarness(t, builderScript(), enabledWrites())

	h.callError(t, tools.ToolCreateRunIntervalWorkout, intervalArgs(map[string]any{"recovery_hr_min": 120}))

	for _, recorded := range h.recordedMethods() {
		if recorded == http.MethodPost+" "+client.PathWorkoutPrefix {
			t.Errorf("half a range still reached an upload: %v", h.recordedMethods())
		}
	}
}
