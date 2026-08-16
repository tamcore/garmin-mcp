package tools_test

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

func TestGetActivitiesReturnsABoundedPage(t *testing.T) {
	h := newHarness(t, readScript())

	got := h.call(t, tools.ToolGetActivities, map[string]any{argStart: 0, argLimit: 2})

	activities, ok := got["activities"].([]any)
	if !ok {
		t.Fatalf("activities = %T, want an array", got["activities"])
	}
	if len(activities) != 2 {
		t.Fatalf("%d activities, want 2", len(activities))
	}
	if got["count"] != float64(2) {
		t.Errorf("count = %v, want 2", got["count"])
	}
	if got[argLimit] != float64(2) {
		t.Errorf("limit = %v, want the requested 2", got[argLimit])
	}

	first, _ := activities[0].(map[string]any)
	if first["activity_id"] != float64(9001) {
		t.Errorf("activity_id = %v, want 9001", first["activity_id"])
	}
	if first["activity_type"] != typeRunning {
		t.Errorf("activity_type = %v, want %q", first["activity_type"], typeRunning)
	}
	assertNoCoordinates(t, first)
}

// TestGetActivitiesCarriesTheStepAndElevationTrio proves the three figures upstream
// reports per listed activity reach this server's list result, and that an activity
// whose document omits them reports nothing rather than a zero. A swim counts no
// steps and an indoor ride records no altitude, so a zero would be a wrong reading
// rather than a missing one.
func TestGetActivitiesCarriesTheStepAndElevationTrio(t *testing.T) {
	h := newHarness(t, readScript())

	got := h.call(t, tools.ToolGetActivities, map[string]any{argStart: 0, argLimit: 2})
	activities, ok := got["activities"].([]any)
	if !ok || len(activities) != 2 {
		t.Fatalf("activities = %v, want two", got["activities"])
	}

	carried, _ := activities[0].(map[string]any)
	for key, want := range map[string]float64{
		"steps":                 8800,
		"elevation_gain_meters": 220,
		"elevation_loss_meters": 214,
	} {
		if carried[key] != want {
			t.Errorf("%s = %v, want %v", key, carried[key], want)
		}
	}

	bare, _ := activities[1].(map[string]any)
	for _, key := range []string{"steps", "elevation_gain_meters", "elevation_loss_meters"} {
		if _, present := bare[key]; present {
			t.Errorf("%s = %v for an activity without one, want the key absent", key, bare[key])
		}
	}
}

func TestGetActivitiesUsesTheManifestDefaults(t *testing.T) {
	h := newHarness(t, readScript())

	got := h.call(t, tools.ToolGetActivities, nil)

	if got["start"] != float64(0) {
		t.Errorf("start = %v, want the default 0", got["start"])
	}
	if got[argLimit] != float64(20) {
		t.Errorf("limit = %v, want the default 20", got[argLimit])
	}
}

func TestGetActivitiesClampsALimitAboveTheConfiguredPageBound(t *testing.T) {
	h := newHarnessWith(t, readScript(), tools.Bounds{}, client.Limits{MaxPageSize: 5})

	got := h.call(t, tools.ToolGetActivities, map[string]any{argLimit: 100})

	if got[argLimit] != float64(5) {
		t.Errorf("limit = %v, want it clamped to the configured 5", got[argLimit])
	}
}

func TestGetActivitiesRefusesAnOutOfRangeArgument(t *testing.T) {
	h := newHarness(t, readScript())

	cases := map[string]map[string]any{
		"negative start":   {argStart: -1},
		"zero limit":       {argLimit: 0},
		"limit over range": {argLimit: 100000},
		"unknown argument": {argUserID: "someone-else"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			assertSanitized(t, h.callError(t, tools.ToolGetActivities, args))
		})
	}
}

func TestGetActivitiesByDatePagesTheWindow(t *testing.T) {
	script := testkit.NewScript().
		With(client.PathActivitySearch,
			testkit.JSON(http.StatusOK, activityArray(9001, 9002, 9003)),
			testkit.JSON(http.StatusOK, `[]`))
	h := newHarnessWith(t, script, tools.Bounds{}, client.Limits{MaxPageSize: 3})

	got := h.call(t, tools.ToolGetActivitiesByDate, map[string]any{
		argStartDate: windowStart,
		argEndDate:   testCalendarDate,
		argPage:      0,
		argPageSize:  2,
	})

	activities, _ := got["activities"].([]any)
	if len(activities) != 2 {
		t.Fatalf("%d activities on page 0, want 2", len(activities))
	}
	if got["has_more"] != true {
		t.Errorf("has_more = %v, want true", got["has_more"])
	}
	if got["next_page"] != float64(1) {
		t.Errorf("next_page = %v, want 1", got["next_page"])
	}
	dateRange, _ := got["date_range"].(map[string]any)
	if dateRange[argStartDate] != windowStart || dateRange[argEndDate] != testCalendarDate {
		t.Errorf("date_range = %v, want the requested window", dateRange)
	}
}

func TestGetActivitiesByDateReportsTheLastPage(t *testing.T) {
	script := testkit.NewScript().
		With(client.PathActivitySearch,
			testkit.JSON(http.StatusOK, activityArray(9001, 9002, 9003)),
			testkit.JSON(http.StatusOK, `[]`))
	h := newHarnessWith(t, script, tools.Bounds{}, client.Limits{MaxPageSize: 3})

	got := h.call(t, tools.ToolGetActivitiesByDate, map[string]any{
		argStartDate: windowStart,
		argEndDate:   testCalendarDate,
		argPage:      1,
		argPageSize:  2,
	})

	if got["has_more"] != false {
		t.Errorf("has_more = %v, want false on the last page", got["has_more"])
	}
	if _, present := got["next_page"]; present {
		t.Errorf("next_page = %v, want it absent on the last page", got["next_page"])
	}
}

func TestGetActivitiesByDateFiltersByActivityType(t *testing.T) {
	h := newHarness(t, readScript())

	h.call(t, tools.ToolGetActivitiesByDate, map[string]any{
		argStartDate:    windowStart,
		argEndDate:      testCalendarDate,
		argActivityType: typeRunning,
	})

	recorded := h.fake.Requests()
	if len(recorded) == 0 {
		t.Fatal("the fake received nothing")
	}
	if got := recorded[0].Query.Get(client.QueryActivityType); got != typeRunning {
		t.Errorf("activityType query = %q, want %q", got, typeRunning)
	}
}

func TestGetActivitiesByDateRefusesAnInvalidWindow(t *testing.T) {
	h := newHarness(t, readScript())

	cases := map[string]map[string]any{
		"missing end date": {argStartDate: windowStart},
		"malformed date":   {argStartDate: "01-01-2026", argEndDate: testCalendarDate},
		"reversed window":  {argStartDate: "2026-02-01", argEndDate: windowStart},
		"window over the bound": {
			argStartDate: "2000-01-01", argEndDate: windowStart,
		},
		"bad activity type": {
			argStartDate: windowStart, argEndDate: "2026-01-02", argActivityType: "Run/../x",
		},
		"negative page": {
			argStartDate: windowStart, argEndDate: "2026-01-02", argPage: -1,
		},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			assertSanitized(t, h.callError(t, tools.ToolGetActivitiesByDate, args))
		})
	}
}

func TestGetActivitiesByDateRefusesAWindowResultOverItsBound(t *testing.T) {
	script := testkit.NewScript().
		With(client.PathActivitySearch,
			testkit.JSON(http.StatusOK, activityArray(9001, 9002, 9003)),
			testkit.JSON(http.StatusOK, `[]`))
	h := newHarnessWith(t, script,
		tools.Bounds{MaxWindowActivities: 2}, client.Limits{MaxPageSize: 3})

	text := h.callError(t, tools.ToolGetActivitiesByDate, map[string]any{
		argStartDate: windowStart,
		argEndDate:   testCalendarDate,
	})

	assertSanitized(t, text)
	if !containsFold(text, "narrow") {
		t.Errorf("the refusal %q does not tell the caller to narrow the window", text)
	}
}

func assertNoCoordinates(t *testing.T, activity map[string]any) {
	t.Helper()

	for _, key := range []string{"latitude", "longitude", "start_latitude", "start_longitude"} {
		if _, present := activity[key]; present {
			t.Errorf("the activity carries %q: a tool result must not pass on coordinates", key)
		}
	}
}
