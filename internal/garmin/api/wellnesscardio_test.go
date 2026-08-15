package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// Every fixture here is synthetic. The readings are invented and no fixture is a
// recording of a real account.

// heartRateBody is a day whose descriptors are declared out of order, whose minimum
// arrives as a numeric string, and whose series carries a null reading followed by an
// hour-long gap. All three are shapes a real document has.
const heartRateBody = `{"userProfilePK":900001,"calendarDate":"` + testCalendarDate + `",` +
	`"startTimestampGMT":"` + testCalendarDate + `T07:00:00.0",` +
	`"endTimestampGMT":"` + testCalendarDate + `T23:00:00.0",` +
	`"maxHeartRate":171,"minHeartRate":"48","restingHeartRate":52,` +
	`"lastSevenDaysAvgRestingHeartRate":53,` +
	`"heartRateValueDescriptors":[{"index":1,"key":"heartrate"},{"index":0,"key":"timestamp"}],` +
	`"heartRateValues":[[1786689600000,61],[1786689720000,null],[1786693320000,66]]}`

const spo2Body = `{"calendarDate":"` + testCalendarDate + `","averageSpO2":95,"lowestSpO2":90,` +
	`"latestSpO2":96,"latestSpO2TimestampGMT":"` + testCalendarDate + `T07:00:00.0",` +
	`"lastSevenDaysAvgSpO2":"95","avgSleepSpO2":94,` +
	`"spO2HourlyAverages":[[1786689600000,95],[1786693200000,null]]}`

const restingHeartRateBody = `{"statisticsStartDate":"` + testCalendarDate + `",` +
	`"statisticsEndDate":"` + testCalendarDate + `","allMetrics":{"metricsMap":{` +
	`"WELLNESS_RESTING_HEART_RATE":[{"calendarDate":"` + testCalendarDate + `","value":52.0}]}}}`

func heartRatePath() string {
	return client.PathDailyHeartRatePrefix + "/" + fakeDisplayName
}

func restingHeartRatePath() string {
	return client.PathRestingHeartRatePrefix + "/" + fakeDisplayName
}

func spo2Path() string {
	return client.PathDailySpO2Prefix + "/" + testCalendarDate
}

func newCardio(t *testing.T, h harness) *api.WellnessCardio {
	t.Helper()

	cardio, err := api.NewWellnessCardio(h.rc)
	if err != nil {
		t.Fatalf("NewWellnessCardio() = %v", err)
	}
	return cardio
}

func TestNewWellnessCardioRefusesAMissingRequestLayer(t *testing.T) {
	t.Parallel()

	if _, err := api.NewWellnessCardio(nil); !errors.Is(err, client.ErrNotConfigured) {
		t.Fatalf("NewWellnessCardio(nil) = %v, want ErrNotConfigured", err)
	}
}

func TestWellnessCardioSharesTheRequestLayerOfWellness(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(heartRatePath(), testkit.JSON(http.StatusOK, heartRateBody))
	h := newHarness(t, script, client.Limits{})

	cardio := newWellness(t, h).Cardio()
	if _, err := cardio.HeartRates(
		t.Context(), h.session, mustDisplayName(t), mustDate(t, testCalendarDate),
	); err != nil {
		t.Fatalf("HeartRates() through Wellness.Cardio() = %v", err)
	}
}

func TestWellnessCardioHeartRatesDecodesTheDayAndItsSeries(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(heartRatePath(), testkit.JSON(http.StatusOK, heartRateBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newCardio(t, h).HeartRates(
		t.Context(), h.session, mustDisplayName(t), mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("HeartRates() = %v", err)
	}

	if value, ok := got.MaxHeartRate.Float64(); !ok || value != 171 {
		t.Errorf("MaxHeartRate = %v/%t, want 171", value, ok)
	}
	if value, ok := got.MinHeartRate.Float64(); !ok || value != 48 {
		t.Errorf("MinHeartRate = %v/%t, want 48 decoded from a numeric string", value, ok)
	}

	samples, err := got.Samples()
	if err != nil {
		t.Fatalf("Samples() = %v", err)
	}
	if len(samples) != 3 {
		t.Fatalf("len(Samples()) = %d, want 3: a null reading keeps its point", len(samples))
	}
	if samples[1].Value.IsSet() {
		t.Error("the middle sample carries a value, want the null reading reported as absent")
	}
	if instant, ok := samples[1].TimeMillis.Int64(); !ok || instant != 1786689720000 {
		t.Errorf("the middle sample's instant = %v/%t, want the declared timestamp position",
			instant, ok)
	}
}

func TestWellnessCardioHeartRatesSendsTheDateQuery(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(heartRatePath(), testkit.JSON(http.StatusOK, heartRateBody))
	h := newHarness(t, script, client.Limits{})

	if _, err := newCardio(t, h).HeartRatesSummary(
		t.Context(), h.session, mustDisplayName(t), mustDate(t, testCalendarDate),
	); err != nil {
		t.Fatalf("HeartRatesSummary() = %v", err)
	}

	requests := h.server.Requests()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1: the two views share one read", len(requests))
	}
	if got := requests[0].Query.Get(client.QueryDate); got != testCalendarDate {
		t.Errorf("date query = %q, want %q", got, testCalendarDate)
	}
}

func TestWellnessCardioHeartRatesRefusesAZeroDisplayName(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})

	_, err := newCardio(t, h).HeartRates(
		t.Context(), h.session, client.DisplayName{}, mustDate(t, testCalendarDate))
	if !errors.Is(err, client.ErrValidation) {
		t.Fatalf("HeartRates() without a display name = %v, want ErrValidation", err)
	}
}

func TestWellnessCardioHeartRatesRefusesAZeroDate(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})

	_, err := newCardio(t, h).HeartRates(
		t.Context(), h.session, mustDisplayName(t), client.Date{})
	if !errors.Is(err, client.ErrValidation) {
		t.Fatalf("HeartRates() without a date = %v, want ErrValidation", err)
	}
}

func TestWellnessCardioRestingHeartRateReadsTheMetricByKey(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(restingHeartRatePath(), testkit.JSON(http.StatusOK, restingHeartRateBody))
	h := newHarness(t, script, client.Limits{})

	day := mustDate(t, testCalendarDate)
	got, err := newCardio(t, h).RestingHeartRateDay(
		t.Context(), h.session, mustDisplayName(t), day)
	if err != nil {
		t.Fatalf("RestingHeartRateDay() = %v", err)
	}

	reading, ok := got.RestingHeartRate(day)
	if !ok {
		t.Fatal("RestingHeartRate() reported no reading, want the metric-map entry")
	}
	if value, _ := reading.Float64(); value != 52 {
		t.Errorf("RestingHeartRate() = %v, want 52", value)
	}

	request := h.server.Requests()[0]
	if got := request.Query.Get(client.QueryMetricID); got != "60" {
		t.Errorf("metricId query = %q, want %q", got, "60")
	}
	if got := request.Query.Get(client.QueryFromDate); got != testCalendarDate {
		t.Errorf("fromDate query = %q, want %q", got, testCalendarDate)
	}
	if got := request.Query.Get(client.QueryUntilDate); got != testCalendarDate {
		t.Errorf("untilDate query = %q, want %q", got, testCalendarDate)
	}
}

func TestRestingHeartRateAcceptsASingleUnknownMetricKey(t *testing.T) {
	t.Parallel()

	body := `{"allMetrics":{"metricsMap":{"SOMETHING_ELSE":[{"value":47}]}}}`
	script := testkit.NewScript().
		With(restingHeartRatePath(), testkit.JSON(http.StatusOK, body))
	h := newHarness(t, script, client.Limits{})

	day := mustDate(t, testCalendarDate)
	got, err := newCardio(t, h).RestingHeartRateDay(
		t.Context(), h.session, mustDisplayName(t), day)
	if err != nil {
		t.Fatalf("RestingHeartRateDay() = %v", err)
	}
	if _, ok := got.RestingHeartRate(day); !ok {
		t.Error("a single-entry metric map reported no reading, want the one series it holds")
	}
}

func TestRestingHeartRateReportsAnEmptyDocumentAsNoReading(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(restingHeartRatePath(), testkit.JSON(http.StatusOK, `{"allMetrics":null}`))
	h := newHarness(t, script, client.Limits{})

	day := mustDate(t, testCalendarDate)
	got, err := newCardio(t, h).RestingHeartRateDay(
		t.Context(), h.session, mustDisplayName(t), day)
	if err != nil {
		t.Fatalf("RestingHeartRateDay() = %v", err)
	}
	if _, ok := got.RestingHeartRate(day); ok {
		t.Error("a document with no metrics reported a reading, want none")
	}
}

func TestWellnessCardioSpO2DecodesTheDayAndItsHourlyAverages(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(spo2Path(), testkit.JSON(http.StatusOK, spo2Body))
	h := newHarness(t, script, client.Limits{})

	got, err := newCardio(t, h).SpO2(t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("SpO2() = %v", err)
	}
	if value, ok := got.LastSevenDaysAvgSpO2.Float64(); !ok || value != 95 {
		t.Errorf("LastSevenDaysAvgSpO2 = %v/%t, want 95 decoded from a numeric string", value, ok)
	}

	hourly, err := got.HourlyAverages()
	if err != nil {
		t.Fatalf("HourlyAverages() = %v", err)
	}
	if len(hourly) != 2 {
		t.Fatalf("len(HourlyAverages()) = %d, want 2", len(hourly))
	}
	if hourly[1].Value.IsSet() {
		t.Error("the second hour carries a value, want the null reading reported as absent")
	}
}
