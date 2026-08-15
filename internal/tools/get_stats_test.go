// The daily-wellness fixtures. The tools themselves are driven by the single
// in-package harness in harness_internal_test.go, which builds the real registrar
// from register.go rather than a private registration list.
package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// Every fixture here is synthetic. The readings are invented and no fixture is a
// recording of a real account.
const (
	dailyName        = "fake-tester"
	dailyDate        = "2026-01-31"
	dailyWindowStart = "2026-01-01"
	dailyWeekStart   = "2026-01-12"
	dailyWindowEnd   = "2026-01-05"

	dailyProfileBody = `{"profileId":900001,"displayName":"` + dailyName + `",` +
		`"fullName":"Fake Tester"}`

	dailyStatsBody = `{"calendarDate":"` + dailyDate + `","totalSteps":9123,` +
		`"dailyStepGoal":8000,"totalDistanceMeters":7345,"floorsAscended":11.5,` +
		`"floorsDescended":10.4,"totalKilocalories":2410.0,"activeKilocalories":610.0,` +
		`"bmrKilocalories":1800.0,"highlyActiveSeconds":900,"activeSeconds":5400,` +
		`"sedentarySeconds":48000,"sleepingSeconds":27000,"moderateIntensityMinutes":31,` +
		`"vigorousIntensityMinutes":12,"intensityMinutesGoal":150,"minHeartRate":48,` +
		`"maxHeartRate":171,"restingHeartRate":52,"lastSevenDaysAvgRestingHeartRate":54,` +
		`"averageStressLevel":24,"maxStressLevel":81,"stressQualifier":"balanced",` +
		`"bodyBatteryChargedValue":62,"bodyBatteryDrainedValue":49,` +
		`"bodyBatteryHighestValue":88,"bodyBatteryLowestValue":19,` +
		`"bodyBatteryMostRecentValue":41,"avgWakingRespirationValue":14.2,` +
		`"highestRespirationValue":19.1,"lowestRespirationValue":9.4}`

	// The sampled account records no weight: dateWeightList arrives as an empty
	// array and every metric of totalAverage is null. No test in this package
	// asserts a metric type, because no sample has shown one.
	dailyBodyBody = `{"startDate":"` + dailyDate + `","endDate":"` + dailyDate + `",` +
		`"dateWeightList":[],"totalAverage":{"from":1769817600000,` +
		`"until":1769903999999,"weight":null,"bmi":null,"bodyFat":null,` +
		`"bodyWater":null,"boneMass":null,"muscleMass":null,"physiqueRating":null,` +
		`"visceralFat":null,"metabolicAge":null,"trend":null}}`
)

// Argument keys the daily-wellness tests assert on, named once so a rename shows up
// in one place.
const (
	argStartDate = "start_date"
	argEndDate   = "end_date"

	// keyDistanceMeters and keyModerateIntensityMinutes are curated result keys the
	// stats test asserts on, and malformedDate is the date form the parser refuses.
	keyDistanceMeters           = "distance_meters"
	keyModerateIntensityMinutes = "moderate_intensity_minutes"
	keyEmail                    = "email"
	keyUserID                   = "user_id"
	keyWeight                   = "weight"
	keyVigorousMinutes          = "vigorous_intensity_minutes"
	malformedDate               = "31-01-2026"
)

// dailyScript scripts the profile read every display-name-keyed tool performs first.
func dailyScript() testkit.Script {
	return testkit.NewScript().With(client.PathSocialProfile,
		dailyRepeat(testkit.JSON(http.StatusOK, dailyProfileBody), 8)...)
}

func dailyRepeat(behavior testkit.Behavior, n int) []testkit.Behavior {
	out := make([]testkit.Behavior, 0, n)
	for range n {
		out = append(out, behavior)
	}
	return out
}

func dailySummaryPath() string { return client.PathUserSummaryPrefix + "/" + dailyName }

func TestGetStatsReturnsTheCuratedUpstreamKeys(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(dailySummaryPath(), testkit.JSON(http.StatusOK, dailyStatsBody)))

	got := h.call(t, ToolGetStats, map[string]any{argDate: dailyDate})
	for _, key := range []string{
		"date", "total_steps", "daily_step_goal", keyDistanceMeters, "floors_ascended",
		"floors_descended", "total_calories", "active_calories", "bmr_calories",
		"highly_active_seconds", "active_seconds", "sedentary_seconds", "sleeping_seconds",
		keyModerateIntensityMinutes, keyVigorousMinutes, "intensity_minutes_goal",
		"min_heart_rate_bpm", "max_heart_rate_bpm", "resting_heart_rate_bpm",
		"last_7_days_avg_resting_hr", "avg_stress_level", "max_stress_level",
		"stress_qualifier", "body_battery_charged", "body_battery_drained",
		"body_battery_highest", "body_battery_lowest", "body_battery_current",
		"avg_waking_respiration", "highest_respiration", "lowest_respiration",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("the result is missing the curated key %q", key)
		}
	}
	if got["date"] != dailyDate {
		t.Errorf("date = %v, want the day that was asked for", got["date"])
	}
	// The account records no pulse ox, so the two SpO2 keys must be omitted rather
	// than reported as zero readings.
	if _, ok := got["avg_spo2_percent"]; ok {
		t.Error("avg_spo2_percent is present for a day Garmin sent no pulse ox for")
	}
}

func TestGetStatsRefusesAMalformedDateBeforeAnyGarminCall(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript())

	advice := h.callError(t, ToolGetStats, map[string]any{argDate: malformedDate})
	if !strings.Contains(advice, "date") {
		t.Errorf("advice = %q, want an actionable refusal naming the date", advice)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestGetStatsAcceptsNoAccountSelector(t *testing.T) {
	t.Parallel()

	// The principal comes from the request context. An argument that named an
	// account would be a way to read somebody else's health data, so the strict
	// schema must refuse it.
	h := newToolHarness(t, dailyScript())

	for _, selector := range []string{keyUserID, keyDisplayName, keyEmail} {
		result := h.rawCall(t, ToolGetStats, map[string]any{argDate: dailyDate, selector: "other"})
		if !result.IsError {
			t.Errorf("get_stats accepted the account selector %q", selector)
		}
	}
}

func TestDailyStatsLogValueReportsShapeAndNoReading(t *testing.T) {
	t.Parallel()

	steps := 9123
	stats := DailyActivityStats{Date: dailyDate, TotalSteps: &steps}

	rendered := stats.LogValue().String()
	if strings.Contains(rendered, "9123") {
		t.Errorf("LogValue rendered a reading: %s", rendered)
	}
	if !strings.Contains(rendered, "steps=set") {
		t.Errorf("LogValue = %s, want the presence of the step count", rendered)
	}
}
