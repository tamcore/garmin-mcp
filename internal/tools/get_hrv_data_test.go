package tools

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// hrvDayBody is synthetic: one night with a baseline band and two readings.
const hrvDayBody = `{"hrvSummary":{"calendarDate":"2026-01-31","lastNightAvg":42.7,` +
	`"lastNight5MinHigh":78.3,"weeklyAvg":41.0,"status":"BALANCED",` +
	`"feedbackPhrase":"HRV_BALANCED","baseline":{"balancedLow":35.0,` +
	`"balancedUpper":55.0,"lowUpper":30.0}},` +
	`"sleepStartTimestampLocal":"2026-01-30T22:10:00.0",` +
	`"sleepEndTimestampLocal":"2026-01-31T06:10:00.0",` +
	`"hrvReadings":[{"readingTimeLocal":"2026-01-31T02:05:00.0","hrvValue":44.5},` +
	`{"readingTimeLocal":"2026-01-31T02:10:00.0","hrvValue":46.5}]}`

// hrvScript serves body for the single day these tests read.
func hrvScript(body string) testkit.Script {
	return testkit.NewScript().With(client.PathHRVPrefix+"/"+trendEnd,
		testkit.JSON(http.StatusOK, body))
}

func TestHRVDataReportsTheSummaryAndOmitsTheSeriesByDefault(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, hrvScript(hrvDayBody))
	out, err := h.svc.readHRVData(h.ctx, trendEnd, false)
	if err != nil {
		t.Fatalf("readHRVData() = %v", err)
	}

	if !out.HasData || out.Date != trendEnd {
		t.Errorf("result = %+v, want data for %s", out, trendEnd)
	}
	if out.LastNightAvgHRVMs == nil || *out.LastNightAvgHRVMs != 42.7 {
		t.Errorf("last_night_avg_hrv_ms = %v, want 42.7", out.LastNightAvgHRVMs)
	}
	if out.BaselineBalancedLowMs == nil || out.BaselineLowUpperMs == nil {
		t.Error("the baseline band did not reach the result")
	}
	if out.Status != "BALANCED" || out.Feedback != "HRV_BALANCED" {
		t.Errorf("status/feedback = %q/%q, want Garmin's phrases", out.Status, out.Feedback)
	}
	if out.SleepStart == "" || out.SleepEnd == "" {
		t.Error("the sleep window did not reach the result")
	}
	if len(out.Readings) != 0 || out.ReadingsCount != 0 {
		t.Errorf("readings = %+v, want none unless they were asked for", out.Readings)
	}
}

func TestHRVDataReturnsTheSeriesWhenItIsAskedFor(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, hrvScript(hrvDayBody))
	out, err := h.svc.readHRVData(h.ctx, trendEnd, true)
	if err != nil {
		t.Fatalf("readHRVData() = %v", err)
	}
	if len(out.Readings) != 2 || out.ReadingsCount != 2 {
		t.Fatalf("readings = %+v (count %d), want 2", out.Readings, out.ReadingsCount)
	}
	if out.ReadingsTruncated {
		t.Error("a two-reading night reports itself truncated")
	}
	if out.Readings[0].HRVMs == nil || *out.Readings[0].HRVMs != 44.5 {
		t.Errorf("first reading = %v, want 44.5", out.Readings[0].HRVMs)
	}
}

// TestHRVDataTruncatesAnOversizedSeries proves the bound is applied and reported: the
// full count Garmin sent stays visible, so nothing is silently dropped.
func TestHRVDataTruncatesAnOversizedSeries(t *testing.T) {
	t.Parallel()

	oversized := DefaultMaxHRVReadings + 5
	var body strings.Builder
	body.WriteString(`{"hrvSummary":{"lastNightAvg":42.0},"hrvReadings":[`)
	for index := range oversized {
		if index > 0 {
			body.WriteString(",")
		}
		body.WriteString(`{"readingTimeLocal":"2026-01-31T02:05:00.0","hrvValue":`)
		body.WriteString(strconv.Itoa(40 + index%5))
		body.WriteString(`}`)
	}
	body.WriteString(`]}`)

	h := newTrendHarness(t, hrvScript(body.String()))
	out, err := h.svc.readHRVData(h.ctx, trendEnd, true)
	if err != nil {
		t.Fatalf("readHRVData() = %v", err)
	}
	if len(out.Readings) != DefaultMaxHRVReadings {
		t.Errorf("readings = %d, want the bound %d", len(out.Readings), DefaultMaxHRVReadings)
	}
	if !out.ReadingsTruncated {
		t.Error("the cut series does not report itself truncated")
	}
	if out.ReadingsCount != oversized {
		t.Errorf("readings_count = %d, want the full count %d Garmin sent",
			out.ReadingsCount, oversized)
	}
}

func TestHRVDataReportsADayWithNoReadings(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, hrvScript(`{"hrvSummary":null}`))
	out, err := h.svc.readHRVData(h.ctx, trendEnd, true)
	if err != nil {
		t.Fatalf("readHRVData() = %v", err)
	}
	if out.HasData {
		t.Errorf("result = %+v, want has_data false", out)
	}
}

func TestHRVDataRefusesABadDateAndAnAnonymousRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, hrvScript(hrvDayBody))
	if _, err := h.svc.readHRVData(h.ctx, "31-01-2026", false); !errors.Is(
		err, ErrInvalidArgument) {
		t.Errorf("a malformed date = %v, want ErrInvalidArgument", err)
	}
	if _, err := h.svc.readHRVData(t.Context(), trendEnd, false); !errors.Is(
		err, identity.ErrNoPrincipal) {
		t.Errorf("an anonymous request = %v, want ErrNoPrincipal", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestHRVDataResultNeverLogsAReading is the redaction rule: an HRV figure is a health
// reading, and the result reports its shape only.
func TestHRVDataResultNeverLogsAReading(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, hrvScript(hrvDayBody))
	out, err := h.svc.readHRVData(h.ctx, trendEnd, true)
	if err != nil {
		t.Fatalf("readHRVData() = %v", err)
	}
	assertShapeOnly(t, "HRVData", out, "42.7", "78.3", "44.5", "BALANCED")
}

// TestHRVDataReportsDataWhenOnlyTheBaselineArrived is the regression for a result
// that contradicted itself.
//
// has_data tested only the three night averages, so a day carrying a baseline, a
// status and a sleep window — but no average — returned every one of those fields
// beside has_data:false. A caller that trusts the flag drops a populated answer.
func TestHRVDataReportsDataWhenOnlyTheBaselineArrived(t *testing.T) {
	t.Parallel()

	baselineOnly := `{"hrvSummary":{"baseline":{"balancedLow":42,"balancedUpper":68,` +
		`"lowUpper":38},"status":"BALANCED"}}`
	h := newTrendHarness(t, hrvScript(baselineOnly))

	out, err := h.svc.readHRVData(h.ctx, trendEnd, false)
	if err != nil {
		t.Fatalf("readHRVData() = %v", err)
	}

	if out.BaselineBalancedLowMs == nil {
		t.Fatal("the baseline was dropped, so this test proves nothing")
	}
	if !out.HasData {
		t.Error("has_data = false beside a populated baseline, status and window")
	}
}
