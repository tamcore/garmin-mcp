package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The hydration, blood-pressure, lifestyle-log and sleep-digest reads. Every fixture
// here is synthetic and none is a recording of a real account.

const hydrationBody = `{"calendarDate":"` + testCalendarDate + `","valueInML":750.0,` +
	`"goalInML":2000.0,"dailyAverageinML":null,"sweatLossInML":null}`

const bloodPressureBody = `{"measurementSummaries":[{"measurementSummaryDate":"` +
	testCalendarDate + `","measurements":[{"systolic":118,"diastolic":76,"pulse":60,` +
	`"measurementTimestampGMT":"` + testCalendarDate + `T07:00:00.0","sourceType":"MANUAL",` +
	`"notes":null}]}]}`

const flatBloodPressureBody = `{"measurements":[{"systolic":"121","diastolic":78}]}`

func hydrationPath() string {
	return client.PathDailyHydrationPrefix + "/" + testCalendarDate
}

func lifestylePath() string {
	return client.PathLifestyleLoggingPrefix + "/" + testCalendarDate
}

func bloodPressurePath() string {
	return client.PathBloodPressureRangePrefix + "/" + testCalendarDate + "/" + testCalendarDate
}

// mustWindow is the one-day window the blood-pressure fixtures are scripted for.
func mustWindow(t *testing.T) client.DateRange {
	t.Helper()

	day := mustDate(t, testCalendarDate)
	span, err := client.NewDateRange(day, day)
	if err != nil {
		t.Fatalf("client.NewDateRange() = %v", err)
	}
	return span
}

func TestWellnessCardioHydrationDecodesTheDay(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(hydrationPath(), testkit.JSON(http.StatusOK, hydrationBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newCardio(t, h).Hydration(t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("Hydration() = %v", err)
	}
	if value, ok := got.ValueInML.Float64(); !ok || value != 750 {
		t.Errorf("ValueInML = %v/%t, want 750", value, ok)
	}
	if got.DailyAverageInML.IsSet() {
		t.Error("DailyAverageInML is set, want a null field reported as absent")
	}
}

func TestWellnessCardioBloodPressureFlattensTheGroupedEnvelope(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(bloodPressurePath(), testkit.JSON(http.StatusOK, bloodPressureBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newCardio(t, h).BloodPressure(t.Context(), h.session, mustWindow(t))
	if err != nil {
		t.Fatalf("BloodPressure() = %v", err)
	}

	readings := got.Measurements()
	if len(readings) != 1 {
		t.Fatalf("len(Measurements()) = %d, want 1", len(readings))
	}
	if value, ok := readings[0].Systolic.Float64(); !ok || value != 118 {
		t.Errorf("Systolic = %v/%t, want 118", value, ok)
	}
	if got := h.server.Requests()[0].Query.Get(client.QueryIncludeAll); got != "true" {
		t.Errorf("includeAll query = %q, want %q", got, "true")
	}
}

func TestWellnessCardioBloodPressureAcceptsAFlatEnvelope(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(bloodPressurePath(), testkit.JSON(http.StatusOK, flatBloodPressureBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newCardio(t, h).BloodPressure(t.Context(), h.session, mustWindow(t))
	if err != nil {
		t.Fatalf("BloodPressure() = %v", err)
	}
	readings := got.Measurements()
	if len(readings) != 1 {
		t.Fatalf("len(Measurements()) = %d, want 1", len(readings))
	}
	if value, ok := readings[0].Systolic.Float64(); !ok || value != 121 {
		t.Errorf("Systolic = %v/%t, want 121 decoded from a numeric string", value, ok)
	}
}

func TestWellnessCardioBloodPressureRefusesAWindowOverTheBound(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{MaxDateRangeDays: 1})

	span, err := client.NewDateRange(mustDate(t, "2026-01-01"), mustDate(t, "2026-03-01"))
	if err != nil {
		t.Fatalf("client.NewDateRange() = %v", err)
	}
	if _, err := newCardio(t, h).BloodPressure(
		t.Context(), h.session, span,
	); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("BloodPressure() over the bound = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("requests = %d, want 0: a refused window costs no Garmin call", got)
	}
}

func TestWellnessCardioBloodPressureRefusesAZeroWindow(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})

	if _, err := newCardio(t, h).BloodPressure(
		t.Context(), h.session, client.DateRange{},
	); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("BloodPressure() with a zero window = %v, want ErrValidation", err)
	}
}

func TestWellnessCardioLifestyleLoggingCarriesTheDocumentVerbatim(t *testing.T) {
	t.Parallel()

	body := `{"unknownKey":[{"nested":true}]}`
	script := testkit.NewScript().With(lifestylePath(), testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	got, err := newCardio(t, h).LifestyleLogging(
		t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("LifestyleLogging() = %v", err)
	}
	if !got.HasDocument() {
		t.Fatal("HasDocument() = false, want true")
	}
	if !json.Valid(got.Document) {
		t.Error("the carried document is not valid JSON")
	}
}

func TestWellnessCardioLifestyleLoggingReportsAnEmptyBody(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(lifestylePath(), testkit.Behavior{Status: http.StatusNoContent})
	h := newHarness(t, script, client.Limits{})

	got, err := newCardio(t, h).LifestyleLogging(
		t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("LifestyleLogging() = %v", err)
	}
	if got.HasDocument() {
		t.Error("HasDocument() = true for an empty body, want false")
	}
}

func TestNewSleepDigestReadsTheFieldsTheSleepViewOmits(t *testing.T) {
	t.Parallel()

	body := `{"dailySleepDTO":{"sleepTimeSeconds":27000,"napTimeSeconds":0,"awakeCount":1,` +
		`"restlessMomentsCount":9,"avgSleepStress":17.0,"restingHeartRate":52,` +
		`"sleepScores":{"overall":{"value":81,"qualifierKey":"GOOD"}}},` +
		`"wellnessSpO2SleepSummaryDTO":{"averageSpo2":96,"lowestSpo2":92},"avgOvernightHrv":48.0}`
	script := testkit.NewScript().With(sleepPath(), testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	sleep, err := newWellness(t, h).DailySleep(
		t.Context(), h.session, mustDisplayName(t), mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("DailySleep() = %v", err)
	}

	digest, err := api.NewSleepDigest(sleep)
	if err != nil {
		t.Fatalf("NewSleepDigest() = %v", err)
	}
	if digest.Daily == nil {
		t.Fatal("Daily = nil, want the nested dailySleepDTO")
	}
	if value, ok := digest.Daily.RestlessMomentsCount.Float64(); !ok || value != 9 {
		t.Errorf("RestlessMomentsCount = %v/%t, want 9", value, ok)
	}
	overall, ok := digest.Overall()
	if !ok {
		t.Fatal("Overall() reported no score, want the nested overall entry")
	}
	if value, _ := overall.Value.Float64(); value != 81 {
		t.Errorf("the overall score = %v, want 81", value)
	}
	if digest.SpO2 == nil {
		t.Fatal("SpO2 = nil, want the overnight pulse-ox summary")
	}
	if got := len(h.server.Requests()); got != 1 {
		t.Errorf("requests = %d, want 1: the digest re-reads the retained payload", got)
	}
}

func TestNewSleepDigestOfAnEmptyDocumentIsTheZeroDigest(t *testing.T) {
	t.Parallel()

	digest, err := api.NewSleepDigest(api.DailySleep{})
	if err != nil {
		t.Fatalf("NewSleepDigest() = %v", err)
	}
	if digest.Daily != nil || digest.SpO2 != nil {
		t.Error("the zero sleep document produced a populated digest, want the zero value")
	}
	if _, ok := digest.Overall(); ok {
		t.Error("Overall() reported a score for the zero digest, want none")
	}
}
