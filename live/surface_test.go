//go:build garminlive

package live

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// Sweep bounds.
const (
	// windowDays is the width of every date window the sweep asks for. It is
	// deliberately short: a wide window is a large response and this suite is a
	// guest on a private API.
	windowDays = 7

	// maxResultItems is the largest list any read-only tool may return. It is the
	// widest per-tool bound the tools package declares, so a list above it means a
	// bound stopped being applied.
	maxResultItems = 3000

	// curveActivities is how many device files the power-duration tool may
	// download during the sweep. One is enough to prove the path works.
	curveActivities = 1
)

// forbiddenKeys are the result keys that would mean this server leaked something a
// tool must never return: a coordinate, a credential or a raw payload.
var forbiddenKeys = []string{
	"lat", "lon", "lng", "latitude", "longitude",
	"startlatitude", "startlongitude", "endlatitude", "endlongitude",
	"position_lat", "position_long", "positionlat", "positionlong",
	"token", "access_token", "refresh_token", "jwt", "cookie", "authorization",
	"password", "raw", "raw_body", "payload",
}

// forbiddenValues are the substrings that would mean a credential or a header
// reached a result.
var forbiddenValues = []string{"Bearer ", "eyJ", "Set-Cookie", "oauth_token"}

// coveredElsewhere names the read-only tools the sweep deliberately does not call,
// with the reason. It exists so the sweep cannot shrink silently: a tool that is
// neither exercised nor listed here fails TestEveryReadOnlyToolIsAccountedFor.
var coveredElsewhere = map[string]string{
	mcpserver.ServerInfoToolName: "the server's own tool: it reaches no Garmin endpoint",
	tools.ToolGetActivityFITData: "exercised by TestToolResultsAgreeWithTheAPILayer, " +
		"which already downloads the device file once",
}

// Argument names, named once so a rename shows up in one place.
const (
	argActivityID = "activity_id"
	argWorkoutID  = "workout_id"
	argDate       = "date"
	argStartDate  = "start_date"
	argEndDate    = "end_date"
	argCalendar   = "calendar_date"
	argLimit      = "limit"
	argCount      = "num_activities"
)

// sweepCall is one tool the sweep drives, with the arguments it needs.
type sweepCall struct {
	tool string
	args map[string]any
}

// accountCalls are the tools that need no activity: they take no argument, or one
// derived from the clock. They run against any account, an empty one included.
func accountCalls() []sweepCall {
	today := time.Now().UTC()
	day := today.AddDate(0, 0, -1).Format(time.DateOnly)
	window := map[string]any{
		argStartDate: today.AddDate(0, 0, -windowDays).Format(time.DateOnly),
		argEndDate:   today.Format(time.DateOnly),
	}
	reference := map[string]any{argCalendar: today.Format(time.DateOnly)}

	return []sweepCall{
		{tools.ToolGetUserProfile, nil},
		{tools.ToolGetFullName, nil},
		{tools.ToolGetUnitSystem, nil},
		{tools.ToolGetUserProfileSettings, nil},
		{tools.ToolGetPersonalRecord, nil},
		{tools.ToolCountActivities, nil},
		{tools.ToolGetActivities, map[string]any{argLimit: 5}},
		{tools.ToolGetActivitiesByDate, window},
		{tools.ToolGetActivitiesForDate, map[string]any{argDate: day}},
		{tools.ToolGetActivityTypes, nil},
		{tools.ToolGetDevices, nil},
		{tools.ToolGetSleepData, map[string]any{argDate: day}},
		{tools.ToolGetUserSummary, map[string]any{argDate: day}},
		{tools.ToolGetExerciseTypes, nil},
		{tools.ToolGetWorkouts, nil},
		{tools.ToolGetScheduledWorkouts, window},
		{tools.ToolGetTrainingPlanWorkouts, reference},
		{tools.ToolGetGarminCoachWorkouts, reference},
		{tools.ToolGetPowerDurationCurve, map[string]any{argCount: curveActivities}},
	}
}

// activityCalls are the tools that need an activity identifier a prior read produced.
// They are skipped, with a reason, on an account that holds no analysable activity.
func activityCalls(activityID string) []sweepCall {
	activity := map[string]any{argActivityID: activityID}
	return []sweepCall{
		{tools.ToolGetActivity, activity},
		{tools.ToolGetActivityGear, activity},
		{tools.ToolGetActivityTypedSplits, activity},
		{tools.ToolGetActivitySplits, activity},
		{tools.ToolGetActivitySplitSummaries, activity},
		{tools.ToolGetActivityHRInZones, activity},
		{tools.ToolGetActivityPowerInZones, activity},
		{tools.ToolGetActivityWeather, activity},
		{tools.ToolGetActivityExerciseSets, activity},
	}
}

// derivedCalls names the tools whose argument comes from a prior read rather than
// from the clock. They are exercised by TestDerivedArgumentToolsAnswer.
func derivedCalls() []string {
	return []string{tools.ToolGetWorkoutByID, tools.ToolDownloadWorkout}
}

// TestReadOnlyToolSurfaceAnswersOverTheLiveAccount drives every account-scoped
// read-only tool against the real account and asserts, for each one, that it
// succeeded, that its result obeys the declared bounds and truncation flags, and that
// it carries no coordinate, credential or raw payload.
func TestReadOnlyToolSurfaceAnswersOverTheLiveAccount(t *testing.T) {
	e := liveEnv(t)

	for _, call := range accountCalls() {
		t.Run(call.tool, func(t *testing.T) { e.assertToolAnswers(t, call) })
	}
}

// TestActivityScopedToolsAnswerForOneActivity drives the tools that need an activity
// identifier. It is separate from the account sweep so an account with no analysable
// activity still proves the rest of the surface instead of skipping all of it.
func TestActivityScopedToolsAnswerForOneActivity(t *testing.T) {
	e := liveEnv(t)
	a := analysedActivity(t)

	for _, call := range activityCalls(a.id.String()) {
		t.Run(call.tool, func(t *testing.T) { e.assertToolAnswers(t, call) })
	}
}

// assertToolAnswers performs one call and checks the whole result.
func (e *env) assertToolAnswers(t *testing.T, call sweepCall) {
	t.Helper()

	result := e.call(t, call.tool, call.args)
	if len(result) == 0 {
		t.Fatalf("%s returned an empty result object", call.tool)
	}
	assertResultIsSafe(t, call.tool, result)
}

// TestDerivedArgumentToolsAnswer drives the two tools whose argument only exists once
// another read produced it.
func TestDerivedArgumentToolsAnswer(t *testing.T) {
	e := liveEnv(t)

	library := e.call(t, tools.ToolGetWorkouts, nil)
	entries, _ := library["workouts"].([]any)
	if len(entries) == 0 {
		t.Skip("not run — the account's workout library is empty, so no workout id can be derived")
	}
	first, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("%s returned a workout that is not an object", tools.ToolGetWorkouts)
	}
	id, ok := first[argWorkoutID].(float64)
	if !ok {
		t.Fatalf("%s returned a workout without a workout id", tools.ToolGetWorkouts)
	}

	for _, tool := range derivedCalls() {
		t.Run(tool, func(t *testing.T) {
			result := e.call(t, tool, map[string]any{argWorkoutID: int64(id)})
			assertResultIsSafe(t, tool, result)
		})
	}
}

// TestEveryReadOnlyToolIsAccountedFor keeps the sweep honest.
//
// A read-only tool that is neither driven above nor listed with a reason fails here,
// so the surface cannot grow past the suite and the suite cannot decay into fewer but
// still-passing calls.
func TestEveryReadOnlyToolIsAccountedFor(t *testing.T) {
	exercised := map[string]bool{}
	for _, call := range slices.Concat(accountCalls(), activityCalls("1")) {
		exercised[call.tool] = true
	}
	for _, tool := range derivedCalls() {
		exercised[tool] = true
	}

	registered := tools.ReadOnlyTools()
	for _, tool := range registered {
		reason, excused := coveredElsewhere[tool]
		switch {
		case exercised[tool] && excused:
			t.Errorf("%s is both exercised and excused as %q: remove one", tool, reason)
		case !exercised[tool] && !excused:
			t.Errorf("%s is registered read-only and is neither exercised by this suite "+
				"nor listed in coveredElsewhere with a reason", tool)
		}
	}

	for tool := range coveredElsewhere {
		if !slices.Contains(registered, tool) {
			t.Errorf("coveredElsewhere names %q, which is not a registered read-only tool", tool)
		}
	}
}

// assertResultIsSafe walks one result and fails on anything a tool must never return,
// on a list above the widest declared bound, and on a truncation flag that is not a
// boolean.
//
// It reports the offending key path and never the value, so a failure cannot print
// the coordinate or the credential it just found.
func assertResultIsSafe(t *testing.T, tool string, result map[string]any) {
	t.Helper()

	walk(result, "", func(path, key string, value any) {
		if key != "" && slices.Contains(forbiddenKeys, strings.ToLower(key)) {
			t.Errorf("%s returned the forbidden field %q at %s", tool, key, path)
		}
		if text, ok := value.(string); ok {
			for _, marker := range forbiddenValues {
				if strings.Contains(text, marker) {
					t.Errorf("%s returned a value at %s that looks like a credential", tool, path)
				}
			}
		}
		if items, ok := value.([]any); ok && len(items) > maxResultItems {
			t.Errorf("%s returned %d items at %s, above the %d bound: a bound is not applied",
				tool, len(items), path, maxResultItems)
		}
		if _, ok := value.(bool); isTruncationFlag(key) && !ok {
			t.Errorf("%s reports %s as something other than a boolean", tool, path)
		}
	})
}

// isTruncationFlag reports whether a key is one of the declared truncation flags.
func isTruncationFlag(key string) bool {
	return key == "truncated" || strings.HasSuffix(key, "_truncated")
}

// walk visits every key and value of a decoded JSON document, depth first, reporting
// the path it reached each one by.
func walk(value any, path string, visit func(path, key string, value any)) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			visit(childPath, key, child)
			walk(child, childPath, visit)
		}
	case []any:
		for index, child := range typed {
			childPath := fmt.Sprintf("%s[%d]", path, index)
			visit(childPath, "", child)
			walk(child, childPath, visit)
		}
	}
}
