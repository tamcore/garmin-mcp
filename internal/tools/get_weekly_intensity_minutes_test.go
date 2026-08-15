package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func weeklyIntensityPath(start, end string) string {
	return client.PathWeeklyIntensityMinutesStatsPrefix + "/" + start + "/" + end
}

var weeklyIntensityFixture = `[{"calendarDate":"` + dailyWindowEnd + `","weeklyGoal":150,` +
	`"moderateValue":90,"vigorousValue":30},{"calendarDate":"2026-01-12",` +
	`"weeklyGoal":150,"moderateValue":null,"vigorousValue":null}]`

// TestGetWeeklyIntensityMinutesTurnsTheWeekCountIntoAWindow pins the start date the
// upstream tool computes, because this path is the one weekly aggregate keyed by a
// range rather than by a count.
func TestGetWeeklyIntensityMinutesTurnsTheWeekCountIntoAWindow(t *testing.T) {
	t.Parallel()

	// Four weeks ending 2026-01-31 is 2026-01-31 minus 27 days.
	h := newToolHarness(t, dailyScript().
		With(weeklyIntensityPath("2026-01-04", dailyDate),
			testkit.JSON(http.StatusOK, weeklyIntensityFixture)))

	got := h.call(t, ToolGetWeeklyIntensityMinutes, map[string]any{argEndDate: dailyDate})
	if got["weeks_requested"] != float64(defaultWeeksArgument) {
		t.Errorf("weeks_requested = %v, want the manifest default", got["weeks_requested"])
	}
	if got["weeks_returned"] != float64(2) {
		t.Errorf("weeks_returned = %v, want 2", got["weeks_returned"])
	}

	weekly, ok := got["weekly_data"].([]any)
	if !ok || len(weekly) != 2 {
		t.Fatalf("weekly_data = %v, want two weeks", got["weekly_data"])
	}
	first, _ := weekly[0].(map[string]any)
	if first["week_start"] != dailyWeekStart {
		t.Errorf("the first week starts %v, want the most recent week first", first["week_start"])
	}
	// A week with no measured values sums to nothing, and reports no moderate or
	// vigorous reading at all.
	if first["total_minutes"] != float64(0) {
		t.Errorf("total_minutes = %v, want 0 for a week with no values", first["total_minutes"])
	}
	if _, present := first["moderate_minutes"]; present {
		t.Error("a week with no values reported a moderate reading")
	}

	second, _ := weekly[1].(map[string]any)
	if second["total_minutes"] != float64(120) {
		t.Errorf("total_minutes = %v, want the plain sum of 90 and 30", second["total_minutes"])
	}
	if second["weekly_goal"] != float64(150) {
		t.Errorf("weekly_goal = %v, want 150", second["weekly_goal"])
	}
}

func TestGetWeeklyIntensityMinutesAppliesTheWeekCount(t *testing.T) {
	t.Parallel()

	// One week ending 2026-01-31 starts six days earlier.
	h := newToolHarness(t, dailyScript().
		With(weeklyIntensityPath("2026-01-25", dailyDate),
			testkit.JSON(http.StatusOK, weeklyIntensityFixture)))

	got := h.call(t, ToolGetWeeklyIntensityMinutes,
		map[string]any{argEndDate: dailyDate, argWeeks: 1})
	if got["weeks_requested"] != float64(1) {
		t.Errorf("weeks_requested = %v, want 1", got["weeks_requested"])
	}
	// One week was asked for, so at most one week comes back even when Garmin
	// answers with more.
	if got["weeks_returned"] != float64(1) {
		t.Errorf("weeks_returned = %v, want 1", got["weeks_returned"])
	}
}

func TestGetWeeklyIntensityMinutesRefusesAnOutOfRangeWeekCount(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript())

	for _, weeks := range []int{0, maxWeeksArgument + 1} {
		result := h.rawCall(t, ToolGetWeeklyIntensityMinutes,
			map[string]any{argEndDate: dailyDate, argWeeks: weeks})
		if !result.IsError {
			t.Errorf("get_weekly_intensity_minutes accepted weeks = %d", weeks)
		}
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestCompareWeekStartsDescendingSortsAnAbsentStartLast(t *testing.T) {
	t.Parallel()

	early, late := dailyWindowEnd, dailyWeekStart
	if compareWeekStartsDescending(&late, &early) >= 0 {
		t.Error("the later week must sort first")
	}
	if compareWeekStartsDescending(nil, &early) <= 0 {
		t.Error("an absent start must sort last")
	}
	if compareWeekStartsDescending(&early, nil) >= 0 {
		t.Error("a dated week must sort before an undated one")
	}
	if compareWeekStartsDescending(nil, nil) != 0 {
		t.Error("two absent starts must compare equal")
	}
}

func TestWeeklyIntensityMinutesLogValueReportsShapeAndNoReading(t *testing.T) {
	t.Parallel()

	moderate := 90
	weekly := WeeklyIntensityMinutes{
		EndDate:        dailyDate,
		WeeksRequested: 4,
		WeeksReturned:  1,
		WeeklyData:     []WeeklyIntensityWeek{{ModerateMinutes: &moderate, TotalMinutes: 90}},
	}

	rendered := weekly.LogValue().String()
	if strings.Contains(rendered, "90") {
		t.Errorf("LogValue rendered a minute count: %s", rendered)
	}
	if !strings.Contains(rendered, "weeksReturned=1") {
		t.Errorf("LogValue = %s, want the week count", rendered)
	}
}
