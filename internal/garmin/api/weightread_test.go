package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The weigh-in range and daily fixtures. Every fixture here is synthetic and
// none is a recording of a real account. Field spellings are cited from
// weight_management.py's curation loops (get_weigh_ins: lines 53-65, :76-79;
// get_daily_weigh_ins: lines 109-120, :126-129) and from
// garminconnect/__init__.py:1343 for samplePk.
const weighInRangeBody = `{"dailyWeightSummaries":[{"allWeightMetrics":[` +
	`{"calendarDate":"` + testCalendarDate + `","weight":72500,"bmi":23.4,"bodyFat":18.2,` +
	`"bodyWater":55.1,"boneMass":3200,"muscleMass":32000,"sourceType":"MANUAL",` +
	`"timestampGMT":"` + testCalendarDate + `T07:30:00.0"}]}],` +
	`"totalAverage":{"weight":72500}}`

const dailyWeighInsBody = `{"dateWeightList":[` +
	`{"weight":72500,"bmi":23.4,"sourceType":"MANUAL",` +
	`"timestampGMT":"` + testCalendarDate + `T07:30:00.0","samplePk":998877}],` +
	`"totalAverage":{"weight":72500}}`

func weightRangePath() string {
	return client.PathWeightRangePrefix + "/" + testCalendarDate + "/" + testCalendarDate
}

func weightDayviewPath() string {
	return client.PathWeightDayviewPrefix + "/" + testCalendarDate
}

func TestGetWeighInsDecodesTheWindow(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(weightRangePath(), testkit.JSON(http.StatusOK, weighInRangeBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newWeight(t, h).GetWeighIns(t.Context(), h.session, mustWindow(t))
	if err != nil {
		t.Fatalf("GetWeighIns() = %v", err)
	}
	measurements := got.Measurements()
	if len(measurements) != 1 {
		t.Fatalf("Measurements() = %d entries, want 1", len(measurements))
	}
	weight, ok := measurements[0].Weight.Float64()
	if !ok || weight != 72500 {
		t.Errorf("Weight = %v, %v, want 72500, true", weight, ok)
	}
	if got.TotalAverage == nil {
		t.Fatal("TotalAverage = nil, want the decoded average")
	}
	if avg, ok := got.TotalAverage.Weight.Float64(); !ok || avg != 72500 {
		t.Errorf("TotalAverage.Weight = %v, %v, want 72500, true", avg, ok)
	}
	if got.MeasurementsTruncated() {
		t.Error("MeasurementsTruncated() = true, want false")
	}
	if got.Payload().Len() == 0 {
		t.Error("Payload() is empty, want the retained response")
	}
}

func TestGetWeighInsRefusesAZeroWindow(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	if _, err := newWeight(t, h).GetWeighIns(
		t.Context(), h.session, client.DateRange{}); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("GetWeighIns() with a zero window = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestGetWeighInsRefusesAWindowOverTheBound(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{MaxDateRangeDays: 1})
	start := mustDate(t, testCalendarDate)
	end := start.AddDays(2)
	span, err := client.NewDateRange(start, end)
	if err != nil {
		t.Fatalf("client.NewDateRange() = %v", err)
	}

	if _, err := newWeight(t, h).GetWeighIns(
		t.Context(), h.session, span); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("GetWeighIns() over the bound = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestGetDailyWeighInsDecodesTheDay(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(weightDayviewPath(), testkit.JSON(http.StatusOK, dailyWeighInsBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newWeight(t, h).GetDailyWeighIns(t.Context(), h.session, mustDate(t, testCalendarDate))
	if err != nil {
		t.Fatalf("GetDailyWeighIns() = %v", err)
	}
	measurements := got.Measurements()
	if len(measurements) != 1 {
		t.Fatalf("Measurements() = %d entries, want 1", len(measurements))
	}
	if pk, ok := measurements[0].SamplePK.Int64Exact(); !ok || pk != 998877 {
		t.Errorf("SamplePK = %v, %v, want 998877, true", pk, ok)
	}
	if got.MeasurementsTruncated() {
		t.Error("MeasurementsTruncated() = true, want false")
	}
}

func TestGetDailyWeighInsRefusesAZeroDate(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	if _, err := newWeight(t, h).GetDailyWeighIns(
		t.Context(), h.session, client.Date{}); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("GetDailyWeighIns() with a zero date = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestWeighInMeasurementsAreBoundedAndReportTruncation proves a response
// carrying more than maxWeighInMeasurements entries is bounded rather than
// forcing an unbounded result, and that the truncation is reported through a
// flag instead of being silently dropped.
func TestWeighInMeasurementsAreBoundedAndReportTruncation(t *testing.T) {
	t.Parallel()

	over := make([]api.WeighInMeasurement, 1001)
	day := api.DailyWeighIns{DateWeightList: over}
	if got := len(day.Measurements()); got != 1000 {
		t.Errorf("DailyWeighIns.Measurements() = %d entries, want 1000", got)
	}
	if !day.MeasurementsTruncated() {
		t.Error("DailyWeighIns.MeasurementsTruncated() = false, want true")
	}
}
