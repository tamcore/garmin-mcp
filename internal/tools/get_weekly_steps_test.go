package tools

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func weeklyStepsPath(end string, weeks int) string {
	return client.PathWeeklyStepsStatsPrefix + "/" + end + "/" + strconv.Itoa(weeks)
}

const weeklyStepsFixture = `[{"calendarDate":"2026-01-05","values":{"totalSteps":61234,` +
	`"averageSteps":8747.7,"totalDistance":49000,"averageDistance":7000,` +
	`"wellnessDataDaysCount":7}},{"calendarDate":"2026-01-12","values":null}]`

func TestGetWeeklyStepsCuratesTheNestedValuesMostRecentFirst(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(weeklyStepsPath(dailyDate, defaultWeeksArgument),
			testkit.JSON(http.StatusOK, weeklyStepsFixture)))

	got := h.call(t, ToolGetWeeklySteps, map[string]any{argEndDate: dailyDate})
	if got[argEndDate] != dailyDate {
		t.Errorf("end_date = %v, want the day that was asked for", got[argEndDate])
	}
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
	// The most recent week carries no values, so it must report none rather than
	// zeroes: a week Garmin holds nothing for is not a week of no steps.
	if _, present := first["total_steps"]; present {
		t.Error("a week with no values reported a step count")
	}

	second, _ := weekly[1].(map[string]any)
	for _, key := range []string{
		"total_steps", "average_steps", "total_distance_meters",
		"average_distance_meters", "days_with_data",
	} {
		if _, present := second[key]; !present {
			t.Errorf("the curated week is missing %q", key)
		}
	}
}

func TestGetWeeklyStepsAppliesTheWeekCount(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(weeklyStepsPath(dailyDate, 2), testkit.JSON(http.StatusOK, weeklyStepsFixture)))

	got := h.call(t, ToolGetWeeklySteps, map[string]any{argEndDate: dailyDate, argWeeks: 2})
	if got["weeks_requested"] != float64(2) {
		t.Errorf("weeks_requested = %v, want 2", got["weeks_requested"])
	}
}

func TestGetWeeklyStepsRefusesAnOutOfRangeWeekCount(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript())

	for _, weeks := range []int{0, maxWeeksArgument + 1} {
		result := h.rawCall(t, ToolGetWeeklySteps,
			map[string]any{argEndDate: dailyDate, argWeeks: weeks})
		if !result.IsError {
			t.Errorf("get_weekly_steps accepted weeks = %d", weeks)
		}
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

func TestResolveWeeksAppliesTheDefaultAndTheBounds(t *testing.T) {
	t.Parallel()

	weeks, err := resolveWeeks(nil)
	if err != nil || weeks != defaultWeeksArgument {
		t.Errorf("resolveWeeks(nil) = %d, %v, want the manifest default", weeks, err)
	}

	zero, over := 0, maxWeeksArgument+1
	if _, err := resolveWeeks(&zero); err == nil {
		t.Error("resolveWeeks accepted zero weeks")
	}
	if _, err := resolveWeeks(&over); err == nil {
		t.Error("resolveWeeks accepted a week count past the cap")
	}
}

func TestWeeklyStepsLogValueReportsShapeAndNoReading(t *testing.T) {
	t.Parallel()

	steps := 61234
	weekly := WeeklySteps{
		EndDate:        dailyDate,
		WeeksRequested: 4,
		WeeksReturned:  1,
		WeeklyData:     []WeeklyStepWeek{{TotalSteps: &steps}},
	}

	rendered := weekly.LogValue().String()
	if strings.Contains(rendered, "61234") {
		t.Errorf("LogValue rendered a step count: %s", rendered)
	}
	if !strings.Contains(rendered, "weeksReturned=1") {
		t.Errorf("LogValue = %s, want the week count", rendered)
	}
}
