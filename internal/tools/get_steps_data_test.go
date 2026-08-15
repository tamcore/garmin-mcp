package tools

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// keySteps is the wire key the synthetic interval fixture carries.
const keySteps = "steps"

func dailyStepsChartPath() string {
	return client.PathDailySummaryChartPrefix + "/" + dailyName
}

// stepIntervals renders n synthetic buckets in the sourced wire shape. Every value
// is invented and no fixture is a recording of a real account.
func stepIntervals(n int) string {
	entries := make([]string, 0, n)
	for i := range n {
		entries = append(entries, `{"startGMT":"`+dailyDate+`T00:00:00.0",`+
			`"endGMT":"`+dailyDate+`T00:15:00.0","`+keySteps+`":`+strconv.Itoa(i)+
			`,"pushes":0,"primaryActivityLevel":"sedentary","activityLevelConstant":true}`)
	}
	return "[" + strings.Join(entries, ",") + "]"
}

func TestGetStepsDataKeepsGarminsOwnIntervalFields(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(dailyStepsChartPath(), testkit.JSON(http.StatusOK, stepIntervals(3))))

	got := h.call(t, ToolGetStepsData, map[string]any{argDate: dailyDate})
	intervals, ok := got["intervals"].([]any)
	if !ok || len(intervals) != 3 {
		t.Fatalf("intervals = %v, want three records", got["intervals"])
	}
	if got["count"] != float64(3) {
		t.Errorf("count = %v, want 3", got["count"])
	}
	if got["truncated"] != false {
		t.Errorf("truncated = %v, want false", got["truncated"])
	}

	first, ok := intervals[0].(map[string]any)
	if !ok {
		t.Fatalf("the first interval = %T, want an object", intervals[0])
	}
	for key, want := range map[string]any{
		"start_gmt":               dailyDate + "T00:00:00.0",
		"end_gmt":                 dailyDate + "T00:15:00.0",
		"primary_activity_level":  "sedentary",
		"activity_level_constant": true,
	} {
		if first[key] != want {
			t.Errorf("%s = %v, want %v", key, first[key], want)
		}
	}
	if date := h.fake.Requests()[1].Query.Get(client.QueryDate); date != dailyDate {
		t.Errorf("date = %q, want %q", date, dailyDate)
	}
}

// TestGetStepsDataTellsAZeroFromAnAbsentPush is the reason pushes is an optional
// pointer: it arrives present and zero on a walking account, and a device with no
// such concept omits it. Collapsing the two would report a push count Garmin never
// measured.
func TestGetStepsDataTellsAZeroFromAnAbsentPush(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(dailyStepsChartPath(), testkit.JSON(http.StatusOK,
			`[{"`+keySteps+`":0,"pushes":0},{"`+keySteps+`":0}]`)))

	got := h.call(t, ToolGetStepsData, map[string]any{argDate: dailyDate})
	intervals, _ := got["intervals"].([]any)
	if len(intervals) != 2 {
		t.Fatalf("intervals = %v, want two buckets", got["intervals"])
	}

	measured, _ := intervals[0].(map[string]any)
	if pushes, present := measured["pushes"]; !present || pushes != float64(0) {
		t.Errorf("pushes = %v/%v, want a measured zero", pushes, present)
	}
	unmeasured, _ := intervals[1].(map[string]any)
	if _, present := unmeasured["pushes"]; present {
		t.Error("pushes is present for a bucket Garmin sent none for")
	}
	// The same rule holds for a step count Garmin did send as zero.
	if steps, present := measured[keySteps]; !present || steps != float64(0) {
		t.Errorf("steps = %v/%v, want a measured zero", steps, present)
	}
}

// TestGetStepsDataPassesOnAnUnknownActivityLevel pins the open enum. Four values were
// observed on one day of one account, which is not the full range, so an unrecognized
// label must reach the caller rather than fail the read.
func TestGetStepsDataPassesOnAnUnknownActivityLevel(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(dailyStepsChartPath(), testkit.JSON(http.StatusOK,
			`[{"primaryActivityLevel":"aLevelNobodyHasSeen"},{"primaryActivityLevel":null}]`)))

	got := h.call(t, ToolGetStepsData, map[string]any{argDate: dailyDate})
	intervals, _ := got["intervals"].([]any)
	if len(intervals) != 2 {
		t.Fatalf("intervals = %v, want two buckets", got["intervals"])
	}
	unknown, _ := intervals[0].(map[string]any)
	if unknown["primary_activity_level"] != "aLevelNobodyHasSeen" {
		t.Errorf("primary_activity_level = %v, want the unknown label passed on",
			unknown["primary_activity_level"])
	}
	nulled, _ := intervals[1].(map[string]any)
	if _, present := nulled["primary_activity_level"]; present {
		t.Error("an explicit null became a value")
	}
}

func TestGetStepsDataCutsAnOversizedSeriesAndSaysSo(t *testing.T) {
	t.Parallel()

	h := newToolHarness(t, dailyScript().
		With(dailyStepsChartPath(),
			testkit.JSON(http.StatusOK, stepIntervals(maxStepIntervals+5))))

	got := h.call(t, ToolGetStepsData, map[string]any{argDate: dailyDate})
	if got["count"] != float64(maxStepIntervals) {
		t.Errorf("count = %v, want the bound %d", got["count"], maxStepIntervals)
	}
	if got["truncated"] != true {
		t.Error("truncated = false for a series that was cut")
	}
}

func TestGetStepsDataReportsADayWithNoSeries(t *testing.T) {
	t.Parallel()

	// Garmin answers a day it holds nothing for with null. That is an empty day,
	// not a failure.
	h := newToolHarness(t, dailyScript().
		With(dailyStepsChartPath(), testkit.JSON(http.StatusOK, "null")))

	got := h.call(t, ToolGetStepsData, map[string]any{argDate: dailyDate})
	if got["count"] != float64(0) {
		t.Errorf("count = %v, want 0", got["count"])
	}
}

func TestStepsDataLogValueReportsShapeAndNoReading(t *testing.T) {
	t.Parallel()

	steps := 4242
	level := "highlyActive"
	data := StepsData{
		Date:      dailyDate,
		Intervals: []StepInterval{{Steps: &steps, PrimaryActivityLevel: &level}},
		Count:     1,
	}

	rendered := data.LogValue().String()
	for _, reading := range []string{"4242", level} {
		if strings.Contains(rendered, reading) {
			t.Errorf("LogValue rendered the reading %s: %s", reading, rendered)
		}
	}
	if !strings.Contains(rendered, "intervals=1") {
		t.Errorf("LogValue = %s, want the interval count", rendered)
	}
}
