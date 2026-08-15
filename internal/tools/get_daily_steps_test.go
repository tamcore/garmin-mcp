package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func dailyStepsStatsPath(start, end string) string {
	return client.PathDailyStepsStatsPrefix + "/" + start + "/" + end
}

func TestGetDailyStepsCuratesTheFlatDayRecord(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(dailyStepsStatsPath(dailyWindowStart, "2026-01-07"),
			testkit.JSON(http.StatusOK,
				`[{"calendarDate":"2026-01-01","totalSteps":9123,`+
					`"totalDistance":7345,"stepGoal":8000}]`)))

	got := h.call(t, ToolGetDailySteps, map[string]any{
		argStartDate: dailyWindowStart, argEndDate: "2026-01-07",
	})
	days, ok := got["days"].([]any)
	if !ok || len(days) != 1 {
		t.Fatalf("days = %v, want one record", got["days"])
	}
	record, ok := days[0].(map[string]any)
	if !ok {
		t.Fatalf("the record = %T, want an object", days[0])
	}
	if record[argDate] != dailyWindowStart {
		t.Errorf("date = %v, want the day the record is for", record[argDate])
	}
	if record["total_steps"] != float64(9123) || record["step_goal"] != float64(8000) {
		t.Errorf("the record lost its curated fields: %v", record)
	}
	// The unit is unstated by Garmin, so the key must not claim one. A
	// distance_meters key here would be an assertion no source supports.
	if record["total_distance"] != float64(7345) {
		t.Errorf("total_distance = %v, want the unlabelled distance", record["total_distance"])
	}
	if _, present := record["distance_meters"]; present {
		t.Error("the result claims a unit Garmin never stated")
	}
	if got["count"] != float64(1) || got["truncated"] != false {
		t.Errorf("count/truncated = %v/%v, want 1/false", got["count"], got["truncated"])
	}
}

// TestGetDailyStepsDoesNotAlignTheListToTheWindow is the reason each record carries
// its own date. A day the watch was not worn is missing from Garmin's answer, so a
// caller that read the list by offset would attribute one day's steps to another.
func TestGetDailyStepsDoesNotAlignTheListToTheWindow(t *testing.T) {
	t.Parallel()

	// A five-day window, two days answered, and in descending order.
	h := newToolHarness(t, dailyScript().
		With(dailyStepsStatsPath(dailyWindowStart, dailyWindowEnd),
			testkit.JSON(http.StatusOK,
				`[{"calendarDate":"`+dailyWindowEnd+`","totalSteps":42},`+
					`{"calendarDate":"2026-01-01","totalSteps":7}]`)))

	got := h.call(t, ToolGetDailySteps, map[string]any{
		argStartDate: dailyWindowStart, argEndDate: dailyWindowEnd,
	})
	if got["count"] != float64(2) {
		t.Fatalf("count = %v, want the two days Garmin held, not the five asked for",
			got["count"])
	}

	days, _ := got["days"].([]any)
	dates := make([]any, 0, len(days))
	for _, day := range days {
		record, _ := day.(map[string]any)
		dates = append(dates, record[argDate])
	}
	// Garmin's own order is preserved and no day is invented to fill the gap.
	if len(dates) != 2 || dates[0] != dailyWindowEnd || dates[1] != dailyWindowStart {
		t.Errorf("dates = %v, want Garmin's answer unreordered and unpadded", dates)
	}
}

// TestGetDailyStepsReadsAWideWindowInParts pins the 28-day chunking, because a
// single wider request is the one shape Garmin refuses outright.
func TestGetDailyStepsReadsAWideWindowInParts(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(dailyStepsStatsPath(dailyWindowStart, "2026-01-28"),
			testkit.JSON(http.StatusOK, `[{"calendarDate":"2026-01-01"}]`)).
		With(dailyStepsStatsPath("2026-01-29", "2026-02-05"),
			testkit.JSON(http.StatusOK, `[{"calendarDate":"2026-01-29"}]`)))

	got := h.call(t, ToolGetDailySteps, map[string]any{
		argStartDate: dailyWindowStart, argEndDate: "2026-02-05",
	})
	if got["count"] != float64(2) {
		t.Fatalf("count = %v, want one record per chunk", got["count"])
	}
	if requests := len(h.fake.Requests()); requests != 2 {
		t.Errorf("the fake received %d requests, want two chunks", requests)
	}
}

func TestGetDailyStepsRefusesAnOversizedWindow(t *testing.T) {
	t.Parallel()

	h := newToolHarnessWith(t, dailyScript(), client.Limits{MaxDateRangeDays: 7})

	advice := h.callError(t, ToolGetDailySteps, map[string]any{
		argStartDate: dailyWindowStart, argEndDate: "2026-02-05",
	})
	if !strings.Contains(advice, "7 days") {
		t.Errorf("advice = %q, want the configured window bound", advice)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestGetDailyStepsRequiresBothDays(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript())

	result := h.rawCall(t, ToolGetDailySteps, map[string]any{argStartDate: dailyDate})
	if !result.IsError {
		t.Error("get_daily_steps accepted a call with no end_date")
	}
}

func TestDailyStepsWindowLogValueReportsShapeAndNoReading(t *testing.T) {
	t.Parallel()

	steps, distance := 9123, 7345.0
	window := DailyStepsWindow{
		StartDate: dailyWindowStart,
		EndDate:   dailyDate,
		Days:      []DailyStepDay{{TotalSteps: &steps, TotalDistance: &distance}},
		Count:     1,
		Truncated: true,
	}

	rendered := window.LogValue().String()
	for _, reading := range []string{"9123", "7345"} {
		if strings.Contains(rendered, reading) {
			t.Errorf("LogValue rendered the reading %s: %s", reading, rendered)
		}
	}
	if !strings.Contains(rendered, "truncated=true") {
		t.Errorf("LogValue = %s, want the truncation flag", rendered)
	}
}
