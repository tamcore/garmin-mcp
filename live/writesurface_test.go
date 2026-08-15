//go:build garminlive

package live

import (
	"slices"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/tools"
)

// The remaining builders' parameters. Every figure is arbitrary and bounded.
const (
	builderWalkSeconds = 120
	builderRepeats     = 2
	builderDurationMin = 20
	builderHRMin       = 100
	builderHRMax       = 130
	builderSets        = 3
	builderReps        = 10
	builderRestSeconds = 60
)

// TestLiveRemainingWorkoutBuildersUpload drives the three builders the workout
// lifecycle does not.
//
// Each one composes a different document shape and uploads it, so a Garmin change
// that rejects one shape while accepting another is caught. Each workout is read back
// and removed.
func TestLiveRemainingWorkoutBuildersUpload(t *testing.T) {
	w := liveWriteEnv(t)

	for _, builder := range remainingBuilders() {
		t.Run(builder.tool, func(t *testing.T) {
			name := w.names.name(builder.label)
			created := w.call(t, builder.tool, merged(builder.args, keyName, name))
			id := identifier(t, created, builder.tool, argWorkoutID)
			w.keepClean(t, kindWorkout, id)

			detail := w.call(t, tools.ToolGetWorkoutByID, map[string]any{argWorkoutID: id})
			assertSuiteValue(t, tools.ToolGetWorkoutByID, keyName, name, detail)
			if segments, present := detail["segments"]; !present || segments == nil {
				t.Errorf("%s uploaded a workout Garmin stored without segments", builder.tool)
			}

			w.deleteViaTool(t, tools.ToolDeleteWorkout, argWorkoutID, kindWorkout, id,
				w.workoutGone(t, id))
		})
	}
}

// builderCall is one builder tool and the arguments it needs beside its name.
type builderCall struct {
	tool  string
	label nameLabel
	args  map[string]any
}

// remainingBuilders are the builders TestLiveWorkoutLifecycle does not drive.
func remainingBuilders() []builderCall {
	return []builderCall{
		{tools.ToolCreateWalkRunWorkout, labelNameWalkRun, map[string]any{
			keyRunSeconds:  workoutRunSeconds,
			"walk_seconds": builderWalkSeconds,
			"repeats":      builderRepeats,
			keyWarmupMin:   workoutWarmupMin,
			keyCooldownMin: workoutCooldownMin,
		}},
		{tools.ToolCreateZ2WalkWorkout, labelNameZ2Walk, map[string]any{
			"duration_min": builderDurationMin,
			"hr_min":       builderHRMin,
			"hr_max":       builderHRMax,
		}},
		{tools.ToolCreateStrengthWorkout, labelNameStrengthWorkout, map[string]any{
			"exercises": []map[string]any{{
				keyName:        "BACK_SQUAT",
				"category":     "SQUAT",
				"sets":         builderSets,
				"reps":         builderReps,
				"rest_seconds": builderRestSeconds,
			}},
		}},
	}
}

// TestLiveGearLinkAndUnlinkOnACreatedActivity drives the two gear tools.
//
// The subject is an activity this suite created, and the gear is whatever the account
// already links to the activity the read half analyses. Linking changes the created
// activity's association only: the gear item itself is never written, and the link is
// removed again.
//
// An account that links no gear is a legitimate state and a skip.
func TestLiveGearLinkAndUnlinkOnACreatedActivity(t *testing.T) {
	w := liveWriteEnv(t)

	gear := w.someGearUUID(t)
	if gear == "" {
		t.Skip("not run — the account links no gear to the activity this suite reads, so no " +
			"gear identifier can be derived")
	}

	id := w.createPlainActivity(t, labelNameGear)
	arguments := map[string]any{argActivityID: id, keyGearUUID: gear}

	w.call(t, tools.ToolAddGearToActivity, arguments)
	if !w.activityHasGear(t, id, gear) {
		t.Errorf("%s reported success and the activity lists no gear",
			tools.ToolAddGearToActivity)
	}

	w.call(t, tools.ToolRemoveGearFromActivity, arguments)
	if w.activityHasGear(t, id, gear) {
		t.Errorf("%s reported success and the activity still lists the gear",
			tools.ToolRemoveGearFromActivity)
	}

	w.deleteViaTool(t, tools.ToolDeleteActivity, argActivityID, kindActivity, id,
		w.activityGone(t, id))
}

// someGearUUID reads a gear identifier the account already uses, or "" when it uses
// none.
//
// The identifier is never rendered. It comes from the activity the read half already
// selects, so no further part of the account is examined.
func (w *writeEnv) someGearUUID(t *testing.T) string {
	t.Helper()

	subject := analysedActivity(t)
	return w.linkedGear(t, subject.id.String(), "")
}

// activityHasGear reports whether one gear identifier is linked to one activity.
func (w *writeEnv) activityHasGear(t *testing.T, id int64, gear string) bool {
	t.Helper()

	return w.linkedGear(t, id, gear) != ""
}

// linkedGear returns the first gear identifier linked to one activity, or the one
// that matches want when want is set. It returns "" when there is no match.
func (w *writeEnv) linkedGear(t *testing.T, activity any, want string) string {
	t.Helper()

	linked := w.call(t, tools.ToolGetActivityGear, map[string]any{argActivityID: activity})
	items, _ := linked[keyGear].([]any)

	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		uuid, named := object[keyGearUUID].(string)
		if !named || uuid == "" {
			continue
		}
		if want == "" || uuid == want {
			return uuid
		}
	}
	return ""
}

// TestLiveDownloadActivityFileAnswersForEveryFormat drives the one write-tier tool
// whose effect is a read.
//
// download_activity_file sits in the write tier because it transfers a whole device
// file, but it mutates nothing, so it runs against the activity the read half
// analyses rather than against a created one: a manual activity has no device file to
// export.
func TestLiveDownloadActivityFileAnswersForEveryFormat(t *testing.T) {
	w := liveWriteEnv(t)
	subject := analysedActivity(t)

	for _, format := range []string{"fit", "tcx", "gpx"} {
		t.Run(format, func(t *testing.T) {
			result := w.call(t, tools.ToolDownloadActivityFile, map[string]any{
				argActivityID: subject.id.String(), keyFormat: format,
			})
			if got, _ := result[keyFormat].(string); got != format {
				t.Errorf("%s reported a different format than the one requested",
					tools.ToolDownloadActivityFile)
			}
			if size, _ := result[keyBytes].(float64); size <= 0 {
				t.Errorf("%s returned an empty %s export", tools.ToolDownloadActivityFile, format)
			}
			assertResultIsSafe(t, tools.ToolDownloadActivityFile, result)
		})
	}
}

// createPlainActivity creates one manual activity and puts it under cleanup.
func (w *writeEnv) createPlainActivity(t *testing.T, label nameLabel) int64 {
	t.Helper()

	date := time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)
	created := w.call(t, tools.ToolCreateManualActivity, map[string]any{
		keyTypeKey:         manualTypeKey,
		argDate:            date,
		"duration_minutes": manualDuration,
		keyActivityName:    w.names.name(label),
		keyTimeZone:        manualTimeZone,
	})
	id := identifier(t, created, tools.ToolCreateManualActivity, argActivityID)
	w.keepClean(t, kindActivity, id)
	return id
}

// TestEveryWriteAndDestructiveToolIsAccountedFor keeps the write coverage honest.
//
// It is the write analogue of TestEveryReadOnlyToolIsAccountedFor: a write or
// destructive tool that is neither driven by this suite nor listed with a reason
// fails here, so the surface cannot grow past the suite and the suite cannot decay
// into fewer but still-passing calls.
func TestEveryWriteAndDestructiveToolIsAccountedFor(t *testing.T) {
	registered := slices.Concat(tools.WriteTools(), tools.DestructiveTools())
	exercised := exercisedWrites()

	for _, tool := range registered {
		reason, excused := writesCoveredElsewhere[tool]
		switch {
		case slices.Contains(exercised, tool) && excused:
			t.Errorf("%s is both exercised and excused as %q: remove one", tool, reason)
		case !slices.Contains(exercised, tool) && !excused:
			t.Errorf("%s is registered as a write or destructive tool and is neither exercised "+
				"by this suite nor listed in writesCoveredElsewhere with a reason", tool)
		}
	}

	for tool := range writesCoveredElsewhere {
		if !slices.Contains(registered, tool) {
			t.Errorf("writesCoveredElsewhere names %q, which is no registered write or "+
				"destructive tool", tool)
		}
	}
	for _, tool := range exercised {
		if !slices.Contains(registered, tool) {
			t.Errorf("this suite drives %q, which is no registered write or destructive tool",
				tool)
		}
	}
}

// exercisedWrites names every write and destructive tool this suite drives against
// the live service.
func exercisedWrites() []string {
	return []string{
		tools.ToolCreateManualActivity,
		tools.ToolSetActivityName,
		tools.ToolSetActivityType,
		tools.ToolSetActivityEventType,
		tools.ToolSetActivityDescription,
		tools.ToolSetActivityFeel,
		tools.ToolSetPerceivedEffort,
		tools.ToolAddGearToActivity,
		tools.ToolRemoveGearFromActivity,
		tools.ToolCreateStrengthTrainingActivity,
		tools.ToolSetActivityStrengthExerciseSets,
		tools.ToolDeleteActivity,
		tools.ToolCreateRunWorkout,
		tools.ToolCreateWalkRunWorkout,
		tools.ToolCreateZ2WalkWorkout,
		tools.ToolCreateStrengthWorkout,
		tools.ToolUploadWorkouts,
		tools.ToolUpdateWorkout,
		tools.ToolScheduleWorkout,
		tools.ToolScheduleWorkouts,
		tools.ToolScheduleWeek,
		tools.ToolUnscheduleWorkout,
		tools.ToolUnscheduleWorkouts,
		tools.ToolDeleteWorkout,
		tools.ToolDeleteWorkouts,
		tools.ToolDownloadActivityFile,
	}
}

// writesCoveredElsewhere names the write tools this suite deliberately does not
// drive, with the reason.
var writesCoveredElsewhere = map[string]string{
	tools.ToolUploadWorkout: "upload_workouts sends the same document to the same endpoint " +
		"through the same api-layer method, and the batch form additionally proves the " +
		"per-item reporting the single form has none of",
}
