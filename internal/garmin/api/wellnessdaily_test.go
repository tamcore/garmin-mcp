package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// Every fixture is synthetic: invented readings, no recording of a real account.
const (
	dailyStatsBody = `{"calendarDate":"` + testCalendarDate + `","totalSteps":9123,` +
		`"dailyStepGoal":"8000","totalDistanceMeters":7345,"floorsAscended":11.5,` +
		`"floorsDescended":10.4,"totalKilocalories":2410.0,"activeKilocalories":610.0,` +
		`"bmrKilocalories":1800.0,"highlyActiveSeconds":900,"activeSeconds":5400,` +
		`"sedentarySeconds":48000,"sleepingSeconds":27000,"moderateIntensityMinutes":31,` +
		`"vigorousIntensityMinutes":12,"intensityMinutesGoal":150,"minHeartRate":48,` +
		`"maxHeartRate":171,"restingHeartRate":52,"lastSevenDaysAvgRestingHeartRate":54,` +
		`"averageStressLevel":24,"maxStressLevel":81,"stressQualifier":"balanced",` +
		`"bodyBatteryChargedValue":62,"bodyBatteryDrainedValue":49,` +
		`"bodyBatteryHighestValue":88,"bodyBatteryLowestValue":19,` +
		`"bodyBatteryMostRecentValue":41,"averageSpo2":null,"lowestSpo2":null,` +
		`"avgWakingRespirationValue":14.2,"highestRespirationValue":19.1,` +
		`"lowestRespirationValue":9.4,"unknownField":{"x":1}}`

	weeklyStepsBody = `[{"calendarDate":"2026-01-05","values":{"totalSteps":61234,` +
		`"averageSteps":8747.7,"totalDistance":49000,"averageDistance":7000,` +
		`"wellnessDataDaysCount":7}},{"calendarDate":"2026-01-12","values":null}]`

	weeklyIntensityBody = `[{"calendarDate":"2026-01-05","weeklyGoal":150,` +
		`"moderateValue":"90","vigorousValue":30}]`
)

// testWindowStart is the synthetic window start every ranged fixture here uses.
const testWindowStart = "2026-01-01"

func newWellnessDaily(t *testing.T, h harness) *api.WellnessDaily {
	t.Helper()

	daily, err := api.NewWellnessDaily(h.rc)
	if err != nil {
		t.Fatalf("NewWellnessDaily() = %v", err)
	}
	return daily
}

func TestWellnessDailyStatsDecodesTheCuratedFieldSet(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(summaryPath(), testkit.JSON(http.StatusOK, dailyStatsBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newWellnessDaily(t, h).Stats(t.Context(), h.session, mustDisplayName(t),
		mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("Stats() = %v", err)
	}

	if got.CalendarDate == nil || *got.CalendarDate != testCalendarDate {
		t.Errorf("CalendarDate = %v, want %q", got.CalendarDate, testCalendarDate)
	}
	if steps, ok := got.TotalSteps.Int64(); !ok || steps != 9123 {
		t.Errorf("TotalSteps = %v/%v, want 9123", steps, ok)
	}
	if goal, ok := got.DailyStepGoal.Int64(); !ok || goal != 8000 {
		t.Errorf("DailyStepGoal = %v/%v, want 8000 from the string form", goal, ok)
	}
	if floors, ok := got.FloorsDescended.Float64(); !ok || floors != 10.4 {
		t.Errorf("FloorsDescended = %v/%v, want 10.4", floors, ok)
	}
	if qualifier, ok := got.StressQualifier.Value(); !ok || qualifier != "balanced" {
		t.Errorf("StressQualifier = %q/%v, want balanced", qualifier, ok)
	}
	if got.AverageSpo2.IsSet() {
		t.Error("AverageSpo2 must report absent for an explicit null")
	}
	if respiration, ok := got.AvgWakingRespiration.Float64(); !ok || respiration != 14.2 {
		t.Errorf("AvgWakingRespiration = %v/%v, want 14.2", respiration, ok)
	}
}

func TestWellnessDailyStatsSendsTheCalendarDate(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(summaryPath(), testkit.JSON(http.StatusOK, dailyStatsBody))
	h := newHarness(t, script, client.Limits{})

	if _, err := newWellnessDaily(t, h).Stats(t.Context(), h.session, mustDisplayName(t),
		mustDate(t, testCalendarDate)); err != nil {
		t.Fatalf("Stats() = %v", err)
	}

	requests := h.server.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want 1", len(requests))
	}
	if got := requests[0].Query.Get(client.QueryCalendarDate); got != testCalendarDate {
		t.Errorf("calendarDate = %q, want %q", got, testCalendarDate)
	}
}

func TestWellnessDailyStatsRefusesAWithheldOrEmptyDay(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		behavior testkit.Behavior
		want     error
	}{
		"withheld day": {
			testkit.JSON(http.StatusOK, `{"privacyProtected":true}`), client.ErrAuthentication,
		},
		"empty body": {
			testkit.Behavior{Status: http.StatusNoContent}, client.ErrUnexpectedResponse,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, testkit.NewScript().With(summaryPath(), tc.behavior), client.Limits{})
			_, err := newWellnessDaily(t, h).Stats(t.Context(), h.session, mustDisplayName(t),
				mustDate(t, testCalendarDate))
			if !errors.Is(err, tc.want) {
				t.Errorf("Stats() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestWellnessDailyStatsRequiresADisplayNameAndADate(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	daily := newWellnessDaily(t, h)

	if _, err := daily.Stats(t.Context(), h.session, client.DisplayName{},
		mustDate(t, testCalendarDate)); !errors.Is(err, client.ErrValidation) {
		t.Errorf("Stats() with no display name = %v, want a validation error", err)
	}
	if _, err := daily.Stats(t.Context(), h.session, mustDisplayName(t),
		client.Date{}); !errors.Is(err, client.ErrValidation) {
		t.Errorf("Stats() with no date = %v, want a validation error", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestWellnessDailyWeeklyStepsDecodesTheNestedValues(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(client.PathWeeklyStepsStatsPrefix+"/"+testCalendarDate+"/4",
			testkit.JSON(http.StatusOK, weeklyStepsBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newWellnessDaily(t, h).WeeklySteps(t.Context(), h.session,
		mustDate(t, testCalendarDate), 4)
	if err != nil {
		t.Fatalf("WeeklySteps() = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("%d weeks returned, want 2", len(got))
	}
	if got[0].Values == nil {
		t.Fatal("the first week lost its nested values")
	}
	if steps, ok := got[0].Values.TotalSteps.Int64(); !ok || steps != 61234 {
		t.Errorf("TotalSteps = %v/%v, want 61234", steps, ok)
	}
	if got[1].Values != nil {
		t.Error("a week with null values must decode as absent, not as zeroes")
	}
}

func TestWellnessDailyWeeklyStepsRefusesAnUnsetDateOrWeekCount(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	daily := newWellnessDaily(t, h)

	if _, err := daily.WeeklySteps(t.Context(), h.session, client.Date{}, 4); !errors.Is(
		err, client.ErrValidation) {
		t.Errorf("WeeklySteps() with no date = %v, want a validation error", err)
	}
	if _, err := daily.WeeklySteps(t.Context(), h.session, mustDate(t, testCalendarDate),
		0); !errors.Is(err, client.ErrValidation) {
		t.Errorf("WeeklySteps() with no weeks = %v, want a validation error", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestWellnessDailyWeeklyIntensityMinutesDecodesTheAggregate(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(client.PathWeeklyIntensityMinutesStatsPrefix+"/"+testWindowStart+"/"+testCalendarDate,
			testkit.JSON(http.StatusOK, weeklyIntensityBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newWellnessDaily(t, h).WeeklyIntensityMinutes(t.Context(), h.session,
		mustRange(t, testWindowStart, testCalendarDate))
	if err != nil {
		t.Fatalf("WeeklyIntensityMinutes() = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("%d weeks returned, want 1", len(got))
	}
	if moderate, ok := got[0].ModerateValue.Int64(); !ok || moderate != 90 {
		t.Errorf("ModerateValue = %v/%v, want 90 from the string form", moderate, ok)
	}
	if goal, ok := got[0].WeeklyGoal.Int64(); !ok || goal != 150 {
		t.Errorf("WeeklyGoal = %v/%v, want 150", goal, ok)
	}
}

func TestWellnessDailyWeeklyIntensityMinutesBoundsTheWindow(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{MaxDateRangeDays: 3})
	_, err := newWellnessDaily(t, h).WeeklyIntensityMinutes(t.Context(), h.session,
		mustRange(t, testWindowStart, testCalendarDate))
	if !errors.Is(err, client.ErrValidation) {
		t.Errorf("WeeklyIntensityMinutes() over the bound = %v, want a validation error", err)
	}
}

func TestWellnessDailyRefusesANilRequestLayerAndAnUnusableSession(t *testing.T) {
	t.Parallel()

	if _, err := api.NewWellnessDaily(nil); !errors.Is(err, client.ErrNotConfigured) {
		t.Errorf("NewWellnessDaily(nil) = %v, want ErrNotConfigured", err)
	}

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	_, err := newWellnessDaily(t, h).Stats(t.Context(), client.Session{}, mustDisplayName(t),
		mustDate(t, testCalendarDate))
	if !errors.Is(err, client.ErrMissingPrincipal) {
		t.Errorf("Stats() with a zero session = %v, want ErrMissingPrincipal", err)
	}
}
