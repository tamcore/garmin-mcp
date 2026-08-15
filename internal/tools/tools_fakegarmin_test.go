//go:build fakegarmin

package tools_test

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// TestWholeReadOnlyToolSurfaceAgainstOneFakeAccount drives every registered tool over
// one MCP session against one scripted account, which is the flow a real client
// performs. It is the integration counterpart of the per-tool unit tests: those check
// a mapping, this checks that the whole surface works together over the transport,
// the middleware chain and the request layer.
func TestWholeReadOnlyToolSurfaceAgainstOneFakeAccount(t *testing.T) {
	h := newHarness(t, wholeReadScript())

	activity := map[string]any{argActivityID: testActivityID}
	calls := []struct {
		tool string
		args map[string]any
	}{
		{tools.ToolGetUserProfile, nil},
		{tools.ToolGetFullName, nil},
		{tools.ToolGetUnitSystem, nil},
		{tools.ToolGetUserProfileSettings, nil},
		{tools.ToolGetPersonalRecord, nil},
		{tools.ToolGetActivities, map[string]any{argLimit: 2}},
		{tools.ToolGetActivitiesByDate, map[string]any{
			argStartDate: windowStart, argEndDate: testCalendarDate,
		}},
		{tools.ToolGetSleepData, map[string]any{argDate: testCalendarDate}},
		{tools.ToolGetUserSummary, map[string]any{argDate: testCalendarDate}},
		{tools.ToolGetDevices, nil},
		{tools.ToolGetActivityTypedSplits, activity},
		{tools.ToolGetActivityExerciseSets, activity},
		{tools.ToolGetActivitySplits, activity},
		{tools.ToolGetActivitySplitSummaries, activity},
		{tools.ToolGetActivityHRInZones, activity},
		{tools.ToolGetActivityPowerInZones, activity},
		{tools.ToolGetActivityWeather, activity},
		{tools.ToolGetExerciseTypes, nil},
		{tools.ToolGetWorkouts, nil},
		{tools.ToolGetWorkoutByID, map[string]any{argWorkoutID: testWorkoutID}},
		{tools.ToolDownloadWorkout, map[string]any{argWorkoutID: 550001}},
	}

	for _, call := range calls {
		t.Run(call.tool, func(t *testing.T) {
			if got := h.call(t, call.tool, call.args); len(got) == 0 {
				t.Errorf("%s returned an empty result", call.tool)
			}
		})
	}

	assertNoCredentialLeftThisProcess(t, h, len(calls))
}

// wholeReadScript serves every endpoint the read-only surface reaches.
func wholeReadScript() testkit.Script {
	script := readScript()
	for path, behavior := range map[string]testkit.Behavior{
		client.PathUserProfileSettings:                     testkit.JSON(http.StatusOK, profileSettingsBody),
		client.PathPersonalRecords + "/" + testDisplayName: testkit.JSON(http.StatusOK, personalRecordsBody),
		activityDetailPath(client.SegmentSplits):           testkit.JSON(http.StatusOK, `{"lapDTOs":[`+splitEntry+`]}`),
		activityDetailPath(client.SegmentSplitSummaries):   testkit.JSON(http.StatusOK, splitSummariesBody),
		activityDetailPath(client.SegmentHRInZones):        testkit.JSON(http.StatusOK, hrZonesBody),
		activityDetailPath(client.SegmentPowerInZones):     testkit.JSON(http.StatusOK, powerZonesBody),
		activityDetailPath(client.SegmentWeather):          testkit.JSON(http.StatusOK, weatherBody),
		client.PathWorkouts:                                testkit.JSON(http.StatusOK, workoutListBody),
		workoutPath(testWorkoutID):                         testkit.JSON(http.StatusOK, workoutDetailBody),
		client.PathWorkoutFITPrefix + "/" + testWorkoutID:  {Status: http.StatusOK, Body: workoutFITBody},
	} {
		script = script.With(path, behavior)
	}
	return script.With(client.PathUserSettings,
		repeat(testkit.JSON(http.StatusOK, settingsBody), 4)...)
}

// assertNoCredentialLeftThisProcess pins the boundary: this package never attaches a
// token. The caller owns the credential, and in these tests there is none.
func assertNoCredentialLeftThisProcess(t *testing.T, h harness, calls int) {
	t.Helper()

	recorded := h.fake.Requests()
	if len(recorded) < calls-1 {
		t.Fatalf("the fake received %d requests for %d calls, want at least one each",
			len(recorded), calls)
	}
	for _, request := range recorded {
		if request.Header.Get("Authorization") != "" {
			t.Error("a request carried an Authorization header: the caller owns the token")
		}
		if request.Method != http.MethodGet {
			t.Errorf("a read-only tool issued %s, want GET", request.Method)
		}
	}
}

// TestARateLimitedGarminBecomesAnActionableRefusal walks the whole path a 429 takes:
// the request layer classifies it, the domain client wraps it, and the tool turns it
// into advice a model can act on without seeing the response body.
func TestARateLimitedGarminBecomesAnActionableRefusal(t *testing.T) {
	script := testkit.NewScript().
		With(client.PathDevices, repeat(testkit.RateLimited(1), 4)...)
	h := newHarness(t, script)

	text := h.callError(t, tools.ToolGetDevices, nil)

	assertSanitized(t, text)
	if !containsFold(text, "rate-limited") {
		t.Errorf("the refusal %q does not name the rate limit", text)
	}
}
