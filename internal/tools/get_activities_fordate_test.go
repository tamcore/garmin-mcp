package tools

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// dayDocument is the synthetic answer of the single-day gateway endpoint. The
// heart-rate series around the activities is deliberately present: the tool must
// drop it. Every value is invented.
const dayDocument = `{"userProfilePK":900001,"calendarDate":"` + parityDate + `",` +
	`"restingHeartRate":52,"heartRateValues":[[1738296000000,61],[1738296060000,64]],` +
	`"ActivitiesForDay":{"payload":[` +
	`{"activityId":9001,"activityName":"Synthetic run",` +
	`"activityType":{"typeKey":"running"},"eventType":{"typeKey":"uncategorized"},` +
	`"startTimeLocal":"2026-01-31 06:12:00","distance":10000.0,"duration":3000.0,` +
	`"calories":640,"averageHR":148,"steps":8800,"lapCount":4,` +
	`"moderateIntensityMinutes":12,"vigorousIntensityMinutes":34,` +
	`"startLatitude":48.1,"startLongitude":11.5,"unknownField":true}]}}`

func forDateArgs() map[string]any { return map[string]any{argDate: parityDate} }

// TestGetActivitiesForDateReturnsTheDaysActivities pins the mapping.
func TestGetActivitiesForDateReturnsTheDaysActivities(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(parityForDatePath(),
		testkit.JSON(http.StatusOK, dayDocument))
	h := newToolHarness(t, script)

	result := h.call(t, ToolGetActivitiesForDate, forDateArgs())

	if got, _ := result[argDate].(string); got != parityDate {
		t.Errorf("date = %q, want %q", got, parityDate)
	}
	if got := number(t, result, "count"); got != 1 {
		t.Fatalf("count = %v, want 1", got)
	}
	activity := entry(t, list(t, result, "activities"), 0)
	assertDailyActivity(t, activity)
}

func assertDailyActivity(t *testing.T, activity map[string]any) {
	t.Helper()

	cases := map[string]float64{
		argActivityID:                9001,
		"distance_meters":            10000,
		"duration_seconds":           3000,
		argCalories:                  640,
		"average_heart_rate":         148,
		"steps":                      8800,
		"lap_count":                  4,
		"moderate_intensity_minutes": 12,
		"vigorous_intensity_minutes": 34,
	}
	for key, want := range cases {
		if got := number(t, activity, key); got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
	if got, _ := activity["activity_type"].(string); got != typeKeyRunning {
		t.Errorf("activity_type = %q, want running", got)
	}
	if got, _ := activity["event_type"].(string); got != "uncategorized" {
		t.Errorf("event_type = %q, want uncategorized", got)
	}
}

// TestGetActivitiesForDateDropsTheHeartRateSeriesAndTheCoordinates is the disclosure
// test. Garmin answers this endpoint with a day of heart-rate samples and with the
// start position of every outing; neither may leave this server.
func TestGetActivitiesForDateDropsTheHeartRateSeriesAndTheCoordinates(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(parityForDatePath(),
		testkit.JSON(http.StatusOK, dayDocument))
	h := newToolHarness(t, script)

	rendered := h.text(t, ToolGetActivitiesForDate, forDateArgs())

	for _, forbidden := range []string{
		"heartRateValues", "restingHeartRate", "1738296000000",
		"48.1", "11.5", keyStartLatitude, "startLongitude",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("the result carries %q, which must never leave this server", forbidden)
		}
	}
}

// TestGetActivitiesForDateReportsAQuietDayAsEmpty proves an empty day is a normal
// state rather than a failure.
func TestGetActivitiesForDateReportsAQuietDayAsEmpty(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"empty payload":        `{"ActivitiesForDay":{"payload":[]}}`,
		"no activities key":    `{"restingHeartRate":52}`,
		"null day":             `null`,
		"payload at top level": `{"payload":[]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(parityForDatePath(),
				testkit.JSON(http.StatusOK, body))
			h := newToolHarness(t, script)

			result := h.call(t, ToolGetActivitiesForDate, forDateArgs())
			if got := number(t, result, "count"); got != 0 {
				t.Errorf("count = %v, want 0", got)
			}
			if got := len(list(t, result, "activities")); got != 0 {
				t.Errorf("activities holds %d entries, want none", got)
			}
		})
	}
}

// TestGetActivitiesForDateRefusesAnImpossibleDateBeforeAnyCall proves the argument
// is validated before Garmin is reached.
func TestGetActivitiesForDateRefusesAnImpossibleDateBeforeAnyCall(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, testkit.NewScript())

	advice := h.callError(t, ToolGetActivitiesForDate, map[string]any{argDate: "2026-13-45"})
	if !strings.Contains(advice, argDate) {
		t.Errorf("the refusal %q does not name the offending argument", advice)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want none", got)
	}
}

// TestGetActivitiesForDateDeclaresOneStrictDateArgument proves the schema carries no
// account selector.
func TestGetActivitiesForDateDeclaresOneStrictDateArgument(t *testing.T) {
	t.Parallel()

	properties := getActivitiesForDateContract().Schema.Properties()
	if len(properties) != 1 || properties[0].Name != argDate {
		t.Fatalf("declared properties = %+v, want exactly one named date", properties)
	}
	if !properties[0].Required || properties[0].Pattern == "" || properties[0].Format == "" {
		t.Errorf("the date property is not strict enough: %+v", properties[0])
	}
}

// TestGetActivitiesForDateRefusesAnImplausibleDay proves the day bound is a refusal
// rather than a silent truncation: a partial day would read as a whole one.
func TestGetActivitiesForDateRefusesAnImplausibleDay(t *testing.T) {
	t.Parallel()

	entries := make([]string, 0, defaultMaxDailyActivities+1)
	for index := range defaultMaxDailyActivities + 1 {
		entries = append(entries, `{"activityId":`+strconv.Itoa(9000+index)+`}`)
	}
	body := `{"ActivitiesForDay":{"payload":[` + strings.Join(entries, ",") + `]}}`
	script := testkit.NewScript().With(parityForDatePath(), testkit.JSON(http.StatusOK, body))
	h := newToolHarness(t, script)

	if advice := h.callError(t, ToolGetActivitiesForDate, forDateArgs()); advice == "" {
		t.Error("the refusal carries no advice")
	}
}
