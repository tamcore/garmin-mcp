// The weigh-in read fixtures and tests. The tools are driven by the single
// in-package harness in harness_internal_test.go, which builds the real registrar
// from register.go rather than a private registration list.
package tools

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// Every fixture here is synthetic. No fixture in this file is a recording of a
// real account. Field spellings follow weight_management.py's own curation
// loops (get_weigh_ins: lines 53-65, :76-79; get_daily_weigh_ins: lines
// 109-120, :126-129), cross-checked against weightread.go's WeighInMeasurement.
const (
	weighInOlderDate = "2026-01-30"

	weighInRangeBody = `{"dailyWeightSummaries":[` +
		`{"allWeightMetrics":[{"calendarDate":"` + weighInOlderDate + `","weight":71000,` +
		`"sourceType":"MANUAL","timestampGMT":"` + weighInOlderDate + `T07:00:00.0"}]},` +
		`{"allWeightMetrics":[{"calendarDate":"` + dailyDate + `","weight":72500,"bmi":23.4,` +
		`"bodyFat":18.2,"bodyWater":55.1,"boneMass":3200,"muscleMass":32000,` +
		`"sourceType":"MANUAL","timestampGMT":"` + dailyDate + `T07:30:00.0"}]}],` +
		`"totalAverage":{"weight":71750}}`

	weighInDailyBody = `{"dateWeightList":[` +
		`{"weight":72500,"bmi":23.4,"bodyFat":18.2,"bodyWater":55.1,"boneMass":3200,` +
		`"muscleMass":32000,"sourceType":"MANUAL","timestampGMT":"` + dailyDate + `T07:30:00.0",` +
		`"samplePk":998877}],"totalAverage":{"weight":72500}}`

	weighInEmptyDailyBody = `{"dateWeightList":[],"totalAverage":{"weight":null}}`
)

func weighInRangePath(start, end string) string {
	return client.PathWeightRangePrefix + "/" + start + "/" + end
}

func weighInDayviewPath(date string) string {
	return client.PathWeightDayviewPrefix + "/" + date
}

func TestGetWeighInsDecodesTheWindowMostRecentFirst(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, testkit.NewScript().
		With(weighInRangePath(weighInOlderDate, dailyDate), testkit.JSON(http.StatusOK, weighInRangeBody)))

	got := h.call(t, ToolGetWeighIns, map[string]any{
		argStartDate: weighInOlderDate, argEndDate: dailyDate,
	})

	if count := number(t, got, "measurement_count"); count != 2 {
		t.Errorf("measurement_count = %v, want 2", count)
	}
	if days := number(t, got, "days_with_data"); days != 2 {
		t.Errorf("days_with_data = %v, want 2", days)
	}
	items := list(t, got, "measurements")
	if len(items) != 2 {
		t.Fatalf("measurements = %d entries, want 2", len(items))
	}
	// Most recent day first, matching weight_management.py:70-73.
	first := entry(t, items, 0)
	if first["date"] != dailyDate {
		t.Errorf("measurements[0].date = %v, want %q (most recent first)", first["date"], dailyDate)
	}
	if weightKg := number(t, first, "weight_kg"); weightKg != 72.5 {
		t.Errorf("weight_kg = %v, want 72.5", weightKg)
	}
	if weightGrams := number(t, first, "weight_grams"); weightGrams != 72500 {
		t.Errorf("weight_grams = %v, want 72500", weightGrams)
	}
	if bmi := number(t, first, "bmi"); bmi != 23.4 {
		t.Errorf("bmi = %v, want 23.4", bmi)
	}
	second := entry(t, items, 1)
	if second["date"] != weighInOlderDate {
		t.Errorf("measurements[1].date = %v, want %q", second["date"], weighInOlderDate)
	}

	if avgKg := number(t, got, "average_weight_kg"); avgKg != 71.75 {
		t.Errorf("average_weight_kg = %v, want 71.75", avgKg)
	}
	if truncated, _ := got["measurements_truncated"].(bool); truncated {
		t.Error("measurements_truncated = true, want false")
	}
}

func TestGetWeighInsRefusesAnInvertedOrOversizedWindow(t *testing.T) {
	t.Parallel()

	h := newToolHarnessWith(t, testkit.NewScript(), client.Limits{MaxDateRangeDays: 7})

	inverted := h.callError(t, ToolGetWeighIns, map[string]any{
		argStartDate: dailyDate, argEndDate: weighInOlderDate,
	})
	assertNoRawPayload(t, inverted)

	oversized := h.callError(t, ToolGetWeighIns, map[string]any{
		argStartDate: scoresStartDate, argEndDate: "2026-02-01",
	})
	assertNoRawPayload(t, oversized)

	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("a refused window still reached Garmin: %d requests", got)
	}
}

func TestGetDailyWeighInsDecodesTheDay(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, testkit.NewScript().
		With(weighInDayviewPath(dailyDate), testkit.JSON(http.StatusOK, weighInDailyBody)))

	got := h.call(t, ToolGetDailyWeighIns, map[string]any{argDate: dailyDate})

	if got["date"] != dailyDate {
		t.Errorf("date = %v, want %q", got["date"], dailyDate)
	}
	if count := number(t, got, "measurement_count"); count != 1 {
		t.Errorf("measurement_count = %v, want 1", count)
	}
	items := list(t, got, "measurements")
	if len(items) != 1 {
		t.Fatalf("measurements = %d entries, want 1", len(items))
	}
	reading := entry(t, items, 0)
	// get_daily_weigh_ins' own curation carries no per-item date key
	// (weight_management.py:109-120): the day is already the tool's argument.
	if _, present := reading["date"]; present {
		t.Errorf("measurements[0] carries a date field, want none (day is the tool's own argument)")
	}
	if weightKg := number(t, reading, "weight_kg"); weightKg != 72.5 {
		t.Errorf("weight_kg = %v, want 72.5", weightKg)
	}
	if source := reading["source_type"]; source != "MANUAL" {
		t.Errorf("source_type = %v, want MANUAL", source)
	}
}

func TestGetDailyWeighInsWithNoEntriesReportsAnEmptyResult(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, testkit.NewScript().
		With(weighInDayviewPath(dailyDate), testkit.JSON(http.StatusOK, weighInEmptyDailyBody)))

	got := h.call(t, ToolGetDailyWeighIns, map[string]any{argDate: dailyDate})

	if count := number(t, got, "measurement_count"); count != 0 {
		t.Errorf("measurement_count = %v, want 0", count)
	}
	if _, present := got["average_weight_kg"]; present {
		t.Error(`average_weight_kg is present, want omitted for a null average`)
	}
}

func TestGetDailyWeighInsRefusesAMalformedDate(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, testkit.NewScript())

	advice := h.callError(t, ToolGetDailyWeighIns, map[string]any{argDate: malformedDate})
	assertNoRawPayload(t, advice)

	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("a refused date still reached Garmin: %d requests", got)
	}
}
